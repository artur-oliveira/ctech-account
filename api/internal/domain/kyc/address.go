package kyc

import "strings"

// ZipCodeDigits is the length of a Brazilian CEP with the separator stripped.
const ZipCodeDigits = 8

// brazilianStates is the closed set of UF codes. Address.State is validated
// against it rather than a length check so a typo cannot reach storage.
var brazilianStates = map[string]struct{}{
	"AC": {}, "AL": {}, "AP": {}, "AM": {}, "BA": {}, "CE": {}, "DF": {},
	"ES": {}, "GO": {}, "MA": {}, "MT": {}, "MS": {}, "MG": {}, "PA": {},
	"PB": {}, "PR": {}, "PE": {}, "PI": {}, "RJ": {}, "RN": {}, "RS": {},
	"RO": {}, "RR": {}, "SC": {}, "SP": {}, "SE": {}, "TO": {},
}

// IsValidState reports whether uf is one of the 27 Brazilian UF codes.
func IsValidState(uf string) bool {
	_, ok := brazilianStates[strings.ToUpper(uf)]
	return ok
}

// IsValidZipCode reports whether cep is 8 digits (already stripped of "-").
func IsValidZipCode(cep string) bool {
	if len(cep) != ZipCodeDigits {
		return false
	}
	for _, r := range cep {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// NormalizeAddress trims every field and upper-cases the UF, so storage never
// holds the whitespace the form let through. Address is an alias of
// user.Address, so these cannot be methods.
func NormalizeAddress(a *Address) {
	a.ZipCode = strings.TrimSpace(a.ZipCode)
	a.Street = strings.TrimSpace(a.Street)
	a.Number = strings.TrimSpace(a.Number)
	a.Complement = strings.TrimSpace(a.Complement)
	a.District = strings.TrimSpace(a.District)
	a.City = strings.TrimSpace(a.City)
	a.State = strings.ToUpper(strings.TrimSpace(a.State))
}

// ValidateAddress checks the required address fields. Complement stays optional.
func ValidateAddress(a Address) error {
	if !IsValidZipCode(a.ZipCode) || !IsValidState(a.State) {
		return ErrInvalidAddress
	}
	if a.Street == "" || a.Number == "" || a.District == "" || a.City == "" {
		return ErrInvalidAddress
	}
	return nil
}
