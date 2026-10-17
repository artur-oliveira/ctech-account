package email

import "context"

// The export e-mails carry no download link (spec E6): they send the person to
// the signed-in page, so a leaked mailbox is not a leaked copy of the data.

func (c *Client) SendExportRequested(ctx context.Context, to, firstName string) error {
	body := `<p>Recebemos o seu pedido de <strong>exportação dos dados pessoais</strong>. Avisaremos por e-mail quando a cópia estiver pronta.</p>` +
		ctaButton("Ver minha exportação", c.baseURL+"/account/export")
	return c.send(ctx, to, "Exportação dos seus dados solicitada — ctech",
		emailLayout("Exportação solicitada", firstName, body, "Se não foi você, troque sua senha e fale com o suporte."))
}

func (c *Client) SendExportReady(ctx context.Context, to, firstName string) error {
	body := `<p>A cópia dos seus dados está pronta e fica disponível por <strong>7 dias</strong>. Por segurança, o download só é liberado com a sua conta conectada.</p>` +
		ctaButton("Baixar meus dados", c.baseURL+"/account/export")
	return c.send(ctx, to, "Sua exportação de dados está pronta — ctech",
		emailLayout("Exportação pronta", firstName, body, "Se não foi você quem pediu, troque sua senha e fale com o suporte."))
}
