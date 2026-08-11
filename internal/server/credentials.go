package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/fr0stylo/tearenv/internal/scaler"
)

const minimumTokenLength = 16

var validIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`)
var validServiceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

type Service struct {
	Name      string
	Target    string
	LocalPort uint32
	Workload  *Workload
}

type Workload struct {
	Kind         string
	Namespace    string
	Name         string
	Replicas     int32
	ReadyTimeout time.Duration
	IdleTimeout  time.Duration
}

func (workload Workload) scalerTarget() scaler.Target {
	return scaler.Target{
		Kind:      workload.Kind,
		Namespace: workload.Namespace,
		Name:      workload.Name,
	}
}

// Credentials authenticates users and persists one-time enrollment invites.
type Credentials struct {
	mu       sync.RWMutex
	path     string
	users    map[string][sha256.Size]byte
	invites  map[string]string
	services map[string]map[string]Service
}

type credentialsFile struct {
	Users   map[string]credentialRecord `json:"users"`
	Invites map[string]inviteRecord     `json:"invites,omitempty"`
	Access  map[string]accessRecord     `json:"access,omitempty"`
}

type credentialRecord struct {
	Token     string `json:"token,omitempty"` // Legacy input; rewritten as a hash on mutation.
	TokenHash string `json:"token_hash,omitempty"`
}

type inviteRecord struct {
	Identity string `json:"identity"`
}

type accessRecord struct {
	Services map[string]serviceRecord `json:"services"`
}

type serviceRecord struct {
	Target    string          `json:"target"`
	LocalPort uint32          `json:"local_port,omitempty"`
	Workload  *workloadRecord `json:"workload,omitempty"`
}

type workloadRecord struct {
	Kind         string `json:"kind"`
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	Replicas     int32  `json:"replicas,omitempty"`
	ReadyTimeout string `json:"ready_timeout,omitempty"`
	IdleTimeout  string `json:"idle_timeout,omitempty"`
}

// LoadCredentials loads a protected JSON credential file.
func LoadCredentials(path string) (*Credentials, error) {
	document, err := readCredentialsFile(path)
	if err != nil {
		return nil, err
	}
	users, invites, services, err := parseCredentials(document)
	if err != nil {
		return nil, err
	}
	return &Credentials{path: path, users: users, invites: invites, services: services}, nil
}

// NewCredentials creates an in-memory credential set, primarily for embedding
// and tests. Enrollment requires a file-backed set loaded with LoadCredentials.
func NewCredentials(users map[string]string) (*Credentials, error) {
	if len(users) == 0 {
		return nil, errors.New("credentials must contain at least one user")
	}
	hashes := make(map[string][sha256.Size]byte, len(users))
	for identity, token := range users {
		if err := validateIdentity(identity); err != nil {
			return nil, err
		}
		if len(token) < minimumTokenLength {
			return nil, fmt.Errorf("token for identity %q must contain at least %d characters", identity, minimumTokenLength)
		}
		hashes[identity] = sha256.Sum256([]byte(token))
	}
	return &Credentials{users: hashes, invites: make(map[string]string), services: make(map[string]map[string]Service)}, nil
}

// CreateInvite creates or replaces a one-time invite for identity and returns
// its plaintext value. Only a hash is persisted.
func CreateInvite(path, identity string) (string, error) {
	if err := validateIdentity(identity); err != nil {
		return "", err
	}
	document := credentialsFile{
		Users:   make(map[string]credentialRecord),
		Invites: make(map[string]inviteRecord),
	}
	if _, err := os.Stat(path); err == nil {
		loaded, err := readCredentialsFile(path)
		if err != nil {
			return "", err
		}
		document = loaded
		users, _, _, err := parseCredentials(document)
		if err != nil {
			return "", err
		}
		setHashedUsers(&document, users)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat credentials file %q: %w", path, err)
	}
	if document.Users == nil {
		document.Users = make(map[string]credentialRecord)
	}
	if document.Invites == nil {
		document.Invites = make(map[string]inviteRecord)
	}
	for hash, invite := range document.Invites {
		if invite.Identity == identity {
			delete(document.Invites, hash)
		}
	}

	invite, err := randomToken("ti_")
	if err != nil {
		return "", err
	}
	document.Invites[tokenHashText(invite)] = inviteRecord{Identity: identity}
	if err := writeCredentialsFile(path, document); err != nil {
		return "", err
	}
	return invite, nil
}

// GrantService binds a named service target to one identity.
func GrantService(path, identity string, service Service) error {
	if err := validateIdentity(identity); err != nil {
		return err
	}
	service, err := validateService(service)
	if err != nil {
		return err
	}
	document, err := readCredentialsFile(path)
	if err != nil {
		return err
	}
	users, invites, _, err := parseCredentials(document)
	if err != nil {
		return err
	}
	_, registered := users[identity]
	invited := false
	for _, invitedIdentity := range invites {
		if invitedIdentity == identity {
			invited = true
			break
		}
	}
	if !registered && !invited {
		return fmt.Errorf("identity %q must be registered or invited before granting services", identity)
	}
	if document.Access == nil {
		document.Access = make(map[string]accessRecord)
	}
	access := document.Access[identity]
	if access.Services == nil {
		access.Services = make(map[string]serviceRecord)
	}
	record := serviceRecord{Target: service.Target, LocalPort: service.LocalPort}
	if service.Workload != nil {
		record.Workload = &workloadRecord{
			Kind: service.Workload.Kind, Namespace: service.Workload.Namespace, Name: service.Workload.Name,
			Replicas: service.Workload.Replicas, ReadyTimeout: service.Workload.ReadyTimeout.String(), IdleTimeout: service.Workload.IdleTimeout.String(),
		}
	}
	access.Services[service.Name] = record
	document.Access[identity] = access
	setHashedUsers(&document, users)
	return writeCredentialsFile(path, document)
}

// Authenticate reports whether token belongs to identity.
func (credentials *Credentials) Authenticate(identity, token string) bool {
	credentials.mu.RLock()
	defer credentials.mu.RUnlock()
	expected, exists := credentials.users[identity]
	provided := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(provided[:], expected[:]) == 1 && exists
}

// AuthenticateInvite checks a one-time invite, reloading file-backed state so
// invites issued by a separate tearenvd process are visible immediately.
func (credentials *Credentials) AuthenticateInvite(identity, invite string) bool {
	credentials.mu.Lock()
	defer credentials.mu.Unlock()
	if credentials.path == "" {
		return false
	}
	if err := credentials.reloadLocked(); err != nil {
		return false
	}
	registeredIdentity, exists := credentials.invites[tokenHashText(invite)]
	return exists && registeredIdentity == identity
}

// Enroll consumes an invite, persists the identity, and returns a new personal token.
func (credentials *Credentials) Enroll(identity, invite string) (string, error) {
	credentials.mu.Lock()
	defer credentials.mu.Unlock()
	if credentials.path == "" {
		return "", errors.New("enrollment requires file-backed credentials")
	}
	document, err := readCredentialsFile(credentials.path)
	if err != nil {
		return "", err
	}
	users, invites, _, err := parseCredentials(document)
	if err != nil {
		return "", err
	}
	inviteHash := tokenHashText(invite)
	invitedIdentity, exists := invites[inviteHash]
	if !exists || invitedIdentity != identity {
		return "", errors.New("invite is invalid or already used")
	}
	token, err := randomToken("tu_")
	if err != nil {
		return "", err
	}
	if document.Users == nil {
		document.Users = make(map[string]credentialRecord)
	}
	setHashedUsers(&document, users)
	document.Users[identity] = credentialRecord{TokenHash: tokenHashText(token)}
	delete(document.Invites, inviteHash)
	if err := writeCredentialsFile(credentials.path, document); err != nil {
		return "", err
	}
	credentials.users, credentials.invites, credentials.services, err = parseCredentials(document)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (credentials *Credentials) reloadLocked() error {
	document, err := readCredentialsFile(credentials.path)
	if err != nil {
		return err
	}
	users, invites, services, err := parseCredentials(document)
	if err != nil {
		return err
	}
	credentials.users = users
	credentials.invites = invites
	credentials.services = services
	return nil
}

// Services returns the services granted to identity, reloading file-backed policy.
func (credentials *Credentials) Services(identity string) ([]Service, error) {
	credentials.mu.Lock()
	defer credentials.mu.Unlock()
	if credentials.path != "" {
		if err := credentials.reloadLocked(); err != nil {
			return nil, err
		}
	}
	grants := credentials.services[identity]
	services := make([]Service, 0, len(grants))
	for _, service := range grants {
		services = append(services, service)
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	return services, nil
}

// ResolveService returns the server-side target granted to identity.
func (credentials *Credentials) ResolveService(identity, name string) (Service, bool) {
	credentials.mu.Lock()
	defer credentials.mu.Unlock()
	if credentials.path != "" {
		if err := credentials.reloadLocked(); err != nil {
			return Service{}, false
		}
	}
	service, exists := credentials.services[identity][name]
	return service, exists
}

func readCredentialsFile(path string) (credentialsFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return credentialsFile{}, fmt.Errorf("stat credentials file %q: %w", path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return credentialsFile{}, fmt.Errorf("credentials file %q permissions are %o; want 600", path, info.Mode().Perm())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return credentialsFile{}, fmt.Errorf("read credentials file %q: %w", path, err)
	}
	var document credentialsFile
	if err := json.Unmarshal(contents, &document); err != nil {
		return credentialsFile{}, fmt.Errorf("parse credentials file %q: %w", path, err)
	}
	return document, nil
}

func parseCredentials(document credentialsFile) (map[string][sha256.Size]byte, map[string]string, map[string]map[string]Service, error) {
	if len(document.Users) == 0 && len(document.Invites) == 0 {
		return nil, nil, nil, errors.New("credentials must contain at least one user or invite")
	}
	users := make(map[string][sha256.Size]byte, len(document.Users))
	for identity, record := range document.Users {
		if err := validateIdentity(identity); err != nil {
			return nil, nil, nil, err
		}
		var hash [sha256.Size]byte
		switch {
		case record.TokenHash != "":
			decoded, err := hex.DecodeString(record.TokenHash)
			if err != nil || len(decoded) != sha256.Size {
				return nil, nil, nil, fmt.Errorf("invalid token hash for identity %q", identity)
			}
			copy(hash[:], decoded)
		case len(record.Token) >= minimumTokenLength:
			hash = sha256.Sum256([]byte(record.Token))
		default:
			return nil, nil, nil, fmt.Errorf("missing or short token for identity %q", identity)
		}
		users[identity] = hash
	}
	invites := make(map[string]string, len(document.Invites))
	for hash, invite := range document.Invites {
		if decoded, err := hex.DecodeString(hash); err != nil || len(decoded) != sha256.Size {
			return nil, nil, nil, errors.New("invalid invite hash")
		}
		if err := validateIdentity(invite.Identity); err != nil {
			return nil, nil, nil, err
		}
		invites[hash] = invite.Identity
	}
	services := make(map[string]map[string]Service, len(document.Access))
	for identity, access := range document.Access {
		if err := validateIdentity(identity); err != nil {
			return nil, nil, nil, err
		}
		services[identity] = make(map[string]Service, len(access.Services))
		for name, record := range access.Services {
			service := Service{Name: name, Target: record.Target, LocalPort: record.LocalPort}
			if record.Workload != nil {
				readyTimeout, err := parseDuration(record.Workload.ReadyTimeout, 2*time.Minute)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("service %q ready timeout: %w", name, err)
				}
				idleTimeout, err := parseDuration(record.Workload.IdleTimeout, 10*time.Minute)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("service %q idle timeout: %w", name, err)
				}
				service.Workload = &Workload{
					Kind: record.Workload.Kind, Namespace: record.Workload.Namespace, Name: record.Workload.Name,
					Replicas: record.Workload.Replicas, ReadyTimeout: readyTimeout, IdleTimeout: idleTimeout,
				}
			}
			service, err := validateService(service)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("service %q for identity %q: %w", name, identity, err)
			}
			services[identity][name] = service
		}
	}
	return users, invites, services, nil
}

func writeCredentialsFile(path string, document credentialsFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create credentials directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tearenv-users-*")
	if err != nil {
		return fmt.Errorf("create temporary credentials file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect temporary credentials file: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode credentials: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync credentials: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close credentials: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace credentials file: %w", err)
	}
	return nil
}

func setHashedUsers(document *credentialsFile, users map[string][sha256.Size]byte) {
	document.Users = make(map[string]credentialRecord, len(users))
	for identity, hash := range users {
		document.Users[identity] = credentialRecord{TokenHash: hex.EncodeToString(hash[:])}
	}
}

func randomToken(prefix string) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(bytes), nil
}

func tokenHashText(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func validateIdentity(identity string) error {
	if !validIdentity.MatchString(identity) {
		return fmt.Errorf("identity %q must match %s", identity, validIdentity.String())
	}
	return nil
}

func validateService(service Service) (Service, error) {
	if !validServiceName.MatchString(service.Name) {
		return Service{}, fmt.Errorf("service name %q must match %s", service.Name, validServiceName.String())
	}
	host, portText, err := net.SplitHostPort(service.Target)
	if err != nil || host == "" {
		return Service{}, fmt.Errorf("target %q must be host:port", service.Target)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return Service{}, fmt.Errorf("target %q has an invalid port", service.Target)
	}
	if service.LocalPort == 0 {
		service.LocalPort = uint32(port)
	}
	if service.LocalPort > 65535 {
		return Service{}, fmt.Errorf("local port %d is invalid", service.LocalPort)
	}
	if service.Workload != nil {
		workload := *service.Workload
		if workload.Kind == "" {
			return Service{}, errors.New("workload kind is required")
		}
		if workload.Name == "" {
			return Service{}, errors.New("workload name is required")
		}
		if workload.Replicas == 0 {
			workload.Replicas = 1
		}
		if workload.Replicas < 0 {
			return Service{}, errors.New("workload replicas cannot be negative")
		}
		if workload.ReadyTimeout == 0 {
			workload.ReadyTimeout = 2 * time.Minute
		}
		if workload.ReadyTimeout < 0 || workload.IdleTimeout < 0 {
			return Service{}, errors.New("workload timeouts cannot be negative")
		}
		service.Workload = &workload
	}
	return service, nil
}

func parseDuration(value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	return time.ParseDuration(value)
}
