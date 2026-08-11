package kube

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"testing"

	"github.com/fr0stylo/tearenv/internal/authorization"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestUpsertAuthorizedKeySecretCreatesAndPreservesKeys(t *testing.T) {
	t.Parallel()

	secrets := &fakeSecretClient{}
	alice := testPublicKey(t)
	bob := testPublicKey(t)
	if err := UpsertAuthorizedKeySecret(t.Context(), secrets, "tearenv-authorized-keys", "alice", alice); err != nil {
		t.Fatal(err)
	}
	if err := UpsertAuthorizedKeySecret(t.Context(), secrets, "tearenv-authorized-keys", "bob", bob); err != nil {
		t.Fatal(err)
	}

	contents := secrets.secret.Data[authorization.PublicKeysDataKey]
	path := t.TempDir() + "/" + authorization.PublicKeysDataKey
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := authorization.LoadPublicKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	for identity, key := range map[string]ssh.PublicKey{"alice": alice, "bob": bob} {
		_, authenticated, err := provider.Authenticate(t.Context(), authorization.Attempt{
			Identity: identity, Method: authorization.MethodPublicKey, PublicKey: key,
		})
		if err != nil || !authenticated {
			t.Fatalf("Authenticate(%q) = %t, %v", identity, authenticated, err)
		}
	}
}

func TestUpsertAuthorizedKeySecretRetriesConflict(t *testing.T) {
	t.Parallel()

	key := testPublicKey(t)
	contents, err := authorization.UpsertPublicKey(nil, "bob", testPublicKey(t))
	if err != nil {
		t.Fatal(err)
	}
	secrets := &fakeSecretClient{
		secret: &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "tearenv-authorized-keys"},
			Data:       map[string][]byte{authorization.PublicKeysDataKey: contents},
		},
		conflictOnce: true,
	}
	if err := UpsertAuthorizedKeySecret(t.Context(), secrets, "tearenv-authorized-keys", "alice", key); err != nil {
		t.Fatal(err)
	}
	if secrets.updates != 2 {
		t.Fatalf("Update() calls = %d, want 2", secrets.updates)
	}
}

type fakeSecretClient struct {
	secret       *corev1.Secret
	conflictOnce bool
	updates      int
}

func (client *fakeSecretClient) Get(_ context.Context, name string, _ metav1.GetOptions) (*corev1.Secret, error) {
	if client.secret == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
	}
	return client.secret.DeepCopy(), nil
}

func (client *fakeSecretClient) Create(_ context.Context, secret *corev1.Secret, _ metav1.CreateOptions) (*corev1.Secret, error) {
	client.secret = secret.DeepCopy()
	return client.secret.DeepCopy(), nil
}

func (client *fakeSecretClient) Update(_ context.Context, secret *corev1.Secret, _ metav1.UpdateOptions) (*corev1.Secret, error) {
	client.updates++
	if client.conflictOnce {
		client.conflictOnce = false
		return nil, apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, secret.Name, errors.New("conflict"))
	}
	client.secret = secret.DeepCopy()
	return client.secret.DeepCopy(), nil
}

func testPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
