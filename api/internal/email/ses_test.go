package email

import (
	"strings"
	"testing"
	"time"
)

func TestNewDeviceLoginEmailHTMLIncludesAllFields(t *testing.T) {
	when := time.Date(2026, 8, 15, 14, 30, 0, 0, time.UTC)
	body := newDeviceLoginEmailHTML("Chrome on Mac", "São Paulo", "BR", "203.0.113.5", when, "https://accounts.aoctech.app/account/sessions")

	for _, want := range []string{"Chrome on Mac", "São Paulo, BR", "203.0.113.5", "15/08/2026 14:30 UTC", "https://accounts.aoctech.app/account/sessions", "Revisar sessões"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestNewDeviceLoginEmailHTMLFallsBackWhenLocationUnknown(t *testing.T) {
	body := newDeviceLoginEmailHTML("Chrome on Mac", "", "", "203.0.113.5", time.Now(), "https://accounts.aoctech.app/account/sessions")
	if !strings.Contains(body, "localização desconhecida") {
		t.Errorf("expected fallback location text, got:\n%s", body)
	}
}

// Regression test: deviceName comes from the request's User-Agent header
// (parseDeviceName falls back to the raw value for unrecognized clients), so
// it must never be interpolated unescaped into the email HTML.
func TestNewDeviceLoginEmailHTMLEscapesDeviceName(t *testing.T) {
	body := newDeviceLoginEmailHTML(`<script>alert(1)</script>`, "São Paulo", "BR", "203.0.113.5", time.Now(), "https://accounts.aoctech.app/account/sessions")
	if strings.Contains(body, "<script>") {
		t.Errorf("deviceName must be HTML-escaped, got:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected escaped deviceName in body:\n%s", body)
	}
}

func TestNewDeviceLoginEmailLayoutRendersFullPage(t *testing.T) {
	page := newDeviceLoginEmailLayout("Maria", "<p>corpo</p>")
	if !strings.Contains(page, "Novo login detectado") || !strings.Contains(page, "Maria") || !strings.Contains(page, "corpo") {
		t.Errorf("layout missing expected content:\n%s", page)
	}
}
