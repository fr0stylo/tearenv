package client

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/fr0stylo/tearenv/internal/authn"
	"golang.org/x/oauth2"
)

// OIDCLoginOptions controls the native application authentication flow.
type OIDCLoginOptions struct {
	Device  bool
	Output  io.Writer
	OpenURL func(string) error
}

// OIDCLoginResult contains the ephemeral exchange credential and claim-derived identity.
// The subject token must remain in memory and is intentionally never persisted.
type OIDCLoginResult struct {
	SubjectToken     string
	SubjectTokenType string
	Identity         string
}

// OIDCLogin authenticates with authorization-code + PKCE or device authorization.
func OIDCLogin(
	ctx context.Context,
	httpClient *http.Client,
	configuration authn.OIDCConfiguration,
	options OIDCLoginOptions,
) (OIDCLoginResult, error) {
	if httpClient == nil {
		return OIDCLoginResult{}, errors.New("HTTP client is required")
	}
	if options.Output == nil {
		options.Output = io.Discard
	}
	if options.OpenURL == nil {
		options.OpenURL = openBrowser
	}
	if strings.TrimSpace(configuration.IssuerURL) == "" || strings.TrimSpace(configuration.ClientID) == "" {
		return OIDCLoginResult{}, errors.New("OIDC issuer and client ID are required")
	}
	if strings.TrimSpace(configuration.IdentityClaim) == "" {
		configuration.IdentityClaim = "preferred_username"
	}
	configuration = *authn.Configuration{OIDC: &configuration}.Normalized().OIDC
	if _, err := authn.NormalizeSubjectTokenType(configuration.SubjectTokenType); err != nil {
		return OIDCLoginResult{}, err
	}

	oidcContext := oidc.ClientContext(ctx, httpClient)
	provider, err := oidc.NewProvider(oidcContext, configuration.IssuerURL)
	if err != nil {
		return OIDCLoginResult{}, fmt.Errorf("discover OIDC provider: %w", err)
	}
	oauthConfig := oauth2.Config{
		ClientID: configuration.ClientID,
		Endpoint: provider.Endpoint(),
		Scopes:   configuration.Scopes,
	}
	var token *oauth2.Token
	var nonce string
	if options.Device {
		if !configuration.DeviceFlow {
			return OIDCLoginResult{}, errors.New("the server does not advertise OIDC device authorization")
		}
		token, err = oidcDeviceLogin(oidcContext, oauthConfig, options.Output)
	} else {
		token, nonce, err = oidcBrowserLogin(oidcContext, oauthConfig, options)
	}
	if err != nil {
		return OIDCLoginResult{}, err
	}
	return validateOIDCToken(oidcContext, provider, configuration, token, nonce)
}

func oidcDeviceLogin(ctx context.Context, oauthConfig oauth2.Config, output io.Writer) (*oauth2.Token, error) {
	device, err := oauthConfig.DeviceAuth(ctx)
	if err != nil {
		return nil, fmt.Errorf("start OIDC device authorization: %w", err)
	}
	verificationURL := device.VerificationURIComplete
	if verificationURL == "" {
		verificationURL = device.VerificationURI
	}
	if _, err := fmt.Fprintf(output, "Open %s and enter code %s\n", verificationURL, device.UserCode); err != nil {
		return nil, fmt.Errorf("write device authorization instructions: %w", err)
	}
	token, err := oauthConfig.DeviceAccessToken(ctx, device)
	if err != nil {
		return nil, fmt.Errorf("complete OIDC device authorization: %w", err)
	}
	return token, nil
}

type authorizationCallback struct {
	code string
	err  error
}

