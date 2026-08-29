package organization

import "testing"

// The keys are asserted directly because two access paths depend on their exact
// shape — a member listing queries the partition, and a person's organizations
// query the index — and a key built two ways is two answers.
func TestKeyShapes(t *testing.T) {
	if got := orgPK("01ABC"); got != "ORG#01ABC" {
		t.Errorf("orgPK = %q", got)
	}
	if got := memberSK("usr_1"); got != "MEMBER#usr_1" {
		t.Errorf("memberSK = %q", got)
	}
	if got := lookupUserPK("usr_1"); got != "USER#usr_1" {
		t.Errorf("lookupUserPK = %q", got)
	}
	if got := inviteSK("Artur@Example.com "); got != "INVITE#artur@example.com" {
		t.Errorf("inviteSK = %q — an invitation is keyed on the normalized address, or the same person is invited twice", got)
	}
}

// The organization id travels in the key, not in the item, so reading a row
// back has to recover it. A reader that forgets returns memberships that do not
// say which organization they are for.
func TestIDsAreRecoveredFromKeys(t *testing.T) {
	if got := orgIDFromPK("ORG#01ABC"); got != "01ABC" {
		t.Errorf("orgIDFromPK = %q", got)
	}
	if got := userIDFromSK("MEMBER#usr_1"); got != "usr_1" {
		t.Errorf("userIDFromSK = %q", got)
	}
	if got := emailFromSK("INVITE#a@b.com"); got != "a@b.com" {
		t.Errorf("emailFromSK = %q", got)
	}
}

// The migration has to be re-runnable, which means it must be able to ask "did
// I already import this dfe organization". The source ref is the key it asks
// with, and only migrated rows carry one — the index is sparse on purpose, so
// organizations created through the product never appear in it.
func TestSourceRefKeyIsNamespaced(t *testing.T) {
	if got := lookupSourcePK("dfe", "CNPJ_123"); got != "SOURCE#dfe#CNPJ_123" {
		t.Errorf("lookupSourcePK = %q", got)
	}
	// Two systems could hand over the same key. Namespacing by system is what
	// keeps a dfe organization and some future import from colliding.
	if lookupSourcePK("dfe", "X") == lookupSourcePK("wallet", "X") {
		t.Error("source refs from different systems must not collide")
	}
}
