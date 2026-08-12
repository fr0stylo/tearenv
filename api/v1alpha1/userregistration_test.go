package v1alpha1

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExampleUserRegistration(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile("examples/user-registration.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUserRegistration(contents); err != nil {
		t.Fatalf("load example registration: %v", err)
	}
}

func TestResourceNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value        string
		wantResource string
		wantKey      string
	}{
		{value: "alice", wantResource: "alice", wantKey: "alice"},
		{value: "Alice@Example.COM", wantResource: "alice-example.com-a40447602ae2", wantKey: "alice-example-com-a40447602ae2"},
		{value: "build-host.example.com", wantResource: "build-host.example.com", wantKey: "build-host-example-com"},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			if got := ResourceName(test.value); got != test.wantResource {
				t.Errorf("ResourceName(%q) = %q, want %q", test.value, got, test.wantResource)
			}
			if got := KeyName(test.value); got != test.wantKey {
				t.Errorf("KeyName(%q) = %q, want %q", test.value, got, test.wantKey)
			}
		})
	}
}

func TestLoadUserRegistration(t *testing.T) {
	t.Parallel()

	publicKey := testAuthorizedKey(t)
	contents := fmt.Appendf(nil, `apiVersion: tearenv.io/v1alpha1
kind: UserRegistration
metadata:
  name: alice
  namespace: tearenv-system
spec:
  identity: alice
  publicKeys:
    - name: laptop
      key: %s
`, publicKey)

	registration, err := LoadUserRegistration(contents)
	if err != nil {
		t.Fatal(err)
	}
	if registration.Name != "alice" {
		t.Fatalf("metadata.name = %q, want alice", registration.Name)
	}
	if registration.Spec.Identity != "alice" {
		t.Fatalf("spec.identity = %q, want alice", registration.Spec.Identity)
	}
	if len(registration.Spec.PublicKeys) != 1 || registration.Spec.PublicKeys[0].Name != "laptop" {
		t.Fatalf("spec.publicKeys = %#v, want laptop key", registration.Spec.PublicKeys)
	}
}

func TestLoadUserRegistrationRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	publicKey := testAuthorizedKey(t)
	tests := []struct {
		name     string
		contents string
	}{
		{
			name: "metadata",
			contents: fmt.Sprintf(`apiVersion: tearenv.io/v1alpha1
kind: UserRegistration
metadata:
  name: alice-laptop
  unexpected: true
spec:
  identity: alice
  publicKeys:
    - name: laptop
      key: %s
`, publicKey),
		},
		{
			name: "spec",
			contents: fmt.Sprintf(`apiVersion: tearenv.io/v1alpha1
kind: UserRegistration
metadata:
  name: alice-laptop
spec:
  identity: alice
  publicKeys:
    - name: laptop
      key: %s
  unexpected: true
`, publicKey),
		},
		{
			name: "public key",
			contents: fmt.Sprintf(`apiVersion: tearenv.io/v1alpha1
kind: UserRegistration
metadata:
  name: alice-laptop
spec:
  identity: alice
  publicKeys:
    - name: laptop
      key: %s
      unexpected: true
`, publicKey),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadUserRegistration([]byte(test.contents))
			if err == nil || !strings.Contains(err.Error(), "unexpected") {
				t.Fatalf("LoadUserRegistration() error = %v, want unknown-field error", err)
			}
		})
	}
}

func TestMarshalUserRegistrationRoundTrip(t *testing.T) {
	t.Parallel()

	want := validUserRegistration(t)
	contents, err := MarshalUserRegistration(want)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "status:") {
		t.Fatalf("MarshalUserRegistration() included empty status:\n%s", contents)
	}
	got, err := LoadUserRegistration(contents)
	if err != nil {
		t.Fatal(err)
	}
	if got.APIVersion != want.APIVersion || got.Kind != want.Kind || got.Name != want.Name || got.Spec.Identity != want.Spec.Identity {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestUserRegistrationValidate(t *testing.T) {
	t.Parallel()

	secondKey := testAuthorizedKey(t)
	tests := []struct {
		name   string
		mutate func(*UserRegistration)
		want   string
	}{
		{name: "valid"},
		{name: "api version", mutate: func(value *UserRegistration) { value.APIVersion = "tearenv.io/v2" }, want: "apiVersion"},
		{name: "kind", mutate: func(value *UserRegistration) { value.Kind = "User" }, want: "kind"},
		{name: "resource name", mutate: func(value *UserRegistration) { value.Name = "Alice Laptop" }, want: "metadata.name"},
		{name: "namespace", mutate: func(value *UserRegistration) { value.Namespace = "Tearenv System" }, want: "metadata.namespace"},
		{name: "identity", mutate: func(value *UserRegistration) { value.Spec.Identity = "alice laptop" }, want: "spec.identity"},
		{name: "identity resource mismatch", mutate: func(value *UserRegistration) { value.Name = "bob" }, want: "metadata.name must be"},
		{name: "no public keys", mutate: func(value *UserRegistration) { value.Spec.PublicKeys = nil }, want: "spec.publicKeys"},
		{name: "key name", mutate: func(value *UserRegistration) { value.Spec.PublicKeys[0].Name = "Alice Laptop" }, want: "spec.publicKeys[0].name"},
		{name: "invalid key", mutate: func(value *UserRegistration) { value.Spec.PublicKeys[0].Key = "not-a-key" }, want: "spec.publicKeys[0].key"},
		{
			name: "duplicate key name",
			mutate: func(value *UserRegistration) {
				value.Spec.PublicKeys = append(value.Spec.PublicKeys, SSHPublicKey{Name: "laptop", Key: secondKey})
			},
			want: "duplicate name",
		},
		{
			name: "duplicate key material",
			mutate: func(value *UserRegistration) {
				value.Spec.PublicKeys = append(value.Spec.PublicKeys, SSHPublicKey{Name: "desktop", Key: value.Spec.PublicKeys[0].Key})
			},
			want: "duplicate public key",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registration := validUserRegistration(t)
			if test.mutate != nil {
				test.mutate(&registration)
			}
			err := registration.Validate()
			if test.want == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func validUserRegistration(t *testing.T) UserRegistration {
	t.Helper()
	return UserRegistration{
		TypeMeta: metav1.TypeMeta{APIVersion: APIVersion, Kind: UserRegistrationKind},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "alice",
			Namespace: "tearenv-system",
		},
		Spec: UserRegistrationSpec{
			Identity: "alice",
			PublicKeys: []SSHPublicKey{{
				Name: "laptop",
				Key:  testAuthorizedKey(t),
			}},
		},
	}
}

func testAuthorizedKey(t *testing.T) string {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}
