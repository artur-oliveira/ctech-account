package support

import (
	"context"
	"testing"
)

// newTestService is local to this file — an in-memory Repository good enough
// for service-level tests without pulling in the handler package's mock.
type memRepo struct {
	tickets  map[string]*Ticket
	messages map[string][]*Message
	counter  int64
}

func newMemRepo() *memRepo {
	return &memRepo{tickets: map[string]*Ticket{}, messages: map[string][]*Message{}}
}

func (m *memRepo) NextTicketNumber(ctx context.Context) (int64, error) {
	m.counter++
	return m.counter, nil
}
func (m *memRepo) CreateTicket(ctx context.Context, t *Ticket) error {
	t.SK = metaSK
	m.tickets[t.ID()] = t
	return nil
}
func (m *memRepo) GetTicket(ctx context.Context, id string) (*Ticket, error) {
	t, ok := m.tickets[id]
	if !ok {
		return nil, ErrNotFound
	}
	return t, nil
}
func (m *memRepo) GetTicketByAnonToken(ctx context.Context, token string) (*Ticket, error) {
	for _, t := range m.tickets {
		if t.AnonymousToken == token && token != "" {
			return t, nil
		}
	}
	return nil, ErrNotFound
}
func (m *memRepo) GetTicketByNumber(ctx context.Context, number int64) (*Ticket, error) {
	for _, t := range m.tickets {
		if t.TicketNumber == number {
			return t, nil
		}
	}
	return nil, ErrNotFound
}
func (m *memRepo) UpdateTicket(ctx context.Context, id string, updates map[string]any) error {
	t, ok := m.tickets[id]
	if !ok {
		return ErrNotFound
	}
	for k, v := range updates {
		switch k {
		case "status":
			t.Status = v.(string)
		case "updated_at":
			t.UpdatedAt = v.(string)
		case "closed_at":
			t.ClosedAt = v.(string)
		case "last_message_at":
			t.LastMessageAt = v.(string)
		case "last_ses_message_id":
			t.LastSESMessageID = v.(string)
		case "root_ses_message_id":
			t.RootSESMessageID = v.(string)
		case "nps_score":
			t.NPSScore = v.(int)
		case "nps_message":
			t.NPSMessage = v.(string)
		case "nps_requested_at":
			t.NPSRequestedAt = v.(string)
		}
	}
	return nil
}
func (m *memRepo) PutMessage(ctx context.Context, msg *Message) error {
	ticketID := msg.PK
	msg.PK = BuildPK(ticketID)
	msg.SK = BuildMessageSK(msg.CreatedAt)
	m.messages[ticketID] = append(m.messages[ticketID], msg)
	return nil
}
func (m *memRepo) ListMessages(ctx context.Context, ticketID string) ([]*Message, error) {
	return m.messages[ticketID], nil
}
func (m *memRepo) ListByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Ticket, string, error) {
	var out []*Ticket
	for _, t := range m.tickets {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	return out, "", nil
}
func (m *memRepo) ListByStatus(ctx context.Context, status, cursor string, limit int32) ([]*Ticket, string, error) {
	var out []*Ticket
	for _, t := range m.tickets {
		if t.Status == status {
			out = append(out, t)
		}
	}
	return out, "", nil
}

func TestCreateTicket_Authenticated(t *testing.T) {
	svc := NewService(newMemRepo())
	ticket, err := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow,
		Body: "Não consigo trocar minha senha pelo app novo.",
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if ticket.TicketNumber != 1 {
		t.Fatalf("got ticket number %d, want 1", ticket.TicketNumber)
	}
	if ticket.Status != StatusOpen {
		t.Fatalf("got status %q, want %q", ticket.Status, StatusOpen)
	}
	if ticket.IsAnonymous() {
		t.Fatalf("ticket should not be anonymous")
	}
}

func TestCreateTicket_AnonymousRequiresEmail(t *testing.T) {
	svc := NewService(newMemRepo())
	_, err := svc.CreateTicket(context.Background(), CreateTicketInput{
		SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Não consigo acessar minha conta de jeito nenhum.",
	})
	if err == nil {
		t.Fatal("expected error for anonymous ticket without email")
	}
}

func TestCreateTicket_RejectsJunkBody(t *testing.T) {
	svc := NewService(newMemRepo())
	_, err := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "..........",
	})
	if err == nil {
		t.Fatal("expected junk-pattern rejection")
	}
}

func TestCreateTicket_OtherCategoryRequiresSubjectOther(t *testing.T) {
	svc := NewService(newMemRepo())
	_, err := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryOther, Priority: PriorityLow, Body: "Preciso de ajuda com um assunto diferente.",
	})
	if err == nil {
		t.Fatal("expected error when category=other has no subject_other")
	}
}

func TestReplyAsAgent_SetsAnsweredAndThreadsMessageID(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Meu problema de login persiste desde ontem.",
	})

	msg, updated, err := svc.ReplyAsAgent(context.Background(), ticket.ID(), "agent-1", "Já verificamos, pode tentar novamente agora.")
	if err != nil {
		t.Fatalf("ReplyAsAgent: %v", err)
	}
	if msg.AuthorType != AuthorAgent {
		t.Fatalf("got author_type %q, want %q", msg.AuthorType, AuthorAgent)
	}
	if updated.Status != StatusAnswered {
		t.Fatalf("got status %q, want %q", updated.Status, StatusAnswered)
	}
}

