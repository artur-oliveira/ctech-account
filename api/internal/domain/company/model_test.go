package company

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidateNamesTrimsAndRequiresALegalName(t *testing.T) {
	legal, trade, ok := ValidateNames("  Acme LTDA  ", "  Acme  ")
	if !ok || legal != "Acme LTDA" || trade != "Acme" {
		t.Fatalf("got %q %q %v", legal, trade, ok)
	}
	if _, _, ok := ValidateNames("   ", "Acme"); ok {
		t.Error("accepted an empty legal name")
	}
}

// The trade name is optional: most companies do not have one, and requiring it
// would mean typing the razão social twice.
func TestValidateNamesAllowsAnAbsentTradeName(t *testing.T) {
	legal, trade, ok := ValidateNames("Acme LTDA", "")
	if !ok || legal != "Acme LTDA" || trade != "" {
		t.Fatalf("got %q %q %v", legal, trade, ok)
	}
}

func TestValidateNamesRejectsAnOverlongName(t *testing.T) {
	long := make([]byte, maxCompanyName+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, _, ok := ValidateNames(string(long), ""); ok {
		t.Error("accepted a legal name past the storage bound")
	}
	if _, _, ok := ValidateNames("Acme LTDA", string(long)); ok {
		t.Error("accepted a trade name past the storage bound")
	}
}

// ADR 0023 keeps the role in the product. A field here would be the platform
// growing a permission model, and the next question is whether ctech-billing
// may use the same value — which is the argument that ADR settles.
//
// Reflection is unusual in a test and deliberate here: the failure it guards is
// somebody adding a field in good faith, and only a test that reads the struct
// itself notices that. A test listing the fields it expects would pass while the
// new one sat beside them.
func TestTheActorEdgeCarriesNoRole(t *testing.T) {
	v := reflect.TypeOf(Actor{})
	forbidden := map[string]string{
		"role":        "a role is a named bundle of permissions and belongs to the product",
		"roles":       "a role is a named bundle of permissions and belongs to the product",
		"permissions": "the permission vocabulary belongs to whichever product defines the verbs",
		"owner":       "who may grant is answered by the product's OWNER row",
		"isowner":     "who may grant is answered by the product's OWNER row",
		"grants":      "explicit grants live with the permissions they extend",
	}
	for i := 0; i < v.NumField(); i++ {
		name := v.Field(i).Name
		if why, bad := forbidden[strings.ToLower(name)]; bad {
			t.Errorf("Actor grew %q — %s (ctech-billing ADR 0023)", name, why)
		}
	}
}

// And the Company itself carries no owner. Same rule, other struct: somebody
// adding it here would be answering "who may grant" in the platform.
func TestTheCompanyCarriesNoOwner(t *testing.T) {
	v := reflect.TypeOf(Company{})
	for i := 0; i < v.NumField(); i++ {
		switch strings.ToLower(v.Field(i).Name) {
		case "owner", "ownerid", "owneruserid", "role":
			t.Errorf("Company grew %q; ADR 0023 answers ownership with the product's OWNER row", v.Field(i).Name)
		}
	}
}
