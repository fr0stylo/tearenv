package authn

import (
	"errors"
	"strings"
	"testing"
)

func TestStaticTokenAuthenticatesExactValue(t *testing.T) {
	t.Parallel()

	token := strings.Repeat("x", 32)
	authenticator, err := NewStaticToken(token)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := authenticator.Authenticate(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Method != MethodToken {
		t.Fatalf("method = %q, want %q", principal.Method, MethodToken)
	}
	if _, err := authenticator.Authenticate(t.Context(), strings.Repeat("y", 32)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("incorrect token error = %v, want ErrUnauthenticated", err)
	}
}

func TestBearerRequiresBearerSchemeAndValue(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "Basic value", "Bearer"} {
		if _, err := Bearer(value); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("Bearer(%q) error = %v, want ErrUnauthenticated", value, err)
		}
	}
	if token, err := Bearer("Bearer value"); err != nil || token != "value" {
		t.Fatalf("Bearer() = (%q, %v), want value", token, err)
	}
}
