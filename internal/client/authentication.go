package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/fr0stylo/tearenv/internal/authn"
	"github.com/fr0stylo/tearenv/internal/registration"
	"golang.org/x/crypto/ssh"
)

const authenticationResponseLimit = 1 << 20

// DiscoverAuthentication returns the public authentication configuration.
func DiscoverAuthentication(ctx context.Context, httpClient *http.Client, baseURL string) (authn.Configuration, error) {
	endpoint, err := resolveAPIEndpoint(baseURL, "/.well-known/tearenv-configuration")
	if err != nil {
		return authn.Configuration{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return authn.Configuration{}, fmt.Errorf("create authentication discovery request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	contents, status, err := doLimitedRequest(httpClient, request)
	if err != nil {
		return authn.Configuration{}, fmt.Errorf("discover tearenv authentication: %w", err)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return authn.Configuration{}, fmt.Errorf("discover tearenv authentication: API returned %s", http.StatusText(status))
	}
	var configuration authn.Configuration
	if err := json.Unmarshal(contents, &configuration); err != nil {
		return authn.Configuration{}, fmt.Errorf("decode authentication configuration: %w", err)
	}
	configuration = configuration.Normalized()
	if configuration.Mode == "" {
		return authn.Configuration{}, errors.New("authentication configuration has no mode")
	}
	return configuration, nil
}

// ExchangeSSHCertificate exchanges an OIDC subject token for a short-lived SSH
// certificate bound to an already registered local public key.
func ExchangeSSHCertificate(
	ctx context.Context,
	httpClient *http.Client,
	baseURL string,
	configuration authn.Configuration,
	subjectToken, keyName, identity string,
	privateSigner ssh.Signer,
) (ssh.Signer, error) {
	if privateSigner == nil {
		return nil, errors.New("private key signer is required")
	}
	configuration = configuration.Normalized()
	if configuration.OIDC == nil {
		return nil, errors.New("OIDC authentication configuration is required")
	}
	subjectTokenType, err := authn.NormalizeSubjectTokenType(configuration.OIDC.SubjectTokenType)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(subjectToken) == "" {
		return nil, errors.New("OIDC subject token is required")
	}
	endpoint, err := resolveAPIEndpoint(baseURL, configuration.TokenEndpoint)
	if err != nil {
		return nil, fmt.Errorf("resolve token exchange endpoint: %w", err)
	}
	form := url.Values{
		"grant_type":           {registration.TokenExchangeGrantType},
		"subject_token":        {strings.TrimSpace(subjectToken)},
		"subject_token_type":   {subjectTokenType},
		"requested_token_type": {registration.SSHCertificateTokenType},
		"scope":                {registration.SSHConnectScope},
		"key_id":               {strings.TrimSpace(keyName)},
	}
	if strings.TrimSpace(configuration.OIDC.Audience) != "" {
		form.Set("audience", configuration.OIDC.Audience)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create SSH token exchange request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	contents, status, err := doLimitedRequest(httpClient, request)
	if err != nil {
		return nil, fmt.Errorf("exchange SSH certificate: %w", err)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		var response struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.Unmarshal(contents, &response)
		message := strings.TrimSpace(response.ErrorDescription)
		if message == "" {
			message = strings.TrimSpace(response.Error)
		}
		if message == "" {
			message = http.StatusText(status)
		}
		return nil, fmt.Errorf("exchange SSH certificate: API returned %d: %s", status, message)
	}
	var response struct {
		AccessToken     string `json:"access_token"`
		IssuedTokenType string `json:"issued_token_type"`
	}
	if err := json.Unmarshal(contents, &response); err != nil {
		return nil, fmt.Errorf("decode SSH token exchange response: %w", err)
	}
	if response.IssuedTokenType != registration.SSHCertificateTokenType {
		return nil, errors.New("token exchange returned an unexpected token type")
	}
	publicKey, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(response.AccessToken))
	if err != nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("token exchange returned an invalid SSH certificate")
	}
	certificate, ok := publicKey.(*ssh.Certificate)
	if !ok || certificate.CertType != ssh.UserCert {
		return nil, errors.New("token exchange did not return an SSH user certificate")
	}
	if !bytes.Equal(certificate.Key.Marshal(), privateSigner.PublicKey().Marshal()) {
		return nil, errors.New("SSH certificate does not match the local private key")
	}
	if err := certificatePrincipal(certificate, identity); err != nil {
		return nil, err
	}
	certificateSigner, err := ssh.NewCertSigner(certificate, privateSigner)
	if err != nil {
		return nil, fmt.Errorf("create SSH certificate signer: %w", err)
	}
	return certificateSigner, nil
}

func certificatePrincipal(certificate *ssh.Certificate, identity string) error {
	for _, principal := range certificate.ValidPrincipals {
		if principal == identity {
			return nil
		}
	}
	return errors.New("SSH certificate is not valid for the saved identity")
}

func resolveAPIEndpoint(baseURL, endpoint string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return "", errors.New("API URL must be an absolute HTTP or HTTPS URL")
	}
	if base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("API URL must not contain a query or fragment")
	}
	reference, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", fmt.Errorf("parse API endpoint: %w", err)
	}
	if reference.IsAbs() && (reference.Scheme != "http" && reference.Scheme != "https") {
		return "", errors.New("API endpoint must use HTTP or HTTPS")
	}
	if reference.IsAbs() {
		return reference.String(), nil
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/"
	base.RawPath = ""
	reference.Path = strings.TrimLeft(reference.Path, "/")
	reference.RawPath = ""
	return base.ResolveReference(reference).String(), nil
}

func doLimitedRequest(httpClient *http.Client, request *http.Request) ([]byte, int, error) {
	if httpClient == nil {
		return nil, 0, errors.New("HTTP client is required")
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, authenticationResponseLimit+1))
	if err != nil {
		return nil, response.StatusCode, err
	}
	if len(contents) > authenticationResponseLimit {
		return nil, response.StatusCode, errors.New("response exceeds 1 MiB")
	}
	return contents, response.StatusCode, nil
}
