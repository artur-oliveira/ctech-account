package kyc

import "testing"

func TestIsValidCPF(t *testing.T) {
	cases := []struct {
		cpf  string
		want bool
	}{
		{"52998224725", true},  // canonical valid CPF
		{"11144477735", true},  // valid
		{"52998224724", false}, // wrong check digit
		{"11111111111", false}, // repeated sequence
		{"00000000000", false},
		{"1234567890", false},   // 10 digits
		{"123456789012", false}, // 12 digits
		{"5299822472a", false},  // non-numeric
		{"", false},
	}
	for _, tc := range cases {
		if got := IsValidCPF(tc.cpf); got != tc.want {
			t.Errorf("IsValidCPF(%q) = %v, want %v", tc.cpf, got, tc.want)
		}
	}
}

func TestMaskCPF(t *testing.T) {
	if got := MaskCPF("52998224725"); got != "***.***.***-25" {
		t.Errorf("MaskCPF = %q", got)
	}
	if got := MaskCPF(""); got != "" {
		t.Errorf("MaskCPF empty = %q", got)
	}
}
