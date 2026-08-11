package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialsAuthenticateIdentity(t *testing.T) {
	credentials, err := NewCredentials(map[string]string{
		"alice": "alice-token-long-enough",
		"bob":   "bob-token-is-long-enough",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !credentials.Authenticate("alice", "alice-token-long-enough") {
		t.Fatal("alice was not authenticated")
	}
	if credentials.Authenticate("bob", "alice-token-long-enough") {
		t.Fatal("alice's token authenticated as bob")
	}
	if credentials.Authenticate("mallory", "alice-token-long-enough") {
		t.Fatal("unknown identity was authenticated")
	}
}

func TestLoadCredentialsRejectsOpenPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	contents := []byte(`{"users":{"alice":{"token":"alice-token-long-enough"}}}`)
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials(path); err == nil {
		t.Fatal("LoadCredentials() accepted group/world-readable credentials")
	}
}

func TestInviteEnrollmentIsSingleUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	invite, err := CreateInvite(path, "alice")
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if !credentials.AuthenticateInvite("alice", invite) {
		t.Fatal("invite was not authenticated")
	}
	token, err := credentials.Enroll("alice", invite)
	if err != nil {
		t.Fatal(err)
	}
	if !credentials.Authenticate("alice", token) {
		t.Fatal("enrolled token was not authenticated")
	}
	if credentials.AuthenticateInvite("alice", invite) {
		t.Fatal("consumed invite was authenticated")
	}
	if _, err := credentials.Enroll("alice", invite); err == nil {
		t.Fatal("invite was consumed twice")
	}

	reloaded, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Authenticate("alice", token) {
		t.Fatal("enrolled token did not survive reload")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), invite) || strings.Contains(string(contents), token) {
		t.Fatal("credentials file contains a plaintext secret")
	}
}

func TestNewInviteReloadsAndRotatesExistingIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	firstInvite, err := CreateInvite(path, "alice")
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	firstToken, err := credentials.Enroll("alice", firstInvite)
	if err != nil {
		t.Fatal(err)
	}

	secondInvite, err := CreateInvite(path, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !credentials.AuthenticateInvite("alice", secondInvite) {
		t.Fatal("running server did not reload newly issued invite")
	}
	secondToken, err := credentials.Enroll("alice", secondInvite)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Authenticate("alice", firstToken) {
		t.Fatal("old token remained valid after login rotation")
	}
	if !credentials.Authenticate("alice", secondToken) {
		t.Fatal("rotated token was not authenticated")
	}
}

func TestServicesAreBoundToIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	if _, err := CreateInvite(path, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateInvite(path, "bob"); err != nil {
		t.Fatal(err)
	}
	if err := GrantService(path, "alice", Service{
		Name: "postgres", Target: "postgres-alice.dev.svc:5432",
	}); err != nil {
		t.Fatal(err)
	}
	if err := GrantService(path, "bob", Service{
		Name: "redis", Target: "redis-bob.dev.svc:6379",
	}); err != nil {
		t.Fatal(err)
	}
	credentials, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	alice, err := credentials.Services("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(alice) != 1 || alice[0].Name != "postgres" || alice[0].LocalPort != 5432 {
		t.Fatalf("Alice services = %#v", alice)
	}
	if _, allowed := credentials.ResolveService("alice", "redis"); allowed {
		t.Fatal("Alice resolved Bob's Redis service")
	}
}

func TestGrantServiceAcceptsBackendSpecificWorkload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	if _, err := CreateInvite(path, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := GrantService(path, "alice", Service{
		Name:   "postgres",
		Target: "127.0.0.1:5432",
		Workload: &Workload{
			Kind: "container", Name: "postgres-alice",
		},
	}); err != nil {
		t.Fatal(err)
	}

	credentials, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	service, ok := credentials.ResolveService("alice", "postgres")
	if !ok {
		t.Fatal("backend-specific service was not persisted")
	}
	if service.Workload.Kind != "container" || service.Workload.Namespace != "" || service.Workload.Replicas != 1 {
		t.Fatalf("workload = %#v", service.Workload)
	}
}
