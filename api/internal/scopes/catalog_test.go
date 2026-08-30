package scopes

import "testing"

// A scope constant that is not in the catalog cannot be granted: createclient
// validates against it and answers ErrInvalidScope. Declaring the constant and
// forgetting the entry is the mistake this catches — the code compiles, the
// route is mounted, and provisioning the credential fails at the last step.
//
// It found a real one: internal:account:kyc was live in {env}_ctech_scopes and
// missing from this seed, so a seedscopes run from scratch would have dropped
// it — and ctech-wallet's KYC check with it.
func TestEveryInternalAccountScopeIsInTheCatalog(t *testing.T) {
	registered := map[string]bool{}
	for _, svc := range defaultCatalog {
		for _, e := range svc.Scopes {
			registered[e.Scope] = true
		}
	}
	for _, scope := range []string{
		InternalAccountKYC,
		InternalAccountScopeRegistryWrite,
		InternalAccountCompanyActor,
	} {
		if !registered[scope] {
			t.Errorf("%q is declared and not in the catalog; no client can be granted it", scope)
		}
	}
}
