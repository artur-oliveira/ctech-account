package support

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gopkg.aoctech.app/account/api/internal/validate"
)

var (
	ErrForbidden    = errors.New("not authorized for this ticket")
	ErrInvalidNPS   = errors.New("invalid NPS submission")
	ErrInvalidInput = errors.New("invalid ticket input")
	ErrTicketClosed = errors.New("closed tickets are immutable")
)

// Freetext bounds, per docs/specs/2026-08-22-support-tickets-design.md §3.5.
var (
	bodyRule = validate.FreetextRule{Min: 15, Max: 4000, AllowNewlines: true}
	// subjectOtherRule stays single-line: it ends up in an e-mail Subject
	// header (ticketSubjectLine in the handler package) as well as list/UI
	// labels, so a newline here would let a submitter inject extra raw MIME
	// headers into the outbound SES message.
	subjectOtherRule = validate.FreetextRule{Min: 3, Max: 120}
	npsMessageRule   = validate.FreetextRule{Min: 15, Max: 1000, AllowNewlines: true}
	internalNoteRule = validate.FreetextRule{Min: 3, Max: 4000, AllowNewlines: true}
)

type Service struct {
	repo     Repository
	notifier Notifier
}

type Event struct{ TicketID, Type, Status, EscalationLevel, AuthorType, Body, CreatedAt string }
type Notifier interface {
	Publish(ctx context.Context, event Event)
	PublishInternal(ctx context.Context, event Event)
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) SetNotifier(notifier Notifier) { s.notifier = notifier }

type CreateTicketInput struct {
	UserID          string // empty for anonymous submissions
	AnonymousEmail  string // required when UserID is empty
	SubjectCategory string
	SubjectOther    string
	Priority        string // defaults to PriorityLow when empty
	Body            string
}

func (s *Service) CreateTicket(ctx context.Context, in CreateTicketInput) (*Ticket, error) {
	if !contains(ValidCategories, in.SubjectCategory) {
		return nil, fmt.Errorf("%w: unknown subject_category %q", ErrInvalidInput, in.SubjectCategory)
	}
	// subject_other doubles as the merged "category — subcategory" label the
	// UI sends for every category, and as the free-typed subject when
	// category=other — so it's validated whenever present, and required only
	// for category=other (the one case with no subcategory catalog to fall
	// back on).
	if in.SubjectOther != "" {
		cleaned, err := validate.Freetext(in.SubjectOther, subjectOtherRule)
		if err != nil {
			return nil, fmt.Errorf("%w: subject_other %v", ErrInvalidInput, err)
		}
		in.SubjectOther = cleaned
	} else if in.SubjectCategory == CategoryOther {
		return nil, fmt.Errorf("%w: subject_other is required for category=other", ErrInvalidInput)
	}
	if in.Priority == "" {
		in.Priority = PriorityLow
	}
	if !contains(ValidPriorities, in.Priority) {
		return nil, fmt.Errorf("%w: unknown priority %q", ErrInvalidInput, in.Priority)
	}
	body, err := validate.Freetext(in.Body, bodyRule)
	if err != nil {
		return nil, fmt.Errorf("%w: body %v", ErrInvalidInput, err)
	}
	if in.UserID == "" && in.AnonymousEmail == "" {
		return nil, fmt.Errorf("%w: anonymous_email is required for anonymous tickets", ErrInvalidInput)
	}

	number, err := s.repo.NextTicketNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("allocating ticket number: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	ticket := &Ticket{
		PK:              BuildPK(uuid.New().String()),
		TicketNumber:    number,
		UserID:          in.UserID,
		AnonymousEmail:  in.AnonymousEmail,
		SubjectCategory: in.SubjectCategory,
		SubjectOther:    in.SubjectOther,
		Priority:        in.Priority,
		Status:          StatusOpen,
		EscalationLevel: EscalationNone,
		CreatedAt:       now,
		UpdatedAt:       now,
		LastMessageAt:   now,
	}
	if ticket.IsAnonymous() {
		ticket.AnonymousToken = uuid.New().String()
	}
	if err := s.repo.CreateTicket(ctx, ticket); err != nil {
		return nil, fmt.Errorf("creating ticket: %w", err)
	}
	if err := s.putSystemMessage(ctx, ticket.ID(), "Ticket criado."); err != nil {
		return nil, err
	}
	if err := s.putMessage(ctx, ticket.ID(), AuthorUser, in.UserID, body); err != nil {
		return nil, err
	}
	return ticket, nil
}

