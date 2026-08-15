package email

import (
	"strings"
	"testing"
	"time"
)

func TestNewDeviceLoginEmailHTMLIncludesAllFields(t *testing.T) {
	when := time.Date(2026, 8, 15, 14, 30, 0, 0, time.UTC)
	body := newDeviceLoginEmailHTML("Chrome on Mac", "São Paulo", "BR", "203.0.113.5", when)

	for _, want := range []string{"Chrome on Mac", "São Paulo, BR", "203.0.113.5", "15/08/2026 14:30 UTC"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestNewDeviceLoginEmailHTMLFallsBackWhenLocationUnknown(t *testing.T) {
	body := newDeviceLoginEmailHTML("Chrome on Mac", "", "", "203.0.113.5", time.Now())
	if !strings.Contains(body, "localização desconhecida") {
		t.Errorf("expected fallback location text, got:\n%s", body)
	}
}

func TestNewDeviceLoginEmailLayoutRendersFullPage(t *testing.T) {
	page := newDeviceLoginEmailLayout("Maria", "<p>corpo</p>")
	if !strings.Contains(page, "Novo login detectado") || !strings.Contains(page, "Maria") || !strings.Contains(page, "corpo") {
		t.Errorf("layout missing expected content:\n%s", page)
	}
}
