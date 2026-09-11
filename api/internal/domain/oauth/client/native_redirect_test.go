package client

import "testing"

func TestNativeRedirectRegistration(t *testing.T) {
	for _, test := range []struct {
		uri     string
		allowed bool
	}{
		{"app.aoctech.poker:/oauth/callback", true},
		{"https://app.example/callback", true},
		{"http://localhost/callback", true},
		{"http://app.example/callback", false},
		{"javascript:alert(1)", false},
		{"data:text/html,hello", false},
		{"poker:/callback", false},
		{"app.aoctech.poker:opaque", false},
		{"app.aoctech.poker://host/callback", false},
		{"app.aoctech.poker:/", false},
		{"app.aoctech.poker:/callback?next=elsewhere", false},
		{"app.aoctech.poker:/callback#fragment", false},
	} {
		t.Run(test.uri, func(t *testing.T) {
			if got := validateRedirectURIs([]string{test.uri}) == nil; got != test.allowed {
				t.Fatalf("allowed=%v, want %v", got, test.allowed)
			}
		})
	}
	c := OAuthClient{RedirectURIs: []string{"app.aoctech.poker:/oauth/callback"}}
	if c.IsRedirectURIAllowed("app.aoctech.poker:/other") {
		t.Fatal("native redirects must still match exactly")
	}
}
