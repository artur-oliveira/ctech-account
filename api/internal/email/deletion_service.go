package email

import (
	"html"
	"time"
)

// productNames are the display names of the CTech products a person can leave
// on their own (service unlink). An id that is not listed reads "CTech <id>".
var productNames = map[string]string{
	"wallet":  "CTech Ledger",
	"billing": "CTech Billing",
	"dfe":     "CTech DF-e",
	"poker":   "CTech Poker",
}

func productName(id string) string {
	if n, ok := productNames[id]; ok {
		return n
	}
	return "CTech " + html.EscapeString(id)
}

const accountStays = `<p>Sua conta CTech e os outros produtos continuam funcionando normalmente.</p>`

func serviceConfirmEmailHTML(firstName, link, product string) string {
	p := productName(product)
	body := `<p>Recebemos um pedido para <strong>você sair do ` + p + `</strong>: seus dados nesse produto serão apagados ou anonimizados, salvo o que a lei exige guardar. Confirme pelo botão abaixo. O link expira em 24 horas.</p>
  ` + accountStays + `
  <p>Ao confirmar, o acesso ao ` + p + ` é bloqueado na hora e a saída é concluída em 7 dias. Até lá você pode cancelar pelo link que enviaremos.</p>
  ` + ctaButton("Confirmar saída", link)
	return emailLayout("Confirme a saída do "+p, firstName, body,
		"Se não foi você, ignore este e-mail: nada acontece sem a confirmação. Recomendamos trocar sua senha.")
}

func serviceScheduledEmailHTML(firstName, cancelLink string, graceUntil time.Time, product string) string {
	p := productName(product)
	body := `<p>O seu acesso ao ` + p + ` está bloqueado e a saída será <strong>concluída em ` +
		graceUntil.In(brt).Format("02/01/2006 às 15:04") + `</strong> (horário de Brasília).</p>
  ` + accountStays + `
  <p>Depois disso os dados do produto não podem ser recuperados. Para continuar no ` + p + `, cancele antes desse prazo.</p>
  ` + ctaButton("Cancelar saída", cancelLink)
	return emailLayout("Saída do "+p+" agendada", firstName, body,
		"Se você não pediu para sair, cancele agora e troque sua senha.")
}

func serviceReminderEmailHTML(firstName, cancelLink string, graceUntil time.Time, product string) string {
	p := productName(product)
	body := `<p>Sua saída do ` + p + ` será <strong>concluída em ` +
		graceUntil.In(brt).Format("02/01/2006 às 15:04") + `</strong> (horário de Brasília). Esta é a última chance de cancelar.</p>
  ` + accountStays + `
  ` + ctaButton("Cancelar saída", cancelLink)
	return emailLayout("Último aviso: saída do "+p, firstName, body,
		"Se você quer mesmo sair, não precisa fazer nada.")
}

func serviceBlockedEmailHTML(firstName, cancelLink string, blockers []string, product string) string {
	p := productName(product)
	body := `<p>O prazo para a sua saída do ` + p + ` terminou, mas ela não pôde ser concluída:</p>
  <ul>` + blockerItems(blockers) + `</ul>
  <p>O seu acesso ao ` + p + ` continua bloqueado. Cancele a saída, resolva os itens acima e faça um novo pedido. ` + `Sua conta CTech não foi afetada.</p>
  ` + ctaButton("Cancelar saída", cancelLink)
	return emailLayout("Saída pendente", firstName, body,
		"Se precisar de ajuda, responda a este e-mail ou abra um chamado no suporte.")
}

func serviceCancelledEmailHTML(firstName, product string) string {
	p := productName(product)
	body := `<p>A sua saída do ` + p + ` foi cancelada. Entre novamente no produto para voltar a usá-lo.</p>` + accountStays
	return emailLayout("Saída cancelada", firstName, body,
		"Se não foi você quem cancelou, fale com o suporte.")
}

func serviceUnlinkedEmailHTML(firstName, product string) string {
	p := productName(product)
	body := `<p>Você saiu do <strong>` + p + `</strong>: seus dados nesse produto foram apagados ou anonimizados, salvo o que a lei exige guardar.</p>
  ` + accountStays + `
  <p>Se quiser voltar um dia, basta entrar no produto: você começa uma conta nova nele.</p>`
	return emailLayout("Você saiu do "+p, firstName, body,
		"Se não foi você, fale com o suporte.")
}
