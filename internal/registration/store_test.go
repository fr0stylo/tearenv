package registration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
	"github.com/fr0stylo/tearenv/internal/authn"
	"github.com/fr0stylo/tearenv/internal/authorization"
	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestStorePersistsAcceptedRegistration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewStore(root, "default")
	if err != nil {
		t.Fatal(err)
	}
	want, publicKey := testRegistration(t)
	stored, created, err := store.Put(want)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("Put() created = false, want true")
	}
	if !stored.Accepted() || stored.Generation != 1 || stored.ResourceVersion == "" || stored.UID == "" || stored.CreationTimestamp.IsZero() {
		t.Fatalf("stored metadata/status was not initialized: %#v", stored)
	}

	reloaded, err := NewStore(root, "default")
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Get("default", want.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResourceVersion != stored.ResourceVersion || !got.Accepted() {
		t.Fatalf("reloaded registration = %#v, want persisted %#v", got, stored)
	}
	result, authenticated, err := reloaded.Authenticate(t.Context(), authorization.Attempt{
		Identity: "alice", Method: authorization.MethodPublicKey, PublicKey: publicKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !authenticated || result.Identity != "alice" || result.Provider != "user-registration" {
		t.Fatalf("Authenticate() = (%#v, %t), want accepted alice", result, authenticated)
	}
}

func TestStorePutIsIdempotentAndImmutable(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	registration, _ := testRegistration(t)
	first, _, err := store.Put(registration)
	if err != nil {
		t.Fatal(err)
	}
	second, created, err := store.Put(registration)
	if err != nil {
		t.Fatal(err)
	}
	if created || second.ResourceVersion != first.ResourceVersion {
		t.Fatalf("idempotent Put() = (%t, %q), want (false, %q)", created, second.ResourceVersion, first.ResourceVersion)
	}

	changed, _ := testRegistration(t)
	registration.Spec.PublicKeys = changed.Spec.PublicKeys
	_, _, err = store.Put(registration)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("changed Put() error = %v, want ErrConflict", err)
	}
}

func TestStoreBindsOIDCPrincipalAndRejectsAnotherSubject(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	registration, publicKey := testRegistration(t)
	owner := authn.Principal{
		Method: authn.MethodOIDC, Issuer: "https://issuer.example.com", Subject: "subject-alice", Identity: "alice",
	}
	stored, created, err := store.PutAs(registration, owner)
	if err != nil {
		t.Fatal(err)
	}
	if !created || stored.Status == nil || stored.Status.AuthenticatedPrincipal == nil {
		t.Fatalf("PutAs() did not persist principal: %#v", stored.Status)
	}
	if _, err := store.PublicKey("default", "alice", "laptop", owner); err != nil {
		t.Fatalf("PublicKey() for owner: %v", err)
	}
	if got, err := store.PublicKey("default", "alice", "laptop", owner); err != nil || !bytes.Equal(got.Marshal(), publicKey.Marshal()) {
		t.Fatalf("PublicKey() = (%v, %v), want registered key", got, err)
	}

	other := owner
	other.Subject = "subject-bob"
	if _, err := store.GetAs("default", "alice", other); !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetAs() error = %v, want ErrForbidden", err)
	}
	if _, _, err := store.PutAs(registration, other); !errors.Is(err, ErrForbidden) {
		t.Fatalf("PutAs() error = %v, want ErrForbidden", err)
	}
}

func TestStoreAdoptsMatchingUnownedRegistration(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	registration, _ := testRegistration(t)
	if _, _, err := store.Put(registration); err != nil {
		t.Fatal(err)
	}
	owner := authn.Principal{
		Method: authn.MethodOIDC, Issuer: "https://issuer.example.com", Subject: "subject-alice", Identity: "alice",
	}
	adopted, created, err := store.PutAs(registration, owner)
	if err != nil {
		t.Fatal(err)
	}
	if created || adopted.Status == nil || adopted.Status.AuthenticatedPrincipal == nil {
		t.Fatalf("PutAs() = (created %t, status %#v), want adopted owner", created, adopted.Status)
	}
}

func TestStoreRejectsOIDCIdentityMismatch(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	registration, _ := testRegistration(t)
	principal := authn.Principal{
		Method: authn.MethodOIDC, Issuer: "https://issuer.example.com", Subject: "subject-bob", Identity: "bob",
	}
	if _, _, err := store.PutAs(registration, principal); !errors.Is(err, ErrForbidden) {
		t.Fatalf("PutAs() error = %v, want ErrForbidden", err)
	}
}

func TestStoreRejectsOtherKeysAndAuthenticationMethods(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	registration, _ := testRegistration(t)
	if _, _, err := store.Put(registration); err != nil {
		t.Fatal(err)
	}
	_, otherKey := testRegistration(t)
	for _, attempt := range []authorization.Attempt{
		{Identity: "alice", Method: authorization.MethodPublicKey, PublicKey: otherKey},
		{Identity: "alice", Method: authorization.MethodPassword, Password: "not-supported"},
		{Identity: "bob", Method: authorization.MethodPublicKey, PublicKey: otherKey},
	} {
		_, authenticated, err := store.Authenticate(context.Background(), attempt)
		if err != nil {
			t.Fatal(err)
		}
		if authenticated {
			t.Fatalf("Authenticate(%#v) = true, want false", attempt)
		}
	}
}

func TestStoreRejectsWritableRegistrationFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewStore(root, "default")
	if err != nil {
		t.Fatal(err)
	}
	registration, _ := testRegistration(t)
	if _, _, err := store.Put(registration); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "default", "alice.yaml")
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("default", "alice"); err == nil || !strings.Contains(err.Error(), "group/world write") {
		t.Fatalf("Get() error = %v, want writable-file rejection", err)
	}
}

func testRegistration(t *testing.T) (v1alpha1.UserRegistration, ssh.PublicKey) {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return v1alpha1.UserRegistration{
		TypeMeta:   metav1.TypeMeta{APIVersion: v1alpha1.APIVersion, Kind: v1alpha1.UserRegistrationKind},
		ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "default"},
		Spec: v1alpha1.UserRegistrationSpec{
			Identity: "alice",
			PublicKeys: []v1alpha1.SSHPublicKey{{
				Name: "laptop",
				Key:  strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey))),
			}},
		},
	}, publicKey
}
