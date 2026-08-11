// Package profile persists a tearenv client login.
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Profile struct {
	ServerAddress string `json:"server"`
	Identity      string `json:"identity"`
	Token         string `json:"token,omitempty"`
	PrivateKey    string `json:"private_key,omitempty"`
	KnownHosts    string `json:"known_hosts,omitempty"`
	Insecure      bool   `json:"insecure_skip_host_key_check,omitempty"`
}

func Load(path string) (*Profile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat profile %q: %w", path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("profile %q permissions are %o; want 600", path, info.Mode().Perm())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile %q: %w", path, err)
	}
	var profile Profile
	if err := json.Unmarshal(contents, &profile); err != nil {
		return nil, fmt.Errorf("parse profile %q: %w", path, err)
	}
	if err := profile.validate(); err != nil {
		return nil, fmt.Errorf("validate profile %q: %w", path, err)
	}
	return &profile, nil
}

func Save(path string, profile Profile) error {
	if err := profile.validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create profile directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tearenv-profile-*")
	if err != nil {
		return fmt.Errorf("create temporary profile: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect temporary profile: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(profile); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode profile: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync profile: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close profile: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace profile %q: %w", path, err)
	}
	return nil
}

func (profile Profile) validate() error {
	if profile.ServerAddress == "" {
		return errors.New("server address is required")
	}
	if profile.Identity == "" {
		return errors.New("identity is required")
	}
	if profile.Token == "" && profile.PrivateKey == "" {
		return errors.New("token or private key is required")
	}
	if !profile.Insecure && profile.KnownHosts == "" {
		return errors.New("known-hosts path is required")
	}
	return nil
}
