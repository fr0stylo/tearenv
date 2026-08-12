package registration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
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
