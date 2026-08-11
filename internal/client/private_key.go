package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// LoadPrivateKey loads an owner-only SSH private key.
func LoadPrivateKey(path string) (ssh.Signer, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat private key %q: %w", path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("private key %q permissions are %o; want 600", path, info.Mode().Perm())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key %q: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(contents)
	if err != nil {
		return nil, fmt.Errorf("parse private key %q: %w", path, err)
	}
	return signer, nil
}

// LoadOrCreatePrivateKey loads an existing key or creates an Ed25519 key.
func LoadOrCreatePrivateKey(path, identity string) (ssh.Signer, error) {
	signer, err := LoadPrivateKey(path)
	if err == nil {
		return signer, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create private key directory: %w", err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate private key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "tearenv "+identity)
	if err != nil {
		return nil, fmt.Errorf("encode private key: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tearenv-key-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary private key: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("protect temporary private key: %w", err)
	}
	if err := pem.Encode(temporary, block); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("write private key: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("sync private key: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("close private key: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return nil, fmt.Errorf("replace private key %q: %w", path, err)
	}
	return ssh.NewSignerFromKey(privateKey)
}
