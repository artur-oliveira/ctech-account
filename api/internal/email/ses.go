package email

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sestypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"

	"gopkg.aoctech.app/api-commons/awsconfig"
)

type Client struct {
	ses     *sesv2.Client
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

func accountExistsEmailHTML(firstName, loginLink, resetLink string) string {
	body := fmt.Sprintf(`<p>Alguém tentou criar uma conta com este endereço de e-mail, mas você já tem uma conta conosco.</p>
  <p>Se foi você, faça login normalmente. Se esqueceu sua senha, redefina-a.</p>
  %s
  <p style="font-size:13px;color:#666">Esqueceu a senha? <a href="%s">Redefinir senha</a></p>`,
		ctaButton("Fazer login", loginLink), resetLink)
	return emailLayout("Você já tem uma conta", firstName, body,
		"Se não foi você, ignore este e-mail — nenhuma conta nova foi criada e nada mudou.")
}
