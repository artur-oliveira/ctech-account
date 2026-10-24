package kyc

import "testing"

func TestMaskCPF(t *testing.T) {
	if got := MaskCPF("52998224725"); got != "529.•••.•••-25" {
		t.Errorf("MaskCPF = %q", got)
	}
	if got := MaskCPF(""); got != "" {
		t.Errorf("MaskCPF empty = %q", got)
	}
}
