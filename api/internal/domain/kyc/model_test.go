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
		{"video/webm", true},
		{"video/mp4", true},
		{"video/webm;codecs=vp8", true},
		{"text/plain", false},
		{"application/csv", false},
	}
	for _, tc := range cases {
		if got := IsValidContentType(tc.ct); got != tc.want {
			t.Errorf("IsValidContentType(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}
