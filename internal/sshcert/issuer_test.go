package sshcert

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestIssuerSignsShortLivedUserCertificate(t *testing.T) {
	t.Parallel()

	ca := testSigner(t)
	user := testSigner(t)
	issuer, err := NewIssuer(ca, 5*time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000_000_000, 0)
	issuer.now = func() time.Time { return now }
	certificate, err := issuer.Issue("alice", "laptop", user.PublicKey(), Subject{
		Issuer: "https://issuer.example.com", Subject: "subject-alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	checker := ssh.CertChecker{IsUserAuthority: func(key ssh.PublicKey) bool {
		return bytes.Equal(key.Marshal(), ca.PublicKey().Marshal())
	}, Clock: func() time.Time { return now }}
	if err := checker.CheckCert("alice", certificate); err != nil {
		t.Fatalf("CheckCert(): %v", err)
	}
	if err := checker.CheckCert("bob", certificate); err == nil {
		t.Fatal("CheckCert(bob) succeeded")
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
