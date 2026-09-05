package client

import "testing"

// IsNativeClient decides whether a public client's refresh_token is safe (and
// necessary) to hand back in the authorization_code JSON body — see its doc
// comment and handler/token.go's exchangeCode. Get this wrong in either
// direction and either a browser SPA's refresh token becomes exfiltratable via
// XSS, or a native/CLI client can never refresh and is forced to re-login on
// every access-token expiry (poker-cli's live-reproduced bug).
func TestIsNativeClientRequiresLoopbackRedirectAndPublicType(t *testing.T) {
	tests := []struct {
		name string
		c    *OAuthClient
		want bool
	}{
		{
			"loopback public client is native",
			&OAuthClient{ClientType: TypePublic, RedirectURIs: []string{"http://127.0.0.1:51789/callback"}},
			true,
		},
		{
			"localhost public client is native",
			&OAuthClient{ClientType: TypePublic, RedirectURIs: []string{"http://localhost:8080/callback"}},
			true,
		},
		{
			"https SPA public client is not native",
			&OAuthClient{ClientType: TypePublic, RedirectURIs: []string{"https://poker.aoctech.app/login/callback"}},
			false,
		},
		{
			"mixed loopback and https is not native (one non-loopback redirect is enough to disqualify)",
			&OAuthClient{ClientType: TypePublic, RedirectURIs: []string{
				"http://127.0.0.1:51789/callback", "https://poker.aoctech.app/login/callback",
			}},
			false,
		},
		{
			"confidential client is never native even with a loopback redirect",
			&OAuthClient{ClientType: TypeConfidential, RedirectURIs: []string{"http://127.0.0.1:51789/callback"}},
			false,
		},
		{
			"no redirect_uris is not native",
			&OAuthClient{ClientType: TypePublic},
			false,
		},
		{
			"malformed redirect_uri is not native",
			&OAuthClient{ClientType: TypePublic, RedirectURIs: []string{"://not a url"}},
			false,
		},
		{
			"https loopback (not http) is not native — RFC 8252 native redirects are http",
			&OAuthClient{ClientType: TypePublic, RedirectURIs: []string{"https://127.0.0.1:51789/callback"}},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.IsNativeClient(); got != tt.want {
				t.Errorf("IsNativeClient() = %v, want %v", got, tt.want)
			}
		})
	}
}
