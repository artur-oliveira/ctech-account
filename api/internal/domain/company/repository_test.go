package company

import (
	"strings"
	"testing"
)

func TestKeysNamespaceTheThreeRowKinds(t *testing.T) {
	if got := orgPK("org_1"); got != "ORG#org_1" {
		t.Errorf("orgPK = %q", got)
	}
	if got := companySK("cmp_1"); got != "COMPANY#cmp_1" {
		t.Errorf("companySK = %q", got)
	}
	if got := taxIDSK("12ABC34501DE35"); got != "TAXID#12ABC34501DE35" {
		t.Errorf("taxIDSK = %q", got)
	}
	if got := actorSK("cmp_1", "usr_1"); got != "ACTOR#cmp_1#usr_1" {
		t.Errorf("actorSK = %q", got)
	}
}

// The prefixes must not be prefixes of one another, or a Query for companies
// would also return actors and tax-id locks. This is what stops a future
// rename from making them overlap.
func TestRowKindPrefixesDoNotOverlap(t *testing.T) {
	prefixes := []string{companySKPrefix, taxIDSKPrefix, actorSKPrefix}
	for i, a := range prefixes {
		for j, b := range prefixes {
			if i != j && strings.HasPrefix(b, a) {
				t.Errorf("%q is a prefix of %q", a, b)
			}
		}
	}
}

func TestLookupKeysAreNamespacedByKind(t *testing.T) {
	if got := lookupUserPK("usr_1"); got != "USER#usr_1" {
		t.Errorf("lookupUserPK = %q", got)
	}
	if got := lookupSourcePK("dfe", "CNPJ_1"); got != "SOURCE#dfe#CNPJ_1" {
		t.Errorf("lookupSourcePK = %q", got)
	}
}

// A user id may itself contain "#", so the split has to take the first
// separator only — otherwise the company id would swallow part of it.
func TestActorIDsAreRecoveredFromTheSortKey(t *testing.T) {
	companyID, userID := actorIDsFromSK(actorSK("cmp_1", "usr#odd"))
	if companyID != "cmp_1" || userID != "usr#odd" {
		t.Errorf("got %q %q, want cmp_1 usr#odd", companyID, userID)
	}
}
