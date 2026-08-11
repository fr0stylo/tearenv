// Package authorization provides authentication providers and identity-bound
// access policy for the tearenv gateway.
package authorization

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// Method identifies the credential presented during SSH authentication.
type Method string

const (
	MethodPassword  Method = "password"
	MethodPublicKey Method = "public-key"
)

// Attempt contains one authentication attempt from the SSH transport.
type Attempt struct {
	Identity  string
	Method    Method
	Password  string
	PublicKey ssh.PublicKey
}

// Result describes the verified identity and provider.
type Result struct {
	Identity string
	Provider string
}

// Authenticator verifies an authentication attempt. Providers return false
// without an error when the attempt is valid but does not match their data.
type Authenticator interface {
	Authenticate(ctx context.Context, attempt Attempt) (Result, bool, error)
}

// Enrollment verifies and consumes one-time enrollment invites.
type Enrollment interface {
	AuthenticateInvite(identity, invite string) bool
	Enroll(identity, invite string) (string, error)
}

// Policy resolves the services granted to an authenticated identity.
type Policy interface {
	Services(identity string) ([]Service, error)
	ResolveService(identity, name string) (Service, bool)
}

// AuthenticatorFunc adapts a function to Authenticator.
type AuthenticatorFunc func(ctx context.Context, attempt Attempt) (Result, bool, error)

func (function AuthenticatorFunc) Authenticate(ctx context.Context, attempt Attempt) (Result, bool, error) {
	return function(ctx, attempt)
}

// Chain accepts an attempt when the first matching provider accepts it.
type Chain struct {
	providers []Authenticator
}

// NewChain creates an authentication chain in evaluation order.
func NewChain(providers ...Authenticator) (*Chain, error) {
	if len(providers) == 0 {
		return nil, errors.New("at least one authentication provider is required")
	}
	for index, provider := range providers {
		if provider == nil {
			return nil, fmt.Errorf("authentication provider %d is nil", index)
		}
	}
	return &Chain{providers: append([]Authenticator(nil), providers...)}, nil
}

// Authenticate evaluates providers in their configured order.
func (chain *Chain) Authenticate(ctx context.Context, attempt Attempt) (Result, bool, error) {
	for _, provider := range chain.providers {
		result, authenticated, err := provider.Authenticate(ctx, attempt)
		if err != nil {
			return Result{}, false, err
		}
		if authenticated {
			return result, true, nil
		}
	}
	return Result{}, false, nil
}
