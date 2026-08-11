package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	want := Profile{
		ServerAddress: "tunnel.example.com:2222",
		Identity:      "alice",
		Token:         "personal-token",
		KnownHosts:    "/home/alice/.ssh/known_hosts",
	}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("profile permissions = %o, want 600", got)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if *got != want {
		t.Fatalf("Load() = %#v, want %#v", *got, want)
	}
}

func TestLoadRejectsOpenPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	contents := []byte(`{"server":"localhost:2222","identity":"alice","token":"token","insecure_skip_host_key_check":true}`)
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted group/world-readable profile")
	}
}

func TestSaveAndLoadPrivateKeyProfile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	want := Profile{
		ServerAddress: "tunnel.example.com:2222",
		Identity:      "alice",
		PrivateKey:    "/home/alice/.config/tearenv/id_ed25519",
		KnownHosts:    "/home/alice/.ssh/known_hosts",
	}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if *got != want {
		t.Fatalf("Load() = %#v, want %#v", *got, want)
	}
}
