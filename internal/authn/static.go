package authn

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
)

// StaticToken verifies the shared registration token used by token mode.
type StaticToken struct {
	want [sha256.Size]byte
}

// NewStaticToken configures shared-token authentication.
func NewStaticToken(token string) (*StaticToken, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("static registration token is required")
	}
	return &StaticToken{want: sha256.Sum256([]byte(token))}, nil
}

func (authenticator *StaticToken) Authenticate(_ context.Context, bearerToken string) (Principal, error) {
	provided := sha256.Sum256([]byte(strings.TrimSpace(bearerToken)))
	if subtle.ConstantTimeCompare(authenticator.want[:], provided[:]) != 1 {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{Method: MethodToken}, nil
}
