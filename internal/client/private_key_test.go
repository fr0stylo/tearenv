package client

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestLoadOrCreatePrivateKeyPersistsStableProtectedKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "id_ed25519")
	first, err := LoadOrCreatePrivateKey(path, "alice")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("private key permissions = %o, want 600", got)
	}
	second, err := LoadOrCreatePrivateKey(path, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if ssh.FingerprintSHA256(first.PublicKey()) != ssh.FingerprintSHA256(second.PublicKey()) {
		t.Fatal("LoadOrCreatePrivateKey() replaced the existing private key")
	}
}

func TestLoadPrivateKeyRejectsOpenPermissions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "id_ed25519")
	if _, err := LoadOrCreatePrivateKey(path, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrivateKey(path); err == nil {
		t.Fatal("LoadPrivateKey() accepted group/world-readable key")
	}
}
