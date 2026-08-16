// Package sshcert issues short-lived OpenSSH user certificates.
package sshcert

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const DefaultTTL = 10 * time.Minute

// Subject identifies the OIDC principal recorded in a signed certificate.
type Subject struct {
	Issuer  string
	Subject string
}

// Issuer signs certificates with a protected OpenSSH user CA key.
type Issuer struct {
	signer    ssh.Signer
	ttl       time.Duration
	clockSkew time.Duration
	now       func() time.Time
}

// NewIssuer validates certificate lifetime settings and constructs an issuer.
func NewIssuer(signer ssh.Signer, ttl, clockSkew time.Duration) (*Issuer, error) {
	if signer == nil {
		return nil, errors.New("SSH user CA signer is required")
	}
	if ttl == 0 {
		ttl = DefaultTTL
	}
	if ttl < time.Minute || ttl > time.Hour {
		return nil, errors.New("SSH certificate TTL must be between 1 minute and 1 hour")
	}
	if clockSkew < 0 || clockSkew > 5*time.Minute {
		return nil, errors.New("SSH certificate clock skew must be between 0 and 5 minutes")
	}
	return &Issuer{signer: signer, ttl: ttl, clockSkew: clockSkew, now: time.Now}, nil
}

// LoadSigner loads a private CA key that is not writable by group or world.
func LoadSigner(path string) (ssh.Signer, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat SSH user CA key %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("SSH user CA key %q is not a regular file", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("SSH user CA key %q permissions are %o; group/world write access is not allowed", path, info.Mode().Perm())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read SSH user CA key %q: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(contents)
	if err != nil {
		return nil, fmt.Errorf("parse SSH user CA key %q: %w", path, err)
	}
	return signer, nil
}

// PublicKey returns the CA public key trusted by the SSH server.
func (issuer *Issuer) PublicKey() ssh.PublicKey {
	return issuer.signer.PublicKey()
}

// Lifetime reports the configured certificate validity duration.
func (issuer *Issuer) Lifetime() time.Duration {
	return issuer.ttl
}

// Issue signs a registered public key for one identity.
func (issuer *Issuer) Issue(identity, keyName string, publicKey ssh.PublicKey, subject Subject) (*ssh.Certificate, error) {
	if strings.TrimSpace(identity) == "" || strings.TrimSpace(keyName) == "" || publicKey == nil {
		return nil, errors.New("identity, key name, and public key are required")
	}
	serialBytes := make([]byte, 8)
	if _, err := rand.Read(serialBytes); err != nil {
		return nil, fmt.Errorf("generate SSH certificate serial: %w", err)
	}
	now := issuer.now().UTC()
	certificate := &ssh.Certificate{
		Key:             publicKey,
		Serial:          binary.BigEndian.Uint64(serialBytes),
		CertType:        ssh.UserCert,
		KeyId:           certificateKeyID(subject, keyName),
		ValidPrincipals: []string{identity},
		ValidAfter:      uint64(now.Add(-issuer.clockSkew).Unix()),
		ValidBefore:     uint64(now.Add(issuer.ttl).Unix()),
		Permissions: ssh.Permissions{Extensions: map[string]string{
			"permit-port-forwarding":    "",
			"tearenv.io/authentication": "oidc",
		}},
	}
	if err := certificate.SignCert(rand.Reader, issuer.signer); err != nil {
		return nil, fmt.Errorf("sign SSH user certificate: %w", err)
	}
	return certificate, nil
}

func certificateKeyID(subject Subject, keyName string) string {
	digest := sha256.Sum256([]byte(subject.Issuer + "\x00" + subject.Subject + "\x00" + keyName))
	return "oidc:" + hex.EncodeToString(digest[:12])
}
