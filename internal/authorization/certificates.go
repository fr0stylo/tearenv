package authorization

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
)

const oidcAuthenticationExtension = "tearenv.io/authentication"

// CertificateAuthority authenticates short-lived SSH user certificates signed
// by one trusted CA. The requested SSH username must be a certificate principal.
type CertificateAuthority struct {
	checker ssh.CertChecker
}

// NewCertificateAuthority creates an SSH certificate authenticator.
func NewCertificateAuthority(authority ssh.PublicKey) (*CertificateAuthority, error) {
	if authority == nil {
		return nil, errors.New("SSH user certificate authority is required")
	}
	authorityBytes := authority.Marshal()
	return &CertificateAuthority{checker: ssh.CertChecker{
		IsUserAuthority: func(candidate ssh.PublicKey) bool {
			return candidate != nil && bytes.Equal(candidate.Marshal(), authorityBytes)
		},
	}}, nil
}

// Authenticate validates a user certificate, its principal, lifetime, and the
// Tearenv OIDC provenance extension added by the token exchange endpoint.
func (authority *CertificateAuthority) Authenticate(_ context.Context, attempt Attempt) (Result, bool, error) {
	if attempt.Method != MethodPublicKey || attempt.PublicKey == nil {
		return Result{}, false, nil
	}
	certificate, ok := attempt.PublicKey.(*ssh.Certificate)
	if !ok {
		return Result{}, false, nil
	}
	if certificate.CertType != ssh.UserCert {
		return Result{}, false, nil
	}
	if certificate.Extensions[oidcAuthenticationExtension] != "oidc" {
		return Result{}, false, nil
	}
	if err := authority.checker.CheckCert(attempt.Identity, certificate); err != nil {
		return Result{}, false, nil
	}
	if certificate.ValidBefore > uint64(^uint64(0)>>1) {
		return Result{}, false, fmt.Errorf("SSH certificate expiry is outside supported range")
	}
	return Result{
		Identity:    attempt.Identity,
		Provider:    "oidc-ssh-certificate",
		ValidBefore: time.Unix(int64(certificate.ValidBefore), 0).UTC(),
	}, true, nil
}
