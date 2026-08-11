package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// LoadOrCreateHostKey loads a private host key or creates a persistent
// Ed25519 key with owner-only permissions.
func LoadOrCreateHostKey(path string) (ssh.Signer, error) {
	contents, err := os.ReadFile(path)
	if err == nil {
		signer, parseErr := ssh.ParsePrivateKey(contents)
		if parseErr != nil {
			return nil, fmt.Errorf("parse host key %q: %w", path, parseErr)
		}
		return signer, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read host key %q: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create host key directory: %w", err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate host key: %w", err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("encode host key: %w", err)
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("write host key %q: %w", path, err)
	}
	return ssh.NewSignerFromKey(privateKey)
}
