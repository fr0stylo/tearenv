package authorization

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestCertificateAuthorityAuthenticatesOIDCCertificate(t *testing.T) {
	ca := testSigner(t)
	user := testSigner(t)
	authenticator, err := NewCertificateAuthority(ca.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(5 * time.Minute).Truncate(time.Second)
	certificate := testCertificate(t, ca, user.PublicKey(), "alice", expires)

	result, authenticated, err := authenticator.Authenticate(context.Background(), Attempt{
		Identity: "alice", Method: MethodPublicKey, PublicKey: certificate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !authenticated {
		t.Fatal("expected certificate to authenticate")
	}
	if result.Identity != "alice" || result.Provider != "oidc-ssh-certificate" || !result.ValidBefore.Equal(expires) {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestCertificateAuthorityRejectsInvalidCertificates(t *testing.T) {
	ca := testSigner(t)
	user := testSigner(t)
	authenticator, err := NewCertificateAuthority(ca.PublicKey())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		identity string
		key      ssh.PublicKey
	}{
		{name: "plain key", identity: "alice", key: user.PublicKey()},
		{name: "wrong principal", identity: "bob", key: testCertificate(t, ca, user.PublicKey(), "alice", time.Now().Add(time.Minute))},
		{name: "expired", identity: "alice", key: testCertificate(t, ca, user.PublicKey(), "alice", time.Now().Add(-time.Minute))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, authenticated, err := authenticator.Authenticate(context.Background(), Attempt{
				Identity: test.identity, Method: MethodPublicKey, PublicKey: test.key,
			})
			if err != nil {
				t.Fatal(err)
			}
			if authenticated {
				t.Fatal("expected authentication rejection")
			}
		})
	}
}

func testSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func testCertificate(t *testing.T, ca ssh.Signer, key ssh.PublicKey, identity string, expires time.Time) *ssh.Certificate {
	t.Helper()
	certificate := &ssh.Certificate{
		Key:             key,
		CertType:        ssh.UserCert,
		ValidPrincipals: []string{identity},
		ValidAfter:      uint64(time.Now().Add(-time.Minute).Unix()),
		ValidBefore:     uint64(expires.Unix()),
		Permissions: ssh.Permissions{Extensions: map[string]string{
			oidcAuthenticationExtension: "oidc",
		}},
	}
	if err := certificate.SignCert(rand.Reader, ca); err != nil {
		t.Fatal(err)
	}
	return certificate
}
