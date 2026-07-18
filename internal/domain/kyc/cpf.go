package kyc

// IsValidCPF reports whether cpf is 11 numeric digits with valid check digits.
// Sequences of a single repeated digit (e.g. 11111111111) pass the checksum
// but are not real CPFs, so they are rejected explicitly.
func IsValidCPF(cpf string) bool {
	if len(cpf) != 11 {
		return false
	}
	allSame := true
	for i := 0; i < 11; i++ {
		if cpf[i] < '0' || cpf[i] > '9' {
			return false
		}
		if cpf[i] != cpf[0] {
			allSame = false
		}
	}
	if allSame {
		return false
	}
	return checkDigit(cpf, 9) == int(cpf[9]-'0') && checkDigit(cpf, 10) == int(cpf[10]-'0')
}

// checkDigit computes the CPF verification digit at position pos (9 or 10)
// from the preceding pos digits, per the Receita Federal mod-11 algorithm.
func checkDigit(cpf string, pos int) int {
	sum := 0
	for i := 0; i < pos; i++ {
		sum += int(cpf[i]-'0') * (pos + 1 - i)
	}
	d := 11 - sum%11
	if d >= 10 {
		return 0
	}
	return d
}

// MaskCPF renders a CPF as ***.***.***-XX (last two digits visible).
func MaskCPF(cpf string) string {
	if len(cpf) != 11 {
		return ""
	}
	return "***.***.***-" + cpf[9:]
}
