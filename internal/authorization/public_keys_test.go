package authorization

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestPublicKeysAuthenticateIdentityAndReload(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), PublicKeysDataKey)
	alice := newPublicKey(t)
	bob := newPublicKey(t)
	writePublicKeys(t, path, map[string][]string{
		"alice": {string(ssh.MarshalAuthorizedKey(alice))},
	})
	provider, err := LoadPublicKeys(path)
	if err != nil {
		t.Fatal(err)
	}

	assertPublicKeyAuthentication(t, provider, "alice", alice, true)
	assertPublicKeyAuthentication(t, provider, "bob", alice, false)
	assertPublicKeyAuthentication(t, provider, "alice", bob, false)

	writePublicKeys(t, path, map[string][]string{
		"bob": {string(ssh.MarshalAuthorizedKey(bob))},
	})
	assertPublicKeyAuthentication(t, provider, "alice", alice, false)
	assertPublicKeyAuthentication(t, provider, "bob", bob, true)
}

func TestUpsertPublicKeyPreservesOtherIdentitiesAndDeduplicates(t *testing.T) {
	t.Parallel()

	alice := newPublicKey(t)
	bob := newPublicKey(t)
	document, err := UpsertPublicKey(nil, "alice", alice)
	if err != nil {
		t.Fatal(err)
	}
	document, err = UpsertPublicKey(document, "bob", bob)
	if err != nil {
		t.Fatal(err)
	}
	document, err = UpsertPublicKey(document, "alice", alice)
	if err != nil {
		t.Fatal(err)
	}

	var keys map[string][]string
	if err := json.Unmarshal(document, &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || len(keys["alice"]) != 1 || len(keys["bob"]) != 1 {
		t.Fatalf("keys = %#v, want one key for alice and bob", keys)
	}
}

func TestLoadPublicKeysRejectsWritablePolicy(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), PublicKeysDataKey)
	writePublicKeys(t, path, map[string][]string{"alice": {string(ssh.MarshalAuthorizedKey(newPublicKey(t)))}})
	if err := os.Chmod(path, 0o622); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPublicKeys(path); err == nil {
		t.Fatal("LoadPublicKeys() accepted a group/world-writable policy")
	}
}

func assertPublicKeyAuthentication(t *testing.T, provider *PublicKeys, identity string, key ssh.PublicKey, want bool) {
	t.Helper()
	result, got, err := provider.Authenticate(t.Context(), Attempt{
		Identity:  identity,
		Method:    MethodPublicKey,
		PublicKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Authenticate(%q) = %t, want %t", identity, got, want)
	}
	if got && (result.Identity != identity || result.Provider != "public-key") {
		t.Fatalf("Authenticate(%q) result = %#v", identity, result)
	}
}

func newPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func writePublicKeys(t *testing.T, path string, keys map[string][]string) {
	t.Helper()
	contents, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}
