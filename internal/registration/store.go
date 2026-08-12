// Package registration persists and authenticates UserRegistration resources.
package registration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
	"github.com/fr0stylo/tearenv/internal/authorization"
	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
)

var (
	// ErrNotFound reports a resource path that has not been registered.
	ErrNotFound = errors.New("user registration not found")
	// ErrConflict reports an attempt to replace an existing registration spec.
	ErrConflict = errors.New("user registration conflicts with the stored resource")
)

// Store keeps API resources as protected YAML files and uses accepted keys for
// SSH public-key authentication.
type Store struct {
	mu                      sync.RWMutex
	root                    string
	authenticationNamespace string
}

// NewStore opens or creates a file-backed registration store.
func NewStore(root, authenticationNamespace string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("registration store path is required")
	}
	if problems := validation.IsDNS1123Label(authenticationNamespace); len(problems) != 0 {
		return nil, fmt.Errorf("authentication namespace %q is invalid: %s", authenticationNamespace, strings.Join(problems, "; "))
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create registration store %q: %w", root, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat registration store %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("registration store %q is not a directory", root)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("registration store %q permissions are %o; group/world write access is not allowed", root, info.Mode().Perm())
	}
	return &Store{root: root, authenticationNamespace: authenticationNamespace}, nil
}

// Put creates a registration or returns the existing resource when its spec is
// identical. Specs are immutable so an unauthenticated retry cannot replace a
// previously registered key.
func (store *Store) Put(registration v1alpha1.UserRegistration) (v1alpha1.UserRegistration, bool, error) {
	if err := registration.Validate(); err != nil {
		return v1alpha1.UserRegistration{}, false, err
	}
	if registration.Namespace == "" {
		return v1alpha1.UserRegistration{}, false, errors.New("metadata.namespace is required")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	existing, err := store.getUnlocked(registration.Namespace, registration.Name)
	if err == nil {
		if !reflect.DeepEqual(existing.Spec, registration.Spec) {
			return v1alpha1.UserRegistration{}, false, ErrConflict
		}
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return v1alpha1.UserRegistration{}, false, err
	}

	identifier, err := randomIdentifier()
	if err != nil {
		return v1alpha1.UserRegistration{}, false, err
	}
	now := metav1.NewTime(time.Now().UTC())
	stored := v1alpha1.UserRegistration{
		TypeMeta: metav1.TypeMeta{APIVersion: v1alpha1.APIVersion, Kind: v1alpha1.UserRegistrationKind},
		ObjectMeta: metav1.ObjectMeta{
			Name:              registration.Name,
			Namespace:         registration.Namespace,
			UID:               types.UID(identifier),
			ResourceVersion:   identifier,
			Generation:        1,
			CreationTimestamp: now,
		},
		Spec: v1alpha1.UserRegistrationSpec{
			Identity:   registration.Spec.Identity,
			PublicKeys: append([]v1alpha1.SSHPublicKey(nil), registration.Spec.PublicKeys...),
		},
		Status: &v1alpha1.UserRegistrationStatus{
			ObservedGeneration: 1,
			Conditions: []metav1.Condition{{
				Type:               v1alpha1.ConditionAccepted,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 1,
				Reason:             "AcceptedByDefault",
				Message:            "Registration is accepted automatically.",
				LastTransitionTime: now,
			}},
		},
	}
	contents, err := v1alpha1.MarshalUserRegistration(stored)
	if err != nil {
		return v1alpha1.UserRegistration{}, false, fmt.Errorf("encode stored user registration: %w", err)
	}
	if err := store.writeUnlocked(stored.Namespace, stored.Name, contents); err != nil {
		return v1alpha1.UserRegistration{}, false, err
	}
	return stored, true, nil
}

// Get loads one persisted registration.
func (store *Store) Get(namespace, name string) (v1alpha1.UserRegistration, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.getUnlocked(namespace, name)
}

// Authenticate verifies a public key against accepted registrations in the
// configured namespace. Password authentication is intentionally unsupported.
func (store *Store) Authenticate(_ context.Context, attempt authorization.Attempt) (authorization.Result, bool, error) {
	if attempt.Method != authorization.MethodPublicKey || attempt.PublicKey == nil {
		return authorization.Result{}, false, nil
	}
	registrations, err := store.list(store.authenticationNamespace)
	if err != nil {
		return authorization.Result{}, false, err
	}
	for _, registration := range registrations {
		if registration.Spec.Identity != attempt.Identity || !registration.Accepted() {
			continue
		}
		for _, encoded := range registration.Spec.PublicKeys {
			candidate, _, _, _, parseErr := ssh.ParseAuthorizedKey([]byte(encoded.Key))
			if parseErr != nil {
				return authorization.Result{}, false, fmt.Errorf("parse stored public key %q: %w", encoded.Name, parseErr)
			}
			if bytes.Equal(candidate.Marshal(), attempt.PublicKey.Marshal()) {
				return authorization.Result{Identity: attempt.Identity, Provider: "user-registration"}, true, nil
			}
		}
	}
	return authorization.Result{}, false, nil
}

func (store *Store) list(namespace string) ([]v1alpha1.UserRegistration, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	directory := filepath.Join(store.root, namespace)
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read registration namespace %q: %w", namespace, err)
	}
	registrations := make([]v1alpha1.UserRegistration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		contents, readErr := readStoredFile(filepath.Join(directory, entry.Name()))
		if readErr != nil {
			return nil, fmt.Errorf("read user registration %q: %w", entry.Name(), readErr)
		}
		registration, loadErr := v1alpha1.LoadUserRegistration(contents)
		if loadErr != nil {
			return nil, fmt.Errorf("load user registration %q: %w", entry.Name(), loadErr)
		}
		registrations = append(registrations, registration)
	}
	return registrations, nil
}