func oidcBrowserLogin(ctx context.Context, oauthConfig oauth2.Config, options OIDCLoginOptions) (*oauth2.Token, string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("listen for OIDC callback: %w", err)
	}
	defer listener.Close()
	redirectURL := "http://" + listener.Addr().String() + "/callback"
	oauthConfig.RedirectURL = redirectURL
	state, err := randomURLToken(32)
	if err != nil {
		return nil, "", err
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return nil, "", err
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		return nil, "", err
	}
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	callback := make(chan authorizationCallback, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(response http.ResponseWriter, request *http.Request) {
		result := authorizationCallback{}
		switch {
		case request.URL.Query().Get("state") != state:
			result.err = errors.New("OIDC callback state did not match")
		case request.URL.Query().Get("error") != "":
			result.err = fmt.Errorf("OIDC authorization failed: %s", request.URL.Query().Get("error"))
		case request.URL.Query().Get("code") == "":
			result.err = errors.New("OIDC callback did not include an authorization code")
		default:
			result.code = request.URL.Query().Get("code")
		}
		if result.err != nil {
			http.Error(response, "Tearenv login failed. You may close this window.", http.StatusBadRequest)
		} else {
			response.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(response, "Tearenv login completed. You may close this window.\n")
		}
		select {
		case callback <- result:
		default:
		}
	})
	callbackServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serveDone := make(chan error, 1)
	go func() { serveDone <- callbackServer.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = callbackServer.Shutdown(shutdownCtx)
	}()

	authorizationURL := oauthConfig.AuthCodeURL(
		state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	if _, err := fmt.Fprintf(options.Output, "Open this URL to authenticate:\n%s\n", authorizationURL); err != nil {
		return nil, "", fmt.Errorf("write OIDC authorization URL: %w", err)
	}
	_ = options.OpenURL(authorizationURL)

	var result authorizationCallback
	select {
	case <-ctx.Done():
		return nil, "", fmt.Errorf("wait for OIDC callback: %w", ctx.Err())
	case serveErr := <-serveDone:
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return nil, "", fmt.Errorf("serve OIDC callback: %w", serveErr)
		}
		return nil, "", errors.New("OIDC callback server stopped")
	case result = <-callback:
	}
	if result.err != nil {
		return nil, "", result.err
	}
	token, err := oauthConfig.Exchange(ctx, result.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, "", fmt.Errorf("exchange OIDC authorization code: %w", err)
	}
	return token, nonce, nil
}

func validateOIDCToken(
	ctx context.Context,
	provider *oidc.Provider,
	configuration authn.OIDCConfiguration,
	token *oauth2.Token,
	nonce string,
) (OIDCLoginResult, error) {
	if token == nil {
		return OIDCLoginResult{}, errors.New("OIDC provider returned no token response")
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || strings.TrimSpace(rawIDToken) == "" {
		return OIDCLoginResult{}, errors.New("OIDC provider returned no ID token")
	}
	verified, err := provider.Verifier(&oidc.Config{ClientID: configuration.ClientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return OIDCLoginResult{}, fmt.Errorf("verify OIDC ID token: %w", err)
	}
	claims := make(map[string]json.RawMessage)
	if err := verified.Claims(&claims); err != nil {
		return OIDCLoginResult{}, fmt.Errorf("decode OIDC ID token claims: %w", err)
	}
	if nonce != "" {
		var tokenNonce string
		if err := json.Unmarshal(claims["nonce"], &tokenNonce); err != nil || tokenNonce != nonce {
			return OIDCLoginResult{}, errors.New("OIDC ID token nonce did not match")
		}
	}
	var identity string
	if err := json.Unmarshal(claims[configuration.IdentityClaim], &identity); err != nil || strings.TrimSpace(identity) == "" {
		return OIDCLoginResult{}, fmt.Errorf("OIDC ID token has no usable %q claim", configuration.IdentityClaim)
	}
	subjectTokenType, err := authn.NormalizeSubjectTokenType(configuration.SubjectTokenType)
	if err != nil {
		return OIDCLoginResult{}, err
	}
	subjectToken, err := selectOIDCSubjectToken(subjectTokenType, token, rawIDToken)
	if err != nil {
		return OIDCLoginResult{}, err
	}
	return OIDCLoginResult{
		SubjectToken: subjectToken, SubjectTokenType: subjectTokenType, Identity: strings.TrimSpace(identity),
	}, nil
}

func selectOIDCSubjectToken(subjectTokenType string, token *oauth2.Token, rawIDToken string) (string, error) {
	switch subjectTokenType {
	case authn.SubjectTokenTypeIDToken:
		return strings.TrimSpace(rawIDToken), nil
	case authn.SubjectTokenTypeAccessToken:
		accessToken := strings.TrimSpace(token.AccessToken)
		if accessToken == "" {
			return "", errors.New("OIDC provider returned no access token")
		}
		return accessToken, nil
	default:
		return "", errors.New("unsupported OIDC subject token type")
	}
}

func randomURLToken(size int) (string, error) {
	contents := make([]byte, size)
	if _, err := rand.Read(contents); err != nil {
		return "", fmt.Errorf("generate OAuth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(contents), nil
}

func openBrowser(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	return command.Start()
}