// resolveAccess loads the ticket and checks the caller can see it: either
// userID matches Ticket.UserID, or anonToken matches Ticket.AnonymousToken.
// An empty userID with a matching anonToken is the anonymous-link path.
func (s *Service) resolveAccess(ctx context.Context, id, userID, anonToken string) (*Ticket, error) {
	ticket, err := s.repo.GetTicket(ctx, id)
	if err != nil {
		return nil, err
	}
	if userID != "" && ticket.UserID == userID {
		return ticket, nil
	}
	if anonToken != "" && ticket.AnonymousToken == anonToken {
		return ticket, nil
	}
	return nil, ErrForbidden
}

func (s *Service) GetTicketForCaller(ctx context.Context, id, userID, anonToken string) (*Ticket, []*Message, error) {
	ticket, err := s.resolveAccess(ctx, id, userID, anonToken)
	if err != nil {
		return nil, nil, err
	}
	messages, err := s.repo.ListMessages(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("listing messages: %w", err)
	}
	return ticket, messages, nil
}

// GetTicketAdmin returns a complete thread without an owner check. The caller
// is authorized by middleware.RequireSupportRole before reaching this method.
func (s *Service) GetTicketAdmin(ctx context.Context, id string) (*Ticket, []*Message, error) {
	ticket, err := s.repo.GetTicket(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	messages, err := s.repo.ListMessages(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("listing messages: %w", err)
	}
	return ticket, messages, nil
}

func (s *Service) GetTicketAdminWithNotes(ctx context.Context, id string) (*Ticket, []*Message, []*InternalNote, error) {
	ticket, messages, err := s.GetTicketAdmin(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	notes, err := s.repo.ListInternalNotes(ctx, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("listing internal notes: %w", err)
	}
	return ticket, messages, notes, nil
}

// RecordEmailMessageID persists mail-thread state after SES accepted a send.
// SES returns a bare Message-ID (no angle brackets); RFC 5322's
// In-Reply-To/References headers require the bracketed form, so it's wrapped
// once here rather than at every call site.
func (s *Service) RecordEmailMessageID(ctx context.Context, id, messageID string, root bool) error {
	wrapped := "<" + messageID + ">"
	updates := map[string]any{"last_ses_message_id": wrapped}
	if root {
		updates["root_ses_message_id"] = wrapped
	}
	return s.repo.UpdateTicket(ctx, id, updates)
}

// MarkNPSRequested guards SendTicketNPSEmail against double-sends when a
// ticket is closed more than once.
func (s *Service) MarkNPSRequested(ctx context.Context, id string) error {
	return s.repo.UpdateTicket(ctx, id, map[string]any{"nps_requested_at": time.Now().UTC().Format(time.RFC3339)})
}

func (s *Service) ReplyAsUser(ctx context.Context, id, userID, anonToken, body string) error {
	ticket, err := s.resolveAccess(ctx, id, userID, anonToken)
	if err != nil {
		return err
	}
	if ticket.Status == StatusClosed {
		return ErrTicketClosed
	}
	cleaned, err := validate.Freetext(body, bodyRule)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := s.putMessage(ctx, id, AuthorUser, userID, cleaned); err != nil {
		return err
	}
	return nil
}

// ReplyAsAgent appends the agent's message and sets status=answered. It does
// not send e-mail — the handler does that with the returned Message/Ticket
// and then persists the resulting SES Message-ID via UpdateTicket.
func (s *Service) ReplyAsAgent(ctx context.Context, id, agentUserID, body string) (*Message, *Ticket, error) {
	cleaned, err := validate.Freetext(body, bodyRule)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	ticket, err := s.repo.GetTicket(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if ticket.Status == StatusClosed {
		return nil, nil, ErrTicketClosed
	}
	msg, err := s.putMessageReturning(ctx, id, AuthorAgent, agentUserID, cleaned)
	if err != nil {
		return nil, nil, err
	}
	if err := s.repo.MarkAnswered(ctx, id, time.Now().UTC().Format(time.RFC3339)); err != nil {
		if current, getErr := s.repo.GetTicket(ctx, id); getErr == nil && current.Status == StatusClosed {
			return nil, nil, ErrTicketClosed
		}
		return nil, nil, fmt.Errorf("updating ticket status: %w", err)
	}
	if s.notifier != nil {
		s.notifier.Publish(ctx, Event{TicketID: id, Type: "ticket_updated", Status: StatusAnswered, EscalationLevel: ticket.EscalationLevel, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	}
	ticket, err = s.repo.GetTicket(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return msg, ticket, nil
}

func (s *Service) SetStatus(ctx context.Context, id, status string) error {
	if !contains(ValidStatuses, status) {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidInput, status)
	}
	ticket, err := s.repo.GetTicket(ctx, id)
	if err != nil {
		return err
	}
	if ticket.Status == StatusClosed {
		return ErrTicketClosed
	}
	if ticket.Status == status {
		return nil
	}
	updates := map[string]any{"status": status, "updated_at": time.Now().UTC().Format(time.RFC3339)}
	if status == StatusClosed {
		closedAt := time.Now().UTC()
		updates["closed_at"] = closedAt.Format(time.RFC3339)
		createdAt, parseErr := time.Parse(time.RFC3339, ticket.CreatedAt)
		if parseErr != nil {
			return fmt.Errorf("parsing ticket created_at: %w", parseErr)
		}
		if err := s.repo.CloseTicket(ctx, id, closedAt, int64(closedAt.Sub(createdAt).Seconds())); err != nil {
			return fmt.Errorf("closing ticket and recording support metrics: %w", err)
		}
	} else if err := s.repo.UpdateActiveStatus(ctx, id, status, updates["updated_at"].(string)); err != nil {
		if current, getErr := s.repo.GetTicket(ctx, id); getErr == nil && current.Status == StatusClosed {
			return ErrTicketClosed
		}
		return err
	}
	if err := s.putSystemMessage(ctx, id, fmt.Sprintf("Status alterado para %q.", status)); err != nil {
		return err
	}
	if s.notifier != nil {
		s.notifier.Publish(ctx, Event{TicketID: id, Type: "ticket_updated", Status: status, EscalationLevel: ticket.EscalationLevel, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	}
	return nil
}

func (s *Service) AddInternalNote(ctx context.Context, id, agentUserID, body string) (*InternalNote, error) {
	ticket, err := s.repo.GetTicket(ctx, id)
	if err != nil {
		return nil, err
	}
	if ticket.Status == StatusClosed {
		return nil, ErrTicketClosed
	}
	cleaned, err := validate.Freetext(body, internalNoteRule)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	note := &InternalNote{PK: id, AuthorID: agentUserID, Body: cleaned, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := s.repo.PutInternalNote(ctx, note); err != nil {
		if current, getErr := s.repo.GetTicket(ctx, id); getErr == nil && current.Status == StatusClosed {
			return nil, ErrTicketClosed
		}
		return nil, fmt.Errorf("putting internal note: %w", err)
	}
	if s.notifier != nil {
		s.notifier.PublishInternal(ctx, Event{TicketID: id, Type: "internal_note", AuthorType: AuthorAgent, Body: note.Body, CreatedAt: note.CreatedAt})
	}
	return note, nil
}

func (s *Service) SetEscalation(ctx context.Context, id, agentUserID, level string) error {
	if !contains(ValidEscalations, level) {
		return fmt.Errorf("%w: unknown escalation level %q", ErrInvalidInput, level)
	}
	ticket, err := s.repo.GetTicket(ctx, id)
	if err != nil {
		return err
	}
	if ticket.Status == StatusClosed {
		return ErrTicketClosed
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.repo.UpdateEscalation(ctx, id, level, agentUserID, now); err != nil {
		if current, getErr := s.repo.GetTicket(ctx, id); getErr == nil && current.Status == StatusClosed {
			return ErrTicketClosed
		}
		return err
	}
	if s.notifier != nil {
		s.notifier.Publish(ctx, Event{TicketID: id, Type: "ticket_updated", Status: ticket.Status, EscalationLevel: level, CreatedAt: now})
	}
	return nil
}

func (s *Service) Metrics(ctx context.Context, now time.Time) ([]MetricBucket, error) {
	return s.repo.GetMetrics(ctx, now.UTC())
}

func (s *Service) SubmitNPS(ctx context.Context, id, userID, anonToken string, score int, message string) error {
	ticket, err := s.resolveAccess(ctx, id, userID, anonToken)
	if err != nil {
		return err
	}
	if ticket.Status != StatusClosed || ticket.NPSScore != 0 {
		return ErrInvalidNPS
	}
	if score < 1 || score > 5 {
		return ErrInvalidNPS
	}
	cleanMessage := ""
	if score <= 3 {
		cleaned, err := validate.Freetext(message, npsMessageRule)
		if err != nil {
			return ErrInvalidNPS
		}
		cleanMessage = cleaned
	} else if message != "" {
		cleaned, err := validate.Freetext(message, npsMessageRule)
		if err != nil {
			return ErrInvalidNPS
		}
		cleanMessage = cleaned
	}
	if err := s.repo.UpdateTicket(ctx, id, map[string]any{
		"nps_score":   score,
		"nps_message": cleanMessage,
	}); err != nil {
		return err
	}
	return s.putSystemMessage(ctx, id, "Avaliação NPS registrada.")
}

func (s *Service) ListMine(ctx context.Context, userID, cursor string, limit int32) ([]*Ticket, string, error) {
	return s.repo.ListByUser(ctx, userID, cursor, limit)
}

func (s *Service) ListByStatus(ctx context.Context, status, cursor string, limit int32) ([]*Ticket, string, error) {
	return s.repo.ListByStatus(ctx, status, cursor, limit)
}

func (s *Service) putMessage(ctx context.Context, ticketID, authorType, authorID, body string) error {
	_, err := s.putMessageReturning(ctx, ticketID, authorType, authorID, body)
	return err
}

func (s *Service) putMessageReturning(ctx context.Context, ticketID, authorType, authorID, body string) (*Message, error) {
	msg := &Message{
		PK:         ticketID, // PutMessage builds the real PK/SK — see repository.go
		AuthorType: authorType,
		AuthorID:   authorID,
		Body:       body,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.repo.PutMessage(ctx, msg, authorType != AuthorSystem); err != nil {
		if ticket, getErr := s.repo.GetTicket(ctx, ticketID); getErr == nil && ticket.Status == StatusClosed {
			return nil, ErrTicketClosed
		}
		return nil, fmt.Errorf("putting message: %w", err)
	}
	if err := s.repo.UpdateTicket(ctx, ticketID, map[string]any{"last_message_at": msg.CreatedAt}); err != nil {
		return nil, fmt.Errorf("updating last_message_at: %w", err)
	}
	if s.notifier != nil {
		s.notifier.Publish(ctx, Event{TicketID: ticketID, Type: "message", AuthorType: authorType, Body: body, CreatedAt: msg.CreatedAt})
	}
	return msg, nil
}

func (s *Service) putSystemMessage(ctx context.Context, ticketID, body string) error {
	return s.putMessage(ctx, ticketID, AuthorSystem, "", body)
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
