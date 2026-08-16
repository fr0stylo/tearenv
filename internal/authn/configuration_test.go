package authn

import "testing"

func TestConfigurationNormalizesOIDCSubjectTokenType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tokenType string
		want      string
	}{
		{name: "legacy discovery defaults to access token", want: SubjectTokenTypeAccessToken},
		{name: "ID token is retained", tokenType: SubjectTokenTypeIDToken, want: SubjectTokenTypeIDToken},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			configuration := Configuration{OIDC: &OIDCConfiguration{SubjectTokenType: test.tokenType}}.Normalized()
			if configuration.OIDC.SubjectTokenType != test.want {
				t.Fatalf("subject token type = %q, want %q", configuration.OIDC.SubjectTokenType, test.want)
			}
		})
	}
}

func TestNormalizeSubjectTokenTypeAcceptsNamesAndURNs(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"access-token":              SubjectTokenTypeAccessToken,
		SubjectTokenTypeAccessToken: SubjectTokenTypeAccessToken,
		"id-token":                  SubjectTokenTypeIDToken,
		SubjectTokenTypeIDToken:     SubjectTokenTypeIDToken,
	}
	for input, want := range tests {
		if got, err := NormalizeSubjectTokenType(input); err != nil || got != want {
			t.Errorf("NormalizeSubjectTokenType(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := NormalizeSubjectTokenType("opaque"); err == nil {
		t.Fatal("NormalizeSubjectTokenType(opaque) succeeded")
	}
}
