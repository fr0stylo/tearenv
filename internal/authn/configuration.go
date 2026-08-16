package authn

import (
	"errors"
	"strings"
)

const (
	// SubjectTokenTypeAccessToken is the RFC 8693 access-token type identifier.
	SubjectTokenTypeAccessToken = "urn:ietf:params:oauth:token-type:access_token"
	// SubjectTokenTypeIDToken is the RFC 8693 ID-token type identifier.
	SubjectTokenTypeIDToken = "urn:ietf:params:oauth:token-type:id_token"
)

// Configuration is the public authentication metadata advertised by tearenvd.
type Configuration struct {
	Mode          string             `json:"mode"`
	TokenEndpoint string             `json:"tokenEndpoint,omitempty"`
	OIDC          *OIDCConfiguration `json:"oidc,omitempty"`
}

// OIDCConfiguration contains public native-client settings. ClientID is not a secret.
type OIDCConfiguration struct {
	IssuerURL        string   `json:"issuerURL"`
	ClientID         string   `json:"clientID"`
	Audience         string   `json:"audience"`
	Scopes           []string `json:"scopes"`
	IdentityClaim    string   `json:"identityClaim"`
	SubjectTokenType string   `json:"subjectTokenType"`
	DeviceFlow       bool     `json:"deviceFlow"`
}

// Normalized returns a copy with trimmed strings and a required openid scope.
func (configuration Configuration) Normalized() Configuration {
	configuration.Mode = strings.TrimSpace(configuration.Mode)
	configuration.TokenEndpoint = strings.TrimSpace(configuration.TokenEndpoint)
	if configuration.OIDC == nil {
		return configuration
	}
	oidcConfiguration := *configuration.OIDC
	oidcConfiguration.IssuerURL = strings.TrimRight(strings.TrimSpace(oidcConfiguration.IssuerURL), "/")
	oidcConfiguration.ClientID = strings.TrimSpace(oidcConfiguration.ClientID)
	oidcConfiguration.Audience = strings.TrimSpace(oidcConfiguration.Audience)
	oidcConfiguration.IdentityClaim = strings.TrimSpace(oidcConfiguration.IdentityClaim)
	oidcConfiguration.SubjectTokenType = strings.TrimSpace(oidcConfiguration.SubjectTokenType)
	if oidcConfiguration.SubjectTokenType == "" {
		// Discovery responses produced before ID-token support implicitly used an
		// RFC 9068 access token. Preserve that behavior for older servers.
		oidcConfiguration.SubjectTokenType = SubjectTokenTypeAccessToken
	}
	seenOpenID := false
	for _, scope := range oidcConfiguration.Scopes {
		if scope == "openid" {
			seenOpenID = true
		}
	}
	if !seenOpenID {
		oidcConfiguration.Scopes = append([]string{"openid"}, oidcConfiguration.Scopes...)
	}
	configuration.OIDC = &oidcConfiguration
	return configuration
}

// NormalizeSubjectTokenType accepts CLI-friendly names and RFC 8693 URNs.
func NormalizeSubjectTokenType(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "access-token", SubjectTokenTypeAccessToken:
		return SubjectTokenTypeAccessToken, nil
	case "id-token", SubjectTokenTypeIDToken:
		return SubjectTokenTypeIDToken, nil
	default:
		return "", errors.New("OIDC subject token type must be id-token or access-token")
	}
}
