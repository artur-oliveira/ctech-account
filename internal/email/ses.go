package email

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sestypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

type Client struct {
	ses     *sesv2.Client
	from    string
	baseURL string
}

func New(ctx context.Context, region, from, baseURL string) (*Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
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

func (c *Client) send(ctx context.Context, to, subject, htmlBody string) error {
	_, err := c.ses.SendEmail(ctx, &sesv2.SendEmailInput{
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
	})
	return err
}

func verificationEmailHTML(firstName, link string) string {
	name := firstName
	if name == "" {
		name = "Usuário"
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="pt-BR"><head><meta charset="UTF-8"></head>
<body style="font-family:sans-serif;max-width:520px;margin:0 auto;padding:32px 16px;color:#1a1a1a">
  <h2 style="margin-bottom:8px">Verifique seu e-mail</h2>
  <p>Olá, %s!</p>
  <p>Clique no botão abaixo para verificar seu endereço de e-mail. O link expira em 24 horas.</p>
  <a href="%s" style="display:inline-block;margin:24px 0;padding:12px 28px;background:#2563eb;color:#fff;text-decoration:none;border-radius:6px;font-weight:600">Verificar e-mail</a>
  <p style="font-size:13px;color:#666">Se você não criou uma conta, ignore este e-mail.</p>
  <p style="font-size:12px;color:#999">Link direto: %s</p>
</body></html>`, name, link, link)
}

func passwordResetEmailHTML(firstName, link string) string {
	name := firstName
	if name == "" {
		name = "Usuário"
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="pt-BR"><head><meta charset="UTF-8"></head>
<body style="font-family:sans-serif;max-width:520px;margin:0 auto;padding:32px 16px;color:#1a1a1a">
  <h2 style="margin-bottom:8px">Redefinir senha</h2>
  <p>Olá, %s!</p>
  <p>Recebemos uma solicitação para redefinir sua senha. Clique no botão abaixo. O link expira em 15 minutos.</p>
  <a href="%s" style="display:inline-block;margin:24px 0;padding:12px 28px;background:#2563eb;color:#fff;text-decoration:none;border-radius:6px;font-weight:600">Redefinir senha</a>
  <p style="font-size:13px;color:#666">Se você não solicitou isso, ignore este e-mail — sua senha não será alterada.</p>
  <p style="font-size:12px;color:#999">Link direto: %s</p>
</body></html>`, name, link, link)
}
