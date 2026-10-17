package email

import (
	"strings"
	"testing"
	"time"
)

func TestServiceEmailsNameTheProductAndSayTheAccountStays(t *testing.T) {
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	for name, got := range map[string]string{
		"confirm":   serviceConfirmEmailHTML("Ana", "https://x/confirm", "poker"),
		"scheduled": serviceScheduledEmailHTML("Ana", "https://x/cancel", at, "poker"),
		"reminder":  serviceReminderEmailHTML("Ana", "https://x/cancel", at, "poker"),
		"blocked":   serviceBlockedEmailHTML("Ana", "https://x/cancel", []string{"poker.chips_held"}, "poker"),
		"cancelled": serviceCancelledEmailHTML("Ana", "poker"),
		"left":      serviceUnlinkedEmailHTML("Ana", "poker"),
	} {
		if !strings.Contains(got, "CTech Poker") {
			t.Errorf("%s: does not name the product", name)
		}
		if strings.Contains(got, "excluir sua conta") || strings.Contains(got, "excluída definitivamente") {
			t.Errorf("%s: reads like an account deletion", name)
		}
		if name != "blocked" && !strings.Contains(got, "continuam funcionando") {
			t.Errorf("%s: does not say the account stays", name)
		}
	}
}

func TestAnUnlistedProductIsEscaped(t *testing.T) {
	if got := productName(`x<script>`); strings.Contains(got, "<script>") {
		t.Fatalf("productName = %q, want escaped", got)
	}
}

func TestServiceLinksNameTheProduct(t *testing.T) {
	c := &Client{baseURL: "https://accounts.example"}
	got := c.serviceDeletionLink("confirm", "r1", "tok", "poker")
	if got != "https://accounts.example/account-deletion/confirm?request=r1&token=tok&service=poker" {
		t.Fatalf("link = %s", got)
	}
}
