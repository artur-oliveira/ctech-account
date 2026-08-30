package scopes

import (
	"strings"
	"testing"
)

// Every internal:account scope this repo declares as a constant must be in the
// Resource Server manifest, which is the source of truth
// (account-scope-manifest.json).
//
// A constant without a manifest entry cannot be granted: createclient validates
// the requested scopes against the published catalog and answers
// ErrInvalidScope. Nothing else notices — the code compiles, the route mounts,
// and provisioning the credential fails at the last step, which is the worst
// moment to discover it.
func TestEveryInternalAccountScopeIsInTheManifest(t *testing.T) {
	manifest, err := AccountManifest()
	if err != nil {
		t.Fatalf("AccountManifest: %v", err)
	}
	published := make(map[string]ScopeDefinition, len(manifest.Scopes))
	for _, s := range manifest.Scopes {
		published[s.Scope] = s
	}

	for _, scope := range []string{
		InternalAccountKYC,
		InternalAccountScopeRegistryWrite,
		InternalAccountCompanyActor,
	} {
		entry, ok := published[scope]
		if !ok {
			t.Errorf("%q is declared and not in the manifest; no client can be granted it", scope)
			continue
		}
		// Internal, not public: these are machine-to-machine, and a public one
		// would appear on a consent screen for a permission no person grants.
		if entry.Visibility != "internal" {
			t.Errorf("%q has visibility %q, want internal", scope, entry.Visibility)
		}
		if entry.Status != "active" {
			t.Errorf("%q has status %q, want active", scope, entry.Status)
		}
	}
}

// And the reverse: a manifest entry with no constant is a scope nothing in this
// repo can reference, which means either the constant was forgotten or the entry
// is dead. Either way somebody has to look.
func TestEveryInternalManifestScopeHasAConstant(t *testing.T) {
	manifest, err := AccountManifest()
	if err != nil {
		t.Fatalf("AccountManifest: %v", err)
	}
	declared := map[string]bool{
		InternalAccountKYC:                true,
		InternalAccountScopeRegistryWrite: true,
		InternalAccountCompanyActor:       true,
	}
	for _, s := range manifest.Scopes {
		if !strings.HasPrefix(s.Scope, "internal:") {
			continue
		}
		if !declared[s.Scope] {
			t.Errorf("the manifest publishes %q with no constant in this repo", s.Scope)
		}
	}
}
