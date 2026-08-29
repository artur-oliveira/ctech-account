package main

import (
	"strings"
	"testing"
	"time"
)

func orgDecisionFor(owner string) decision {
	return decision{
		OwnerUserID: owner,
		OwnerName:   "Dono",
		CreatedAt:   time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC),
	}
}

// The tax id comes from the legacy key, and this is the one place in the whole
// re-key where reading a key for a document is correct: the legacy key IS the
// document, which is exactly the property ADR 0022 removes.
func TestTheTaxIDComesFromTheLegacyKey(t *testing.T) {
	got := planCompany(dfeOrg{PK: "CNPJ_11222333000181", Name: "ACME LTDA"}, nil, orgDecisionFor("usr_1"))
	if got.Action == actionReview {
		t.Fatalf("refused: %v", got.Review)
	}
	if got.TaxID != "11222333000181" || got.TaxIDKind != "cnpj" {
		t.Errorf("tax id = %q %q", got.TaxID, got.TaxIDKind)
	}
	if got.LegalName != "ACME LTDA" {
		t.Errorf("legal name = %q", got.LegalName)
	}
}

// A CPF issuer — produtor rural, MEI pessoa física — migrates as a CPF company.
// dfe has keyed them CPF_ all along.
func TestACPFIssuerMigratesAsACPFCompany(t *testing.T) {
	got := planCompany(dfeOrg{PK: "CPF_52998224725", Name: "PRODUTOR"}, nil, orgDecisionFor("usr_1"))
	if got.Action == actionReview {
		t.Fatalf("refused: %v", got.Review)
	}
	if got.TaxID != "52998224725" || got.TaxIDKind != "cpf" {
		t.Errorf("tax id = %q %q", got.TaxID, got.TaxIDKind)
	}
}

// A key whose document never was valid is not corrected here. It emitted under
// that key anyway, so whether the company is real and what its tax id should be
// is a person's call.
func TestAnInvalidDocumentNeedsAHuman(t *testing.T) {
	got := planCompany(dfeOrg{PK: "CNPJ_11222333000182", Name: "ACME"}, nil, orgDecisionFor("usr_1"))
	if got.Action != actionReview {
		t.Fatal("a key with a bad check digit was migrated")
	}
	if !strings.Contains(strings.Join(got.Review, " "), "11222333000182") {
		t.Errorf("the review does not name the document: %v", got.Review)
	}
}

func TestAKeyThatIsNotADocumentNeedsAHuman(t *testing.T) {
	got := planCompany(dfeOrg{PK: "ORG_something", Name: "ACME"}, nil, orgDecisionFor("usr_1"))
	if got.Action != actionReview {
		t.Fatal("a key carrying no document was migrated")
	}
}

// The legal name lands on a document's emit node here and on an invoice in
// billing. Importing an empty one moves the problem instead of surfacing it.
func TestAnEmptyNameNeedsAHuman(t *testing.T) {
	got := planCompany(dfeOrg{PK: "CNPJ_11222333000181", Name: "   "}, nil, orgDecisionFor("usr_1"))
	if got.Action != actionReview {
		t.Fatal("a company with no legal name was migrated")
	}
}

// Every member becomes an actor. Membership gets you into the workspace; the
// edge is what lets you act for the company inside it, and a migration that
// dropped it would lock everybody out of issuing.
func TestEveryMemberBecomesAnActor(t *testing.T) {
	members := []dfeMember{
		{UserID: "usr_1", Name: "Ana", Role: "OWNER", CreatedAt: "2026-01-01T00:00:00Z"},
		{UserID: "usr_2", Name: "Bruno", Role: "USER", CreatedAt: "2026-02-01T00:00:00Z"},
	}
	got := planCompany(dfeOrg{PK: "CNPJ_11222333000181", Name: "ACME"}, members, orgDecisionFor("usr_1"))
	if len(got.Actors) != 2 {
		t.Fatalf("got %d actors, want 2: %+v", len(got.Actors), got.Actors)
	}
}

// dfe let an organization have an owner with no membership row. That person
// still acts for their own company.
func TestTheOwnerActsEvenWithNoMembershipRow(t *testing.T) {
	got := planCompany(dfeOrg{PK: "CNPJ_11222333000181", Name: "ACME"}, nil, orgDecisionFor("usr_owner"))
	if len(got.Actors) != 1 || got.Actors[0].UserID != "usr_owner" {
		t.Fatalf("actors = %+v, want the owner", got.Actors)
	}
}

// And is not added twice when they do have one.
func TestTheOwnerIsNotDuplicated(t *testing.T) {
	members := []dfeMember{{UserID: "usr_owner", Name: "Ana", Role: "OWNER", CreatedAt: "2026-01-01T00:00:00Z"}}
	got := planCompany(dfeOrg{PK: "CNPJ_11222333000181", Name: "ACME"}, members, orgDecisionFor("usr_owner"))
	if len(got.Actors) != 1 {
		t.Fatalf("got %d actors, want 1: %+v", len(got.Actors), got.Actors)
	}
}

// A company nobody may act for cannot emit, and nothing in the product offers a
// way to grant the first edge — it would take a support ticket.
func TestACompanyWithNobodyNeedsAHuman(t *testing.T) {
	got := planCompany(dfeOrg{PK: "CNPJ_11222333000181", Name: "ACME"}, nil, decision{})
	if got.Action != actionReview {
		t.Fatal("a company with no actors was migrated")
	}
	if !strings.Contains(strings.Join(got.Review, " "), "act for") {
		t.Errorf("the review does not say why: %v", got.Review)
	}
}

// A membership row with no user id is already reported by planOrg. Reporting it
// here too would double every such line in a report somebody has to read.
func TestAnEmptyUserIDIsNotReportedTwice(t *testing.T) {
	members := []dfeMember{{UserID: "  ", Role: "USER"}}
	got := planCompany(dfeOrg{PK: "CNPJ_11222333000181", Name: "ACME"}, members, orgDecisionFor("usr_1"))
	for _, r := range got.Review {
		if strings.Contains(r, "user_id") {
			t.Errorf("planCompany repeated planOrg's refusal: %q", r)
		}
	}
}

func TestLegacyDocumentReadsOnlyTheRetiredShapes(t *testing.T) {
	if got := legacyDocument("CNPJ_11222333000181"); got != "11222333000181" {
		t.Errorf("CNPJ: %q", got)
	}
	if got := legacyDocument("CPF_52998224725"); got != "52998224725" {
		t.Errorf("CPF: %q", got)
	}
	// A company id carries nothing, which is the whole point of the re-key.
	if got := legacyDocument("0199f3a1-8c42-7c31-9d5e-6a2b4c8e1f70"); got != "" {
		t.Errorf("a company id yielded %q", got)
	}
}
