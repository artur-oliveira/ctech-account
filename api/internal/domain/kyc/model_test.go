package kyc

import "testing"

func TestIsValidContentType(t *testing.T) {
	cases := []struct {
		ct   string
		want bool
	}{
		{"image/jpeg", true},
		{"image/png", true},
		{"image/heic", true},
		{"application/pdf", true},
		{"video/webm", false},
		{"video/mp4", false},
		{"text/plain", false},
		{"application/csv", false},
	}
	for _, tc := range cases {
		if got := IsValidContentType(tc.ct); got != tc.want {
			t.Errorf("IsValidContentType(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}

func TestClaimLevel(t *testing.T) {
	cases := []struct {
		level, status, want string
	}{
		{LevelNone, StatusNone, ""},
		{LevelBasic, StatusPending, ""},
		{LevelBasic, StatusVerified, "basic"},
		{LevelEnhanced, StatusPending, "basic"},
		{LevelEnhanced, StatusRejected, "basic"},
		{LevelEnhanced, StatusVerified, "enhanced"},
	}
	for _, tc := range cases {
		if got := ClaimLevel(tc.level, tc.status); got != tc.want {
			t.Errorf("ClaimLevel(%q, %q) = %q, want %q", tc.level, tc.status, got, tc.want)
		}
	}
}

func TestIsValidPhone(t *testing.T) {
	cases := []struct {
		phone string
		want  bool
	}{
		{"+5511987654321", true},
		{"+12025550123", true},
		{"5511987654321", false},  // missing +
		{"+0511987654321", false}, // leading zero after +
		{"not-a-phone", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsValidPhone(tc.phone); got != tc.want {
			t.Errorf("IsValidPhone(%q) = %v, want %v", tc.phone, got, tc.want)
		}
	}
}

func TestMaskPhone(t *testing.T) {
	if got := MaskPhone("+5511987654321"); got != "••••-4321" {
		t.Errorf("MaskPhone = %q, want •••-4321", got)
	}
	if got := MaskPhone(""); got != "" {
		t.Errorf("MaskPhone(empty) = %q, want empty", got)
	}
}
