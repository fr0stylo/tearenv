package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fr0stylo/tearenv/internal/authn"
	"github.com/fr0stylo/tearenv/internal/registration"
	"golang.org/x/crypto/ssh"
)

func TestDiscoverAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/gateway/.well-known/tearenv-configuration" {
			http.NotFound(response, request)
			return
		}
		_ = json.NewEncoder(response).Encode(authn.Configuration{
			Mode: authn.MethodOIDC, TokenEndpoint: "/oauth/token",
			OIDC: &authn.OIDCConfiguration{
				IssuerURL: "https://id.example.com", ClientID: "tearenv-cli", Audience: "tearenv",
				IdentityClaim: "preferred_username", Scopes: []string{"profile"},
				SubjectTokenType: authn.SubjectTokenTypeIDToken,
			},
		})
	}))
	defer server.Close()

	configuration, err := DiscoverAuthentication(context.Background(), server.Client(), server.URL+"/gateway")
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Mode != authn.MethodOIDC || configuration.TokenEndpoint != "/oauth/token" ||
		configuration.OIDC == nil || configuration.OIDC.Scopes[0] != "openid" ||
		configuration.OIDC.SubjectTokenType != authn.SubjectTokenTypeIDToken {
		t.Fatalf("unexpected configuration: %#v", configuration)
	}
}

func TestExchangeSSHCertificate(t *testing.T) {
	privateSigner := clientTestSigner(t)
	caSigner := clientTestSigner(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if request.Form.Get("grant_type") != registration.TokenExchangeGrantType ||
			request.Form.Get("audience") != "tearenv" || request.Form.Get("key_id") != "workstation" ||
			request.Form.Get("subject_token_type") != authn.SubjectTokenTypeIDToken {
			t.Fatalf("unexpected token exchange form: %v", request.Form)
		}
		certificate := &ssh.Certificate{
			Key: privateSigner.PublicKey(), CertType: ssh.UserCert,
			ValidPrincipals: []string{"alice"},
			ValidAfter:      uint64(time.Now().Add(-time.Minute).Unix()), ValidBefore: uint64(time.Now().Add(time.Minute).Unix()),
		}
		if err := certificate.SignCert(rand.Reader, caSigner); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(response).Encode(map[string]any{
			"access_token":      string(ssh.MarshalAuthorizedKey(certificate)),
			"issued_token_type": registration.SSHCertificateTokenType,
			"token_type":        "N_A",
			"expires_in":        60,
		})
	}))
	defer server.Close()

	signer, err := ExchangeSSHCertificate(
		context.Background(), server.Client(), server.URL,
		authn.Configuration{TokenEndpoint: "/oauth/token", OIDC: &authn.OIDCConfiguration{
			Audience: "tearenv", SubjectTokenType: authn.SubjectTokenTypeIDToken,
		}},
		"id-token", "workstation", "alice", privateSigner,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := signer.PublicKey().(*ssh.Certificate); !ok {
		t.Fatalf("signer public key is %T, want SSH certificate", signer.PublicKey())
	}
}

func clientTestSigner(t *testing.T) ssh.Signer {
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
