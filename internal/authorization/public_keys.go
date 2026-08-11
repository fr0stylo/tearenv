package authorization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"
)

// PublicKeysDataKey is the filename and Kubernetes Secret data key used for
// the public-key authorization document.
const PublicKeysDataKey = "authorized_keys.json"

// PublicKeys authenticates SSH public keys from a reloadable JSON document.
// Reloading each attempt allows projected Kubernetes Secret updates to take
// effect without restarting the gateway.
type PublicKeys struct {
	path string
}

// LoadPublicKeys validates a public-key authorization document.
func LoadPublicKeys(path string) (*PublicKeys, error) {
	if path == "" {
		return nil, errors.New("public keys path is required")
	}
	if _, err := readPublicKeys(path); err != nil {
		return nil, err
	}
	return &PublicKeys{path: path}, nil
}

// Authenticate verifies an identity-bound SSH public key.
func (keys *PublicKeys) Authenticate(_ context.Context, attempt Attempt) (Result, bool, error) {
	if attempt.Method != MethodPublicKey || attempt.PublicKey == nil {
		return Result{}, false, nil
	}
	document, err := readPublicKeys(keys.path)
	if err != nil {
		return Result{}, false, err
	}
	for _, candidate := range document[attempt.Identity] {
		if bytes.Equal(candidate.Marshal(), attempt.PublicKey.Marshal()) {
			return Result{Identity: attempt.Identity, Provider: "public-key"}, true, nil
		}
	}
	return Result{}, false, nil
}

// UpsertPublicKey adds an identity-bound public key to a JSON document.
func UpsertPublicKey(contents []byte, identity string, key ssh.PublicKey) ([]byte, error) {
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	if key == nil {
		return nil, errors.New("public key is required")
	}
	document := make(map[string][]string)
	if len(bytes.TrimSpace(contents)) != 0 {
		if err := json.Unmarshal(contents, &document); err != nil {
			return nil, fmt.Errorf("parse public keys: %w", err)
		}
	}
	encoded := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	for _, existing := range document[identity] {
		candidate, err := parsePublicKey(identity, existing)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(candidate.Marshal(), key.Marshal()) {
			return marshalPublicKeys(document)
		}
	}
	document[identity] = append(document[identity], encoded)
	return marshalPublicKeys(document)
}

func readPublicKeys(path string) (map[string][]ssh.PublicKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat public keys file %q: %w", path, err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("public keys file %q permissions are %o; group/world write access is not allowed", path, info.Mode().Perm())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read public keys file %q: %w", path, err)
	}
	var encoded map[string][]string
	if err := json.Unmarshal(contents, &encoded); err != nil {
		return nil, fmt.Errorf("parse public keys file %q: %w", path, err)
	}
	if len(encoded) == 0 {
		return nil, errors.New("public keys file must contain at least one identity")
	}
	result := make(map[string][]ssh.PublicKey, len(encoded))
	for identity, values := range encoded {
		if err := validateIdentity(identity); err != nil {
			return nil, err
		}
		if len(values) == 0 {
			return nil, fmt.Errorf("public keys for identity %q must not be empty", identity)
		}
		for _, value := range values {
			key, err := parsePublicKey(identity, value)
			if err != nil {
				return nil, fmt.Errorf("public keys file %q: %w", path, err)
			}
			result[identity] = append(result[identity], key)
		}
	}
	return result, nil
}

func parsePublicKey(identity, value string) (ssh.PublicKey, error) {
	key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(value))
	if err != nil {
		return nil, fmt.Errorf("parse public key for identity %q: %w", identity, err)
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("public key for identity %q contains multiple keys", identity)
	}
	return key, nil
}

func marshalPublicKeys(document map[string][]string) ([]byte, error) {
	identities := make([]string, 0, len(document))
	for identity := range document {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	ordered := make(map[string][]string, len(document))
	for _, identity := range identities {
		ordered[identity] = document[identity]
	}
	contents, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode public keys: %w", err)
	}
	return append(contents, '\n'), nil
}
