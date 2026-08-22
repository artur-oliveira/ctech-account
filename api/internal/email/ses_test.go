package email

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

// fakeSES implements sesAPI, capturing the raw MIME bytes of the last send
// instead of hitting real AWS.
type fakeSES struct {
	lastInput *sesv2.SendEmailInput
}

func (f *fakeSES) SendEmail(_ context.Context, params *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	f.lastInput = params
	return &sesv2.SendEmailOutput{MessageId: aws.String("test-message-id@ses.amazonaws.com")}, nil
}

func (f *fakeSES) lastRaw() string {
	if f.lastInput == nil || f.lastInput.Content == nil || f.lastInput.Content.Raw == nil {
		return ""
	}
	return string(f.lastInput.Content.Raw.Data)
}

// newTestClientWithCapture returns a Client backed by a fakeSES, plus the
// fake itself so tests can inspect what was sent.
func newTestClientWithCapture(t *testing.T) (*Client, *fakeSES) {
	t.Helper()
	fake := &fakeSES{}
	cli := &Client{ses: fake, from: "support@aoctech.app", baseURL: "https://accounts.aoctech.app"}
	return cli, fake
}

func TestSendTicketConfirmationEmail_ContainsPortalLink(t *testing.T) {
	cli, sent := newTestClientWithCapture(t)
	_, err := cli.SendTicketConfirmationEmail(context.Background(), "user@example.com", 42, "Conta / Login", "https://accounts.aoctech.app/support/ticket/abc?token=xyz")
	if err != nil {
		t.Fatalf("SendTicketConfirmationEmail: %v", err)
	}
	raw := sent.lastRaw()
	if !strings.Contains(raw, "https://accounts.aoctech.app/support/ticket/abc?token=xyz") {
		t.Fatal("expected portal link in raw message")
	}
	if !strings.Contains(raw, "Ticket #42") {
		t.Fatal("expected ticket number in subject/body")
	}
	if strings.Contains(raw, "In-Reply-To:") {
		t.Fatal("confirmation email must not have In-Reply-To — it's the thread root")
	}
}

func TestSendTicketReplyEmail_SetsThreadingHeaders(t *testing.T) {
	cli, sent := newTestClientWithCapture(t)
	_, err := cli.SendTicketReplyEmail(context.Background(), "user@example.com", 42, "Conta / Login", "Resolvido, pode conferir.", "<root@ses.amazonaws.com>", "<root@ses.amazonaws.com>", "https://accounts.aoctech.app/support/ticket/abc")
	if err != nil {
		t.Fatalf("SendTicketReplyEmail: %v", err)
	}
	raw := sent.lastRaw()
	if !strings.Contains(raw, "In-Reply-To: <root@ses.amazonaws.com>") {
		t.Fatal("expected In-Reply-To header")
	}
	if !strings.Contains(raw, "References: <root@ses.amazonaws.com>") {
		t.Fatal("expected References header")
	}
	if !strings.Contains(raw, "Subject: Re: [Ticket #42] Conta / Login") {
		t.Fatal("expected Re: subject with ticket number")
	}
}

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
