package client

import (
	"testing"

	"github.com/fr0stylo/tearenv/internal/authn"
	"golang.org/x/oauth2"
)

func TestSelectOIDCSubjectToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tokenType string
		token     *oauth2.Token
		idToken   string
		want      string
		wantError bool
	}{
		{
			name: "ID token", tokenType: authn.SubjectTokenTypeIDToken,
			token: &oauth2.Token{AccessToken: "access-token"}, idToken: "id-token", want: "id-token",
		},
		{
			name: "access token", tokenType: authn.SubjectTokenTypeAccessToken,
			token: &oauth2.Token{AccessToken: "access-token"}, idToken: "id-token", want: "access-token",
		},
		{
			name: "missing access token", tokenType: authn.SubjectTokenTypeAccessToken,
			token: &oauth2.Token{}, idToken: "id-token", wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := selectOIDCSubjectToken(test.tokenType, test.token, test.idToken)
			if test.wantError && err == nil {
				t.Fatal("selectOIDCSubjectToken() succeeded")
			}
			if !test.wantError && (err != nil || got != test.want) {
				t.Fatalf("selectOIDCSubjectToken() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}
