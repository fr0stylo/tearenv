package authorization

import (
	"context"
	"errors"
	"testing"
)

func TestChainUsesFirstSuccessfulProvider(t *testing.T) {
	t.Parallel()

	firstCalled := false
	chain, err := NewChain(
		AuthenticatorFunc(func(_ context.Context, _ Attempt) (Result, bool, error) {
			firstCalled = true
			return Result{}, false, nil
		}),
		AuthenticatorFunc(func(_ context.Context, attempt Attempt) (Result, bool, error) {
			return Result{Identity: attempt.Identity, Provider: "public-key"}, true, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, authenticated, err := chain.Authenticate(context.Background(), Attempt{Identity: "alice", Method: MethodPublicKey})
	if err != nil {
		t.Fatal(err)
	}
	if !firstCalled {
		t.Fatal("first provider was not called")
	}
	if !authenticated || result.Identity != "alice" || result.Provider != "public-key" {
		t.Fatalf("Authenticate() = %#v, %t; want alice authenticated by public-key", result, authenticated)
	}
}

func TestChainStopsOnProviderError(t *testing.T) {
	t.Parallel()

	want := errors.New("provider unavailable")
	chain, err := NewChain(AuthenticatorFunc(func(_ context.Context, _ Attempt) (Result, bool, error) {
		return Result{}, false, want
	}))
	if err != nil {
		t.Fatal(err)
	}

	_, _, got := chain.Authenticate(context.Background(), Attempt{Identity: "alice", Method: MethodPassword})
	if !errors.Is(got, want) {
		t.Fatalf("Authenticate() error = %v, want %v", got, want)
	}
}

func TestNewChainRejectsMissingProviders(t *testing.T) {
	t.Parallel()

	if _, err := NewChain(); err == nil {
		t.Fatal("NewChain() accepted no providers")
	}
}
