package client

import "testing"

// IsRegisteredOrigin gates two redirects — RP-initiated logout and the
// organization handoff's return_to — so it is the whole open-redirect defence
// for both. It had no test until the second caller arrived.
func TestIsRegisteredOriginMatchesSchemeAndHost(t *testing.T) {
	c := &OAuthClient{RedirectURIs: []string{"https://dfe.example/callback"}}

	allowed := []string{
		"https://dfe.example/callback",
		"https://dfe.example/empresas/vincular",
		"https://dfe.example/x?y=1#z",
		// A port-less URL and its default port are the same origin to a
		// browser, but not to a string compare — pinned so nobody "fixes" this
		// into something looser.
		"https://dfe.example",
	}
	for _, uri := range allowed {
		if !c.IsRegisteredOrigin(uri) {
			t.Errorf("%q: refused, want allowed", uri)
		}
	}

	refused := []string{
		"http://dfe.example/callback",         // a different scheme
		"https://outro.example/callback",      // a different host
		"https://dfe.example.evil.com/x",      // a lookalike suffix
		"https://evil.com/https://dfe.example", // the host in the path
		"https://dfe.example:8443/x",          // a different port
		"//dfe.example/x",                     // scheme-relative, no scheme
		"/empresas/vincular",                  // relative, no host
		"",
		"::not a url",
		"javascript:alert(1)",
	}
	for _, uri := range refused {
		if c.IsRegisteredOrigin(uri) {
			t.Errorf("%q: allowed, want refused", uri)
		}
	}
}

// A client with no registered redirect_uri matches nothing. The zero value must
// fail closed, not open.
func TestIsRegisteredOriginRefusesEverythingWithoutRegistration(t *testing.T) {
	c := &OAuthClient{}
	if c.IsRegisteredOrigin("https://dfe.example/x") {
		t.Error("a client with no redirect_uris allowed an origin")
	}
}
