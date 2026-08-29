package company

import "testing"

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
