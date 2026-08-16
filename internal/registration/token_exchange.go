package registration

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"slices"
	"strings"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
	"github.com/fr0stylo/tearenv/internal/authn"
	"github.com/fr0stylo/tearenv/internal/sshcert"
	"golang.org/x/crypto/ssh"
)

const (
	TokenExchangeGrantType       = "urn:ietf:params:oauth:grant-type:token-exchange"
	AccessTokenType              = authn.SubjectTokenTypeAccessToken
	IDTokenType                  = authn.SubjectTokenTypeIDToken
	SSHCertificateTokenType      = "https://tearenv.io/oauth/token-type/ssh-certificate"
	SSHConnectScope              = "ssh:connect"
	maximumTokenExchangeBodySize = 64 << 10
)

// TokenExchangeOptions configures the RFC 8693 SSH certificate profile.
type TokenExchangeOptions struct {
	Authenticator    authn.Authenticator
	Store            *Store
	Issuer           *sshcert.Issuer
	Namespace        string
	Audience         string
	SubjectTokenType string
}

type tokenExchangeResponse struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int64  `json:"expires_in"`
}

type oauthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// NewTokenExchangeHandler exchanges an OIDC subject token for an SSH certificate.
func NewTokenExchangeHandler(options TokenExchangeOptions) (http.Handler, error) {
	if options.Authenticator == nil || options.Store == nil || options.Issuer == nil {
		return nil, errors.New("token exchange authenticator, store, and SSH certificate issuer are required")
	}
	if strings.TrimSpace(options.Namespace) == "" || strings.TrimSpace(options.Audience) == "" {
		return nil, errors.New("token exchange namespace and audience are required")
	}
	subjectTokenType, err := authn.NormalizeSubjectTokenType(options.SubjectTokenType)
	if err != nil {
		return nil, err
	}
	options.SubjectTokenType = subjectTokenType
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		handleTokenExchange(response, request, options)
	}), nil
}

func handleTokenExchange(response http.ResponseWriter, request *http.Request, options TokenExchangeOptions) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		writeOAuthError(response, http.StatusUnsupportedMediaType, "invalid_request", "Content-Type must be application/x-www-form-urlencoded")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumTokenExchangeBodySize)
	if err := request.ParseForm(); err != nil {
		writeOAuthError(response, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	if request.PostForm.Get("grant_type") != TokenExchangeGrantType {
		writeOAuthError(response, http.StatusBadRequest, "unsupported_grant_type", "grant_type must select OAuth token exchange")
		return
	}
	if request.PostForm.Get("subject_token_type") != options.SubjectTokenType || strings.TrimSpace(request.PostForm.Get("subject_token")) == "" {
		writeOAuthError(response, http.StatusBadRequest, "invalid_request", "the configured OIDC subject_token is required")
		return
	}
	if request.PostForm.Get("requested_token_type") != SSHCertificateTokenType {
		writeOAuthError(response, http.StatusBadRequest, "invalid_request", "requested_token_type must select an SSH certificate")
		return
	}
	if request.PostForm.Get("audience") != options.Audience {
		writeOAuthError(response, http.StatusBadRequest, "invalid_target", "audience is not accepted")
		return
	}
	if !slices.Contains(strings.Fields(request.PostForm.Get("scope")), SSHConnectScope) {
		writeOAuthError(response, http.StatusBadRequest, "invalid_scope", "ssh:connect scope is required")
		return
	}
	keyName := strings.TrimSpace(request.PostForm.Get("key_id"))
	if keyName == "" {
		writeOAuthError(response, http.StatusBadRequest, "invalid_request", "key_id is required")
		return
	}
	principal, err := options.Authenticator.Authenticate(request.Context(), request.PostForm.Get("subject_token"))
	if err != nil || principal.Method != authn.MethodOIDC {
		writeOAuthError(response, http.StatusBadRequest, "invalid_grant", "subject_token is invalid")
		return
	}
	registrationName := v1alpha1.ResourceName(principal.Identity)
	publicKey, err := options.Store.PublicKey(options.Namespace, registrationName, keyName, principal)
	if errors.Is(err, ErrForbidden) {
		writeOAuthError(response, http.StatusBadRequest, "invalid_target", "registration is not owned by the subject")
		return
	}
	if err != nil {
		writeOAuthError(response, http.StatusBadRequest, "invalid_request", "registered key was not found")
		return
	}
	certificate, err := options.Issuer.Issue(principal.Identity, keyName, publicKey, sshcert.Subject{
		Issuer: principal.Issuer, Subject: principal.Subject,
	})
	if err != nil {
		writeOAuthError(response, http.StatusInternalServerError, "server_error", "SSH certificate issuance failed")
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Pragma", "no-cache")
	writeJSON(response, http.StatusOK, tokenExchangeResponse{
		AccessToken:     strings.TrimSpace(string(ssh.MarshalAuthorizedKey(certificate))),
		IssuedTokenType: SSHCertificateTokenType,
		TokenType:       "N_A",
		ExpiresIn:       int64(options.Issuer.Lifetime().Seconds()),
	})
}

func writeOAuthError(response http.ResponseWriter, status int, code, description string) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Pragma", "no-cache")
	writeJSON(response, status, oauthErrorResponse{Error: code, ErrorDescription: description})
}

func parseSSHCertificate(encoded string) (*ssh.Certificate, error) {
	publicKey, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(encoded))
	if err != nil {
		return nil, fmt.Errorf("parse SSH certificate: %w", err)
	}
	if strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("SSH certificate response contains trailing data")
	}
	certificate, ok := publicKey.(*ssh.Certificate)
	if !ok || certificate.CertType != ssh.UserCert {
		return nil, errors.New("response is not an SSH user certificate")
	}
	return certificate, nil
}
