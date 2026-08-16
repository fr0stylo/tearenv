package registration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
	"github.com/fr0stylo/tearenv/internal/authn"
)

func TestHandlerCreatesAndGetsRegistration(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, "")
	registration, _ := testRegistration(t)
	body, err := json.Marshal(registration)
	if err != nil {
		t.Fatal(err)
	}
	path := "/apis/tearenv.io/v1alpha1/namespaces/default/userregistrations/alice"
	request := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201; body: %s", response.Code, response.Body.String())
	}
	stored, err := v1alpha1.LoadUserRegistration(response.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Accepted() {
		t.Fatalf("PUT response is not accepted: %#v", stored.Status)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body: %s", response.Code, response.Body.String())
	}
}

func TestHandlerRejectsPathBodyMismatch(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	registration, _ := testRegistration(t)
	body, err := json.Marshal(registration)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut,
		"/apis/tearenv.io/v1alpha1/namespaces/default/userregistrations/bob", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	NewHandler(store, "").ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("PUT status = %d, want 400; body: %s", response.Code, response.Body.String())
	}
}

func TestHandlerRequiresConfiguredBearerToken(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	registration, _ := testRegistration(t)
	body, err := json.Marshal(registration)
	if err != nil {
		t.Fatal(err)
	}
	path := "/apis/tearenv.io/v1alpha1/namespaces/default/userregistrations/alice"
	token := string(bytes.Repeat([]byte{'x'}, 32))
	incorrectToken := string(bytes.Repeat([]byte{'y'}, 32))
	handler := NewHandler(store, token)

	for _, test := range []struct {
		name       string
		token      string
		wantStatus int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "incorrect", token: incorrectToken, wantStatus: http.StatusUnauthorized},
		{name: "accepted", token: token, wantStatus: http.StatusCreated},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestHandlerHealthDoesNotRequireBearerToken(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	token := string(bytes.Repeat([]byte{'x'}, 32))
	for _, path := range []string{"/healthz", "/readyz"} {
		response := httptest.NewRecorder()
		NewHandler(store, token).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, response.Code)
		}
	}
}

func TestHandlerBindsOIDCIdentityAndProtectsOwnedRegistration(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	principal := authn.Principal{
		Method: authn.MethodOIDC, Issuer: "https://issuer.example.com", Subject: "subject-alice", Identity: "alice",
	}
	authenticator := authn.AuthenticatorFunc(func(_ context.Context, token string) (authn.Principal, error) {
		if token != "valid" {
			return authn.Principal{}, authn.ErrUnauthenticated
		}
		return principal, nil
	})
	handler := NewHandlerWithOptions(store, HandlerOptions{Authenticator: authenticator})
	registration, _ := testRegistration(t)
	body, err := json.Marshal(registration)
	if err != nil {
		t.Fatal(err)
	}
	path := "/apis/tearenv.io/v1alpha1/namespaces/default/userregistrations/alice"
	request := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201; body: %s", response.Code, response.Body.String())
	}

	principal.Subject = "subject-bob"
	request = httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer valid")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("GET status = %d, want 403; body: %s", response.Code, response.Body.String())
	}
}

func TestHandlerPublishesAuthenticationConfiguration(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithOptions(store, HandlerOptions{Configuration: authn.Configuration{
		Mode: authn.MethodOIDC,
		OIDC: &authn.OIDCConfiguration{
			IssuerURL: "https://issuer.example.com", ClientID: "tearenv-cli", Audience: "tearenv-api",
		},
	}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/.well-known/tearenv-configuration", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var configuration authn.Configuration
	if err := json.Unmarshal(response.Body.Bytes(), &configuration); err != nil {
		t.Fatal(err)
	}
	if configuration.Mode != authn.MethodOIDC || configuration.OIDC == nil || len(configuration.OIDC.Scopes) == 0 || configuration.OIDC.Scopes[0] != "openid" {
		t.Fatalf("configuration = %#v", configuration)
	}
}
