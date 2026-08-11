package server

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestLoadOrCreateHostKeyPersistsKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "host_key")
	first, err := LoadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}

	if ssh.FingerprintSHA256(first.PublicKey()) != ssh.FingerprintSHA256(second.PublicKey()) {
		t.Fatal("host key changed after reload")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("host key permissions = %o, want 600", got)
	}
}
