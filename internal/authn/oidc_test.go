package authn

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type oidcVerifierFunc func(context.Context, string) (verifiedOIDCToken, error)

func (function oidcVerifierFunc) Verify(ctx context.Context, token string) (verifiedOIDCToken, error) {
	return function(ctx, token)
}

func TestOIDCMapsVerifiedSubjectAndIdentity(t *testing.T) {
	t.Parallel()

	authenticator := newOIDCWithVerifier("https://issuer.example.com", "preferred_username", SubjectTokenTypeAccessToken, oidcVerifierFunc(
		func(context.Context, string) (verifiedOIDCToken, error) {
			identity, _ := json.Marshal("alice")
			return verifiedOIDCToken{
				Issuer: "https://issuer.example.com", Subject: "subject-alice",
				Claims: map[string]json.RawMessage{"preferred_username": identity},
			}, nil
		},
	))
	principal, err := authenticator.Authenticate(t.Context(), accessTokenFixture("at+jwt"))
	if err != nil {
		t.Fatal(err)
	}
	if principal.Method != MethodOIDC || principal.Identity != "alice" || principal.Subject != "subject-alice" {
		t.Fatalf("principal = %#v", principal)
	}
}

func TestOIDCEnforcesConfiguredSubjectTokenType(t *testing.T) {
	t.Parallel()

	identity, _ := json.Marshal("alice")
	verifier := oidcVerifierFunc(func(context.Context, string) (verifiedOIDCToken, error) {
		return verifiedOIDCToken{
			Issuer: "https://issuer.example.com", Subject: "subject-alice",
			Claims: map[string]json.RawMessage{"preferred_username": identity},
		}, nil
	})
	tests := []struct {
		name        string
		subjectType string
		token       string
		wantError   bool
	}{
		{name: "access token accepts at+jwt", subjectType: SubjectTokenTypeAccessToken, token: accessTokenFixture("at+jwt")},
		{name: "access token rejects generic JWT", subjectType: SubjectTokenTypeAccessToken, token: accessTokenFixture("JWT"), wantError: true},
		{name: "ID token accepts generic JWT", subjectType: SubjectTokenTypeIDToken, token: accessTokenFixture("JWT")},
		{name: "ID token accepts omitted type", subjectType: SubjectTokenTypeIDToken, token: accessTokenFixture("")},
		{name: "ID token rejects at+jwt", subjectType: SubjectTokenTypeIDToken, token: accessTokenFixture("at+jwt"), wantError: true},
		{name: "ID token rejects malformed JWT", subjectType: SubjectTokenTypeIDToken, token: "not-a-jwt", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authenticator := newOIDCWithVerifier(
				"https://issuer.example.com", "preferred_username", test.subjectType, verifier,
			)
			_, err := authenticator.Authenticate(t.Context(), test.token)
			if test.wantError && !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("Authenticate() error = %v, want ErrUnauthenticated", err)
			}
			if !test.wantError && err != nil {
				t.Fatalf("Authenticate() error = %v", err)
			}
		})
	}
}

func TestOIDCRejectsMissingClaims(t *testing.T) {
	t.Parallel()

	verifier := oidcVerifierFunc(func(context.Context, string) (verifiedOIDCToken, error) {
		return verifiedOIDCToken{Issuer: "https://issuer.example.com", Subject: "subject-alice"}, nil
	})
	authenticator := newOIDCWithVerifier(
		"https://issuer.example.com", "preferred_username", SubjectTokenTypeIDToken, verifier,
	)
	if _, err := authenticator.Authenticate(t.Context(), accessTokenFixture("JWT")); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("Authenticate() error = %v, want ErrUnauthenticated", err)
	}
}

func TestOIDCHTTPClientTrustsConfiguredCA(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	caPath := filepath.Join(t.TempDir(), "issuer-ca.pem")
	if err := os.WriteFile(caPath, certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := newOIDCHTTPClient(caPath)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("GET with configured CA: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.StatusCode)
	}
}

func TestOIDCHTTPClientRejectsInvalidCA(t *testing.T) {
	t.Parallel()

	caPath := filepath.Join(t.TempDir(), "issuer-ca.pem")
	if err := os.WriteFile(caPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newOIDCHTTPClient(caPath); err == nil {
		t.Fatal("newOIDCHTTPClient() accepted invalid PEM")
	}
}

func TestOIDCDiscoversIssuerAndVerifiesIDToken(t *testing.T) {
	t.Parallel()

	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"issuer":                                server.URL,
				"authorization_endpoint":                server.URL + "/authorize",
				"token_endpoint":                        server.URL + "/token",
				"jwks_uri":                              server.URL + "/keys",
				"response_types_supported":              []string{"code"},
				"subject_types_supported":               []string{"public"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/keys":
			_ = json.NewEncoder(response).Encode(map[string]any{"keys": []map[string]string{{
				"kty": "RSA", "kid": "test-key", "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(signingKey.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
			}}})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	caPath := filepath.Join(t.TempDir(), "issuer-ca.pem")
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caPath, certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	authenticator, err := NewOIDC(t.Context(), OIDCConfig{
		IssuerURL: server.URL, ClientID: "tearenv-cli", Audience: "tearenv",
		IdentityClaim: "preferred_username", SubjectTokenType: SubjectTokenTypeIDToken, CAFile: caPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	token := signedIDToken(t, signingKey, map[string]any{
		"iss": server.URL, "sub": "subject-alice", "aud": "tearenv-cli",
		"exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Add(-time.Second).Unix(),
		"preferred_username": "alice",
	})
	principal, err := authenticator.Authenticate(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Identity != "alice" || principal.Subject != "subject-alice" || principal.Issuer != server.URL {
		t.Fatalf("principal = %#v", principal)
	}

	wrongAudience := signedIDToken(t, signingKey, map[string]any{
		"iss": server.URL, "sub": "subject-alice", "aud": "another-client",
		"exp": time.Now().Add(time.Minute).Unix(), "preferred_username": "alice",
	})
	if _, err := authenticator.Authenticate(t.Context(), wrongAudience); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("wrong audience error = %v, want ErrUnauthenticated", err)
	}

	accessAuthenticator, err := NewOIDC(t.Context(), OIDCConfig{
		IssuerURL: server.URL, ClientID: "tearenv-cli", Audience: "tearenv",
		IdentityClaim: "preferred_username", SubjectTokenType: SubjectTokenTypeAccessToken, CAFile: caPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	accessToken := signedToken(t, signingKey, "at+jwt", map[string]any{
		"iss": server.URL, "sub": "subject-alice", "aud": "tearenv",
		"exp": time.Now().Add(time.Minute).Unix(), "preferred_username": "alice",
	})
	if _, err := accessAuthenticator.Authenticate(t.Context(), accessToken); err != nil {
		t.Fatalf("Authenticate RFC 9068 access token: %v", err)
	}
}

func signedIDToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	return signedToken(t, key, "JWT", claims)
}

func signedToken(t *testing.T, key *rsa.PrivateKey, tokenType string, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "kid": "test-key", "typ": tokenType})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func accessTokenFixture(tokenType string) string {
	headerValues := map[string]string{"alg": "RS256"}
	if tokenType != "" {
		headerValues["typ"] = tokenType
	}
	header, _ := json.Marshal(headerValues)
	return base64.RawURLEncoding.EncodeToString(header) + ".payload.signature"
}
