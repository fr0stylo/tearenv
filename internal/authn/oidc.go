package authn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// OIDCConfig selects one issuer, subject-token profile, and identity claim.
type OIDCConfig struct {
	IssuerURL        string
	ClientID         string
	Audience         string
	IdentityClaim    string
	SubjectTokenType string
	CAFile           string
}

type verifiedOIDCToken struct {
	Issuer  string
	Subject string
	Claims  map[string]json.RawMessage
}

type oidcTokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (verifiedOIDCToken, error)
}

type remoteOIDCVerifier struct {
	verifier *oidc.IDTokenVerifier
}

func (verifier remoteOIDCVerifier) Verify(ctx context.Context, rawToken string) (verifiedOIDCToken, error) {
	verified, err := verifier.verifier.Verify(ctx, rawToken)
	if err != nil {
		return verifiedOIDCToken{}, err
	}
	claims := make(map[string]json.RawMessage)
	if err := verified.Claims(&claims); err != nil {
		return verifiedOIDCToken{}, fmt.Errorf("decode OIDC claims: %w", err)
	}
	return verifiedOIDCToken{Issuer: verified.Issuer, Subject: verified.Subject, Claims: claims}, nil
}

// OIDC validates audience-bound JWT subject tokens discovered from one issuer.
type OIDC struct {
	issuerURL        string
	identityClaim    string
	subjectTokenType string
	verifier         oidcTokenVerifier
}

// NewOIDC discovers an issuer and creates a long-lived, JWKS-caching verifier.
func NewOIDC(ctx context.Context, config OIDCConfig) (*OIDC, error) {
	issuerURL := strings.TrimRight(strings.TrimSpace(config.IssuerURL), "/")
	parsed, err := url.Parse(issuerURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("OIDC issuer must be an HTTPS URL without query or fragment")
	}
	subjectTokenType, err := NormalizeSubjectTokenType(config.SubjectTokenType)
	if err != nil {
		return nil, err
	}
	verificationAudience := strings.TrimSpace(config.Audience)
	if subjectTokenType == SubjectTokenTypeIDToken {
		verificationAudience = strings.TrimSpace(config.ClientID)
	}
	if verificationAudience == "" {
		return nil, errors.New("OIDC verification audience is required")
	}
	identityClaim := strings.TrimSpace(config.IdentityClaim)
	if identityClaim == "" {
		identityClaim = "preferred_username"
	}
	httpClient, err := newOIDCHTTPClient(config.CAFile)
	if err != nil {
		return nil, err
	}
	oidcContext := oidc.ClientContext(ctx, httpClient)
	provider, err := oidc.NewProvider(oidcContext, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC issuer: %w", err)
	}
	verifier := provider.VerifierContext(oidcContext, &oidc.Config{
		ClientID: verificationAudience,
		SupportedSigningAlgs: []string{
			oidc.RS256, oidc.ES256, oidc.PS256, oidc.EdDSA,
		},
	})
	return newOIDCWithVerifier(issuerURL, identityClaim, subjectTokenType, remoteOIDCVerifier{verifier: verifier}), nil
}

func newOIDCWithVerifier(issuerURL, identityClaim, subjectTokenType string, verifier oidcTokenVerifier) *OIDC {
	return &OIDC{
		issuerURL: issuerURL, identityClaim: identityClaim, subjectTokenType: subjectTokenType, verifier: verifier,
	}
}

func (authenticator *OIDC) Authenticate(ctx context.Context, bearerToken string) (Principal, error) {
	bearerToken = strings.TrimSpace(bearerToken)
	if bearerToken == "" || !matchesSubjectTokenType(bearerToken, authenticator.subjectTokenType) {
		return Principal{}, ErrUnauthenticated
	}
	verified, err := authenticator.verifier.Verify(ctx, bearerToken)
	if err != nil || verified.Issuer != authenticator.issuerURL || strings.TrimSpace(verified.Subject) == "" {
		return Principal{}, ErrUnauthenticated
	}
	rawIdentity, exists := verified.Claims[authenticator.identityClaim]
	if !exists {
		return Principal{}, ErrUnauthenticated
	}
	var identity string
	if err := json.Unmarshal(rawIdentity, &identity); err != nil || strings.TrimSpace(identity) == "" {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{
		Method: MethodOIDC, Issuer: verified.Issuer, Subject: verified.Subject, Identity: strings.TrimSpace(identity),
	}, nil
}

func matchesSubjectTokenType(rawToken, subjectTokenType string) bool {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return false
	}
	encodedHeader := parts[0]
	headerBytes, err := base64.RawURLEncoding.DecodeString(encodedHeader)
	if err != nil {
		return false
	}
	var header struct {
		Type string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return false
	}
	isAccessToken := strings.EqualFold(header.Type, "at+jwt") || strings.EqualFold(header.Type, "application/at+jwt")
	switch subjectTokenType {
	case SubjectTokenTypeAccessToken:
		return isAccessToken
	case SubjectTokenTypeIDToken:
		return !isAccessToken
	default:
		return false
	}
}

func newOIDCHTTPClient(caFile string) (*http.Client, error) {
	caFile = strings.TrimSpace(caFile)
	if caFile == "" {
		return &http.Client{Timeout: 15 * time.Second}, nil
	}
	certificate, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read OIDC CA file %q: %w", caFile, err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system certificate pool: %w", err)
	}
	if !roots.AppendCertsFromPEM(certificate) {
		return nil, fmt.Errorf("OIDC CA file %q contains no certificates", caFile)
	}
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("default HTTP transport does not support TLS configuration")
	}
	transport := baseTransport.Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second}, nil
}