func (store *Store) getUnlocked(namespace, name string) (v1alpha1.UserRegistration, error) {
	path, err := store.resourcePath(namespace, name)
	if err != nil {
		return v1alpha1.UserRegistration{}, err
	}
	contents, err := readStoredFile(path)
	if os.IsNotExist(err) {
		return v1alpha1.UserRegistration{}, fmt.Errorf("%w: %s/%s", ErrNotFound, namespace, name)
	}
	if err != nil {
		return v1alpha1.UserRegistration{}, fmt.Errorf("read user registration %q: %w", path, err)
	}
	registration, err := v1alpha1.LoadUserRegistration(contents)
	if err != nil {
		return v1alpha1.UserRegistration{}, fmt.Errorf("load user registration %q: %w", path, err)
	}
	return registration, nil
}

func (store *Store) writeUnlocked(namespace, name string, contents []byte) error {
	path, err := store.resourcePath(namespace, name)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create registration namespace %q: %w", namespace, err)
	}
	temporary, err := os.CreateTemp(directory, ".user-registration-*")
	if err != nil {
		return fmt.Errorf("create temporary registration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect temporary registration: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary registration: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary registration: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary registration: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("persist user registration %q: %w", path, err)
	}
	return nil
}

func (store *Store) resourcePath(namespace, name string) (string, error) {
	if problems := validation.IsDNS1123Label(namespace); len(problems) != 0 {
		return "", fmt.Errorf("namespace %q is invalid: %s", namespace, strings.Join(problems, "; "))
	}
	if problems := validation.IsDNS1123Subdomain(name); len(problems) != 0 {
		return "", fmt.Errorf("name %q is invalid: %s", name, strings.Join(problems, "; "))
	}
	return filepath.Join(store.root, namespace, name+".yaml"), nil
}

func randomIdentifier() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate registration identifier: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func readStoredFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("registration path %q is not a regular file", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("registration file %q permissions are %o; group/world write access is not allowed", path, info.Mode().Perm())
	}
	return os.ReadFile(path)
}
