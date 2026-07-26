package kyc

import "regexp"

// e164Pattern matches E.164: a leading '+' followed by 8-15 digits, the first
// non-zero (ITU-T E.164 recommendation).
var e164Pattern = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)

// IsValidPhone reports whether phone is a plausible E.164 number. The DTO
// layer also validates with go-playground/validator's "e164" tag — this is
// the domain-layer check, same defense-in-depth pattern as IsValidCPF.
func IsValidPhone(phone string) bool {
	return e164Pattern.MatchString(phone)
}

// MaskPhone renders a phone as ***1234 (last 4 digits visible), mirroring
// MaskCPF's style.
func MaskPhone(phone string) string {
	if len(phone) < 4 {
		return ""
	}
	return "***" + phone[len(phone)-4:]
}
