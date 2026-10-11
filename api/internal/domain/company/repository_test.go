package company

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"gopkg.aoctech.app/account/api/internal/database"
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

// An organization past one Query page (an accountant's hundreds of CNPJs, or a
// 1 MB page) must come back whole: the list is sorted by name afterwards, and
// sorting a first page by id would hand out an arbitrary subset.
func TestListCompaniesReadsEveryPage(t *testing.T) {
	page := func(ids ...string) []map[string]types.AttributeValue {
		out := make([]map[string]types.AttributeValue, 0, len(ids))
		for _, id := range ids {
			out = append(out, map[string]types.AttributeValue{
				"pk":         &types.AttributeValueMemberS{Value: orgPK("org_1")},
				"sk":         &types.AttributeValueMemberS{Value: companySK(id)},
				"legal_name": &types.AttributeValueMemberS{Value: "Empresa " + id},
			})
		}
		return out
	}
	pages := []*database.QueryResult{
		{Items: page("cmp_1", "cmp_2"), LastEvaluatedKey: map[string]types.AttributeValue{"sk": &types.AttributeValueMemberS{Value: companySK("cmp_2")}}},
		{Items: page("cmp_3")},
	}
	var calls []database.QueryOpts
	query := func(_ context.Context, opts database.QueryOpts) (*database.QueryResult, error) {
		calls = append(calls, opts)
		return pages[len(calls)-1], nil
	}
	got, err := listCompanies(context.Background(), query, "org_1")
	if err != nil {
		t.Fatalf("listCompanies: %v", err)
	}
	if len(got) != 3 || got[2].ID != "cmp_3" || got[0].OrganizationID != "org_1" {
		t.Fatalf("got %d companies (%+v), want all three pages' worth", len(got), got)
	}
	if len(calls) != 2 || calls[1].ExclusiveStartKey == nil || calls[0].PK != orgPK("org_1") || calls[0].SKPrefix != companySKPrefix {
		t.Fatalf("calls = %+v, want a second call from the first page's key", calls)
	}
}
