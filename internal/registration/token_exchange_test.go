package registration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fr0stylo/tearenv/internal/authn"
	"github.com/fr0stylo/tearenv/internal/sshcert"
	"golang.org/x/crypto/ssh"
)

func TestTokenExchangeIssuesCertificateForOwnedRegisteredKey(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	registration, registeredKey := testRegistration(t)
	principal := authn.Principal{
		Method: authn.MethodOIDC, Issuer: "https://issuer.example.com", Subject: "subject-alice", Identity: "alice",
	}
	if _, _, err := store.PutAs(registration, principal); err != nil {
		t.Fatal(err)
	}
	ca := registrationTestSigner(t)
	issuer, err := sshcert.NewIssuer(ca, 5*time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewTokenExchangeHandler(TokenExchangeOptions{
		Authenticator: authn.AuthenticatorFunc(func(_ context.Context, token string) (authn.Principal, error) {
			if token != "valid-token" {
				return authn.Principal{}, authn.ErrUnauthenticated
			}
			return principal, nil
		}),
		Store: store, Issuer: issuer, Namespace: "default", Audience: "tearenv-ssh",
		SubjectTokenType: authn.SubjectTokenTypeIDToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(validExchangeForm().Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", response.Code, response.Body.String())
	}
	var exchanged tokenExchangeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &exchanged); err != nil {
		t.Fatal(err)
	}
	certificate, err := parseSSHCertificate(exchanged.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(certificate.Key.Marshal(), registeredKey.Marshal()) {
		t.Fatal("certificate key does not match the registered key")
	}
	checker := ssh.CertChecker{IsUserAuthority: func(key ssh.PublicKey) bool {
		return bytes.Equal(key.Marshal(), ca.PublicKey().Marshal())
	}}
	if err := checker.CheckCert("alice", certificate); err != nil {
		t.Fatalf("CheckCert(): %v", err)
	}
	if exchanged.IssuedTokenType != SSHCertificateTokenType || exchanged.TokenType != "N_A" || exchanged.ExpiresIn != 300 {
		t.Fatalf("exchange response = %#v", exchanged)
	}
}

func TestTokenExchangeRejectsInvalidGrantAndTarget(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := sshcert.NewIssuer(registrationTestSigner(t), 5*time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewTokenExchangeHandler(TokenExchangeOptions{
		Authenticator: authn.AuthenticatorFunc(func(context.Context, string) (authn.Principal, error) {
			return authn.Principal{}, authn.ErrUnauthenticated
		}),
		Store: store, Issuer: issuer, Namespace: "default", Audience: "tearenv-ssh",
		SubjectTokenType: authn.SubjectTokenTypeIDToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(url.Values){
		func(values url.Values) { values.Set("grant_type", "unsupported") },
		func(values url.Values) { values.Set("subject_token_type", authn.SubjectTokenTypeAccessToken) },
		func(values url.Values) { values.Set("audience", "another-service") },
		func(url.Values) {},
	} {
		values := validExchangeForm()
		mutate(values)
		request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(values.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400; body: %s", response.Code, response.Body.String())
		}
	}
}

func validExchangeForm() url.Values {
	return url.Values{
		"grant_type":           {TokenExchangeGrantType},
		"subject_token":        {"valid-token"},
		"subject_token_type":   {authn.SubjectTokenTypeIDToken},
		"requested_token_type": {SSHCertificateTokenType},
		"audience":             {"tearenv-ssh"},
		"scope":                {SSHConnectScope},
		"key_id":               {"laptop"},
	}
}

func registrationTestSigner(t *testing.T) ssh.Signer {
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
