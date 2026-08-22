package email

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sestypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"

	"gopkg.aoctech.app/api-commons/awsconfig"
)

// sesAPI is the subset of *sesv2.Client this package calls — narrowed to an
// interface so tests can substitute a fake and capture what would have been
// sent, without hitting real AWS.
type sesAPI interface {
	SendEmail(ctx context.Context, params *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

type Client struct {
	ses     sesAPI
	from    string
	baseURL string
}

func New(ctx context.Context, region, from, baseURL string) (*Client, error) {
	cfg, err := awsconfig.Load(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return &Client{
		ses:     sesv2.NewFromConfig(cfg),
		from:    from,
		baseURL: baseURL,
	}, nil
}

func (c *Client) SendVerificationEmail(ctx context.Context, to, firstName, token string) error {
	link := c.baseURL + "/verify-email?token=" + token
	subject := "Verifique seu e-mail — ctech"
	body := verificationEmailHTML(firstName, link)
	return c.send(ctx, to, subject, body)
}

func (c *Client) SendPasswordResetEmail(ctx context.Context, to, firstName, token string) error {
	link := c.baseURL + "/reset-password?token=" + token
	subject := "Redefinir sua senha — ctech"
	body := passwordResetEmailHTML(firstName, link)
	return c.send(ctx, to, subject, body)
}

// SendAccountExistsEmail is sent when someone tries to register with an address
// that already has an account. The registration endpoint responds identically
// whether or not the address exists, so this email is the only signal — and it
// reaches the address owner, not the person who submitted the form.
func (c *Client) SendAccountExistsEmail(ctx context.Context, to, firstName string) error {
	subject := "Você já tem uma conta — ctech"
	body := accountExistsEmailHTML(firstName, c.baseURL+"/login", c.baseURL+"/forgot-password")
	return c.send(ctx, to, subject, body)
}

// SendNewDeviceLoginEmail notifies the user of a successful login from a
// device/country combination not seen on any of their prior sessions.
func (c *Client) SendNewDeviceLoginEmail(ctx context.Context, to, firstName, deviceName, city, country, ip string, when time.Time) error {
	subject := "Novo login detectado — ctech"
	body := newDeviceLoginEmailHTML(deviceName, city, country, ip, when, c.baseURL+"/account/sessions")
	return c.send(ctx, to, subject, newDeviceLoginEmailLayout(firstName, body))
}

// sendRaw builds a full RFC 5322 message and sends it via SESv2's raw
// content mode, which is the only way to set custom headers (In-Reply-To,
// References) — the existing send() helper uses Simple content and can't
// express threading. Returns the assigned Message-ID (no angle brackets).
func (c *Client) sendRaw(ctx context.Context, to, subject, htmlBody, inReplyTo, references string) (string, error) {
	// Defense in depth: every header value must be a single line. Callers
	// are expected to have already rejected newlines further upstream (e.g.
	// validate.Freetext for user-submitted subject text), but a header
	// value that reaches this far with a \r or \n would let its source
	// inject arbitrary additional headers into the raw MIME message.
	for _, header := range []string{to, subject, inReplyTo, references} {
		if strings.ContainsAny(header, "\r\n") {
			return "", errors.New("email: header value must not contain line breaks")
		}
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", c.from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	if inReplyTo != "" {
		fmt.Fprintf(&buf, "In-Reply-To: %s\r\n", inReplyTo)
	}
	if references != "" {
		fmt.Fprintf(&buf, "References: %s\r\n", references)
	}
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	buf.WriteString(htmlBody)

	in := sesv2.SendEmailInput{
		Content: &sestypes.EmailContent{
			Raw: &sestypes.RawMessage{Data: buf.Bytes()},
		},
	}
	out, err := c.ses.SendEmail(ctx, &in)
	if err != nil {
		return "", err
	}
	return aws.ToString(out.MessageId), nil
}

// SendTicketConfirmationEmail is the first e-mail for a new support ticket —
// no In-Reply-To, since it establishes the thread root. Returns the
// Message-ID the caller persists as both root_ses_message_id and
// last_ses_message_id.
func (c *Client) SendTicketConfirmationEmail(ctx context.Context, to string, ticketNumber int64, subjectLine, portalLink string) (string, error) {
	subject := fmt.Sprintf("[Ticket #%d] %s", ticketNumber, subjectLine)
	body := fmt.Sprintf(`<h2>Seu ticket #%d foi criado</h2>
  <p>Um agente em breve entrará em contato para responder sua dúvida.</p>
  <p><strong>Assunto:</strong> %s</p>
  %s`, ticketNumber, html.EscapeString(subjectLine), ctaButton("Acompanhar ticket", portalLink))
	return c.sendRaw(ctx, to, subject, body, "", "")
}

// SendTicketReplyEmail sends an agent's reply, threaded via inReplyTo/references
// onto the prior message in the ticket (root or previous reply).
func (c *Client) SendTicketReplyEmail(ctx context.Context, to string, ticketNumber int64, subjectLine, agentBody, inReplyTo, references, portalLink string) (string, error) {
	subject := fmt.Sprintf("Re: [Ticket #%d] %s", ticketNumber, subjectLine)
	body := fmt.Sprintf(`<p>%s</p>
  %s`, html.EscapeString(agentBody), ctaButton("Acompanhar ticket", portalLink))
	return c.sendRaw(ctx, to, subject, body, inReplyTo, references)
}

// SendTicketNPSEmail is sent once when a ticket closes, threaded the same way.
func (c *Client) SendTicketNPSEmail(ctx context.Context, to string, ticketNumber int64, npsLink, inReplyTo, references string) (string, error) {
	subject := fmt.Sprintf("Re: [Ticket #%d] Como foi seu atendimento?", ticketNumber)
	body := fmt.Sprintf(`<p>Seu ticket foi encerrado. Conta pra gente como foi o atendimento:</p>
  %s`, ctaButton("Avaliar atendimento", npsLink))
	return c.sendRaw(ctx, to, subject, body, inReplyTo, references)
}

func (c *Client) send(ctx context.Context, to, subject, htmlBody string) error {
	in := sesv2.SendEmailInput{
		FromEmailAddress: aws.String(c.from),
		Destination: &sestypes.Destination{
			ToAddresses: []string{to},
		},
		Content: &sestypes.EmailContent{
			Simple: &sestypes.Message{
				Subject: &sestypes.Content{Data: aws.String(subject)},
				Body: &sestypes.Body{
					Html: &sestypes.Content{Data: aws.String(htmlBody)},
				},
			},
		},
	}
	_, err := c.ses.SendEmail(ctx, &in)
	return err
}

// emailLayout renders the shared transactional email shell. body is raw HTML
// placed between the greeting and the footer note.
func emailLayout(heading, firstName, bodyHTML, footerNote string) string {
	name := firstName
	if name == "" {
		name = "Usuário"
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="pt-BR"><head><meta charset="UTF-8"></head>
<body style="font-family:sans-serif;max-width:520px;margin:0 auto;padding:32px 16px;color:#1a1a1a">
  <h2 style="margin-bottom:8px">%s</h2>
  <p>Olá, %s!</p>
  %s
  <p style="font-size:13px;color:#666">%s</p>
</body></html>`, heading, name, bodyHTML, footerNote)
}

// ctaButton renders the shared call-to-action button plus its plain-text fallback link.
func ctaButton(label, link string) string {
	return fmt.Sprintf(
		`<a href="%s" style="display:inline-block;margin:24px 0;padding:12px 28px;background:#2563eb;color:#fff;text-decoration:none;border-radius:6px;font-weight:600">%s</a>
  <p style="font-size:12px;color:#999">Link direto: %s</p>`, link, label, link)
}

func verificationEmailHTML(firstName, link string) string {
	body := `<p>Clique no botão abaixo para verificar seu endereço de e-mail. O link expira em 24 horas.</p>
  ` + ctaButton("Verificar e-mail", link)
	return emailLayout("Verifique seu e-mail", firstName, body,
		"Se você não criou uma conta, ignore este e-mail.")
}

func passwordResetEmailHTML(firstName, link string) string {
	body := `<p>Recebemos uma solicitação para redefinir sua senha. Clique no botão abaixo. O link expira em 15 minutos.</p>
  ` + ctaButton("Redefinir senha", link)
	return emailLayout("Redefinir senha", firstName, body,
		"Se você não solicitou isso, ignore este e-mail — sua senha não será alterada.")
}

// detailRow renders one label/value line of the device-details card. label is
// omitted from the top border so the first row sits flush with the card edge.
func detailRow(label, value string, first bool) string {
	border := "border-top:1px solid #e5e7eb;"
	if first {
		border = ""
	}
	return fmt.Sprintf(
		`<tr><td style="padding:12px 16px;color:#666;font-size:13px;width:120px;%s">%s</td><td style="padding:12px 16px;font-weight:600;%s">%s</td></tr>`,
		border, label, border, value)
}

func newDeviceLoginEmailHTML(deviceName, city, country, ip string, when time.Time, sessionsLink string) string {
	location := city
	if country != "" {
		if location != "" {
			location += ", "
		}
		location += country
	}
	if location == "" {
		location = "localização desconhecida"
	}
	// deviceName is derived from the request's User-Agent header (parseDeviceName
	// falls back to the raw, attacker-controlled value for unrecognized clients)
	// and ip from a client-supplied header — both must be HTML-escaped before
	// interpolation. city/country come from the GeoIP database, not the request,
	// but are escaped too for defense in depth.
	details := `<table role="presentation" style="width:100%;border-collapse:collapse;margin:20px 0;background:#f8fafc;border-radius:8px;overflow:hidden">` +
		detailRow("Dispositivo", html.EscapeString(deviceName), true) +
		detailRow("Local", html.EscapeString(location), false) +
		detailRow("IP", html.EscapeString(ip), false) +
		detailRow("Quando", when.UTC().Format("02/01/2006 15:04 MST"), false) +
		`</table>`

	return fmt.Sprintf(`<p>Detectamos um login na sua conta a partir de um dispositivo ou local que não reconhecemos:</p>
  %s
  %s
  <p>Se foi você, pode ignorar este e-mail. Se não reconhece este acesso, redefina sua senha imediatamente e revise seus dispositivos conectados.</p>`,
		details, ctaButton("Revisar sessões", sessionsLink))
}

func newDeviceLoginEmailLayout(firstName, bodyHTML string) string {
	return emailLayout("Novo login detectado", firstName, bodyHTML,
		"Se não foi você, redefina sua senha e revise os dispositivos conectados em sua conta.")
}

func accountExistsEmailHTML(firstName, loginLink, resetLink string) string {
	body := fmt.Sprintf(`<p>Alguém tentou criar uma conta com este endereço de e-mail, mas você já tem uma conta conosco.</p>
  <p>Se foi você, faça login normalmente. Se esqueceu sua senha, redefina-a.</p>
  %s
  <p style="font-size:13px;color:#666">Esqueceu a senha? <a href="%s">Redefinir senha</a></p>`,
		ctaButton("Fazer login", loginLink), resetLink)
	return emailLayout("Você já tem uma conta", firstName, body,
		"Se não foi você, ignore este e-mail — nenhuma conta nova foi criada e nada mudou.")
}
