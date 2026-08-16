// Package authn verifies HTTP credentials and returns authenticated principals.
package authn

import (
	"context"
	"errors"
	"strings"
)

const (
	MethodAnonymous = "anonymous"
	MethodToken     = "token"
	MethodOIDC      = "oidc"
)

var ErrUnauthenticated = errors.New("authentication required")

// Principal is the authenticated caller of the registration API.
type Principal struct {
	Method   string
	Issuer   string
	Subject  string
	Identity string
}

// Authenticator verifies one bearer credential.
type Authenticator interface {
	Authenticate(ctx context.Context, bearerToken string) (Principal, error)
}

// AuthenticatorFunc adapts a function to Authenticator.
type AuthenticatorFunc func(context.Context, string) (Principal, error)

func (function AuthenticatorFunc) Authenticate(ctx context.Context, bearerToken string) (Principal, error) {
	return function(ctx, bearerToken)
}

// Anonymous permits loopback-only development deployments without a credential.
type Anonymous struct{}

func (Anonymous) Authenticate(_ context.Context, _ string) (Principal, error) {
	return Principal{Method: MethodAnonymous}, nil
}

// Bearer extracts a case-sensitive RFC 6750 Bearer credential.
func Bearer(authorization string) (string, error) {
	token, found := strings.CutPrefix(strings.TrimSpace(authorization), "Bearer ")
	if !found || strings.TrimSpace(token) == "" {
		return "", ErrUnauthenticated
	}
	return strings.TrimSpace(token), nil
}