func TestReplyAsUser_ReopensClosedTicket(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Preciso de suporte com minha conta principal.",
	})
	if err := svc.SetStatus(context.Background(), ticket.ID(), StatusClosed); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	if err := svc.ReplyAsUser(context.Background(), ticket.ID(), "user-1", "", "Voltou a acontecer o mesmo problema de antes."); err != nil {
		t.Fatalf("ReplyAsUser: %v", err)
	}

	got, err := repo.GetTicket(context.Background(), ticket.ID())
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got.Status != StatusOpen {
		t.Fatalf("got status %q after reply on closed ticket, want %q (reopen)", got.Status, StatusOpen)
	}
}

func TestReplyAsUser_ForbiddenForWrongOwner(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Ticket original do usuário correto aqui.",
	})
	err := svc.ReplyAsUser(context.Background(), ticket.ID(), "user-2", "", "Tentando responder um ticket que não é meu.")
	if err != ErrForbidden {
		t.Fatalf("got %v, want ErrForbidden", err)
	}
}

func TestSubmitNPS_RequiresMessageWhenScoreLow(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Preciso de ajuda urgente com pagamento.",
	})
	svc.SetStatus(context.Background(), ticket.ID(), StatusClosed)

	if err := svc.SubmitNPS(context.Background(), ticket.ID(), "user-1", "", 2, ""); err != ErrInvalidNPS {
		t.Fatalf("got %v, want ErrInvalidNPS for low score with no message", err)
	}
	if err := svc.SubmitNPS(context.Background(), ticket.ID(), "user-1", "", 2, "Demorou muito e não resolveu meu problema real."); err != nil {
		t.Fatalf("SubmitNPS with message: %v", err)
	}
}

func TestSubmitNPS_HighScoreMessageOptional(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Preciso de ajuda com meu cadastro atual.",
	})
	svc.SetStatus(context.Background(), ticket.ID(), StatusClosed)

	if err := svc.SubmitNPS(context.Background(), ticket.ID(), "user-1", "", 5, ""); err != nil {
		t.Fatalf("SubmitNPS: %v", err)
	}
}

func TestSubmitNPS_RejectsBeforeClosed(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Ticket ainda aberto, sem resposta do agente.",
	})
	if err := svc.SubmitNPS(context.Background(), ticket.ID(), "user-1", "", 5, ""); err != ErrInvalidNPS {
		t.Fatalf("got %v, want ErrInvalidNPS before ticket is closed", err)
	}
}

func TestReplyAsAgent_ThreadIDChaining(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Problema recorrente com meu extrato de conta.",
	})
	if err := repo.UpdateTicket(context.Background(), ticket.ID(), map[string]any{"root_ses_message_id": "<root@ses>", "last_ses_message_id": "<root@ses>"}); err != nil {
		t.Fatalf("seeding root message id: %v", err)
	}

	_, updated1, err := svc.ReplyAsAgent(context.Background(), ticket.ID(), "agent-1", "Primeira resposta do agente.")
	if err != nil {
		t.Fatalf("first reply: %v", err)
	}
	if updated1.RootSESMessageID != "<root@ses>" {
		t.Fatalf("root message id should not change: got %q", updated1.RootSESMessageID)
	}
}

func TestRecordEmailMessageID_WrapsInAngleBrackets(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Preciso de ajuda com o extrato da minha conta.",
	})

	if err := svc.RecordEmailMessageID(context.Background(), ticket.ID(), "abc123@ses.amazonaws.com", true); err != nil {
		t.Fatalf("RecordEmailMessageID: %v", err)
	}
	got, err := repo.GetTicket(context.Background(), ticket.ID())
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got.RootSESMessageID != "<abc123@ses.amazonaws.com>" {
		t.Fatalf("got root_ses_message_id %q, want bracketed", got.RootSESMessageID)
	}
	if got.LastSESMessageID != "<abc123@ses.amazonaws.com>" {
		t.Fatalf("got last_ses_message_id %q, want bracketed", got.LastSESMessageID)
	}
}

func TestMarkNPSRequested(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Ticket que será fechado para testar o NPS.",
	})

	if err := svc.MarkNPSRequested(context.Background(), ticket.ID()); err != nil {
		t.Fatalf("MarkNPSRequested: %v", err)
	}
	got, err := repo.GetTicket(context.Background(), ticket.ID())
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got.NPSRequestedAt == "" {
		t.Fatal("expected nps_requested_at to be set")
	}
}

func TestCreateTicket_AcceptsSubjectOtherForNonOtherCategory(t *testing.T) {
	svc := NewService(newMemRepo())
	ticket, err := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, SubjectOther: "Conta e login — Esqueci minha senha", Priority: PriorityLow,
		Body: "Não consigo redefinir minha senha pelo link enviado.",
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if ticket.SubjectOther != "Conta e login — Esqueci minha senha" {
		t.Fatalf("got subject_other %q, want the merged category/subcategory label preserved", ticket.SubjectOther)
	}
}
