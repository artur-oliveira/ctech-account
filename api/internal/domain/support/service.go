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
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

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
	cleaned, err := validate.Freetext(body, bodyRule)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := s.putMessage(ctx, id, AuthorUser, userID, cleaned); err != nil {
		return err
	}
	if ticket.Status == StatusClosed {
		if err := s.putSystemMessage(ctx, id, "Ticket reaberto pelo usuário."); err != nil {
			return err
		}
		return s.repo.UpdateTicket(ctx, id, map[string]any{
			"status":     StatusOpen,
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		})
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
	if _, err := s.repo.GetTicket(ctx, id); err != nil {
		return nil, nil, err
	}
	msg, err := s.putMessageReturning(ctx, id, AuthorAgent, agentUserID, cleaned)
	if err != nil {
		return nil, nil, err
	}
	if err := s.repo.UpdateTicket(ctx, id, map[string]any{
		"status":     StatusAnswered,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return nil, nil, fmt.Errorf("updating ticket status: %w", err)
	}
	ticket, err := s.repo.GetTicket(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return msg, ticket, nil
}

func (s *Service) SetStatus(ctx context.Context, id, status string) error {
	if !contains(ValidStatuses, status) {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidInput, status)
	}
	if _, err := s.repo.GetTicket(ctx, id); err != nil {
		return err
	}
	updates := map[string]any{"status": status, "updated_at": time.Now().UTC().Format(time.RFC3339)}
	if status == StatusClosed {
		updates["closed_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	if err := s.repo.UpdateTicket(ctx, id, updates); err != nil {
		return err
	}
	return s.putSystemMessage(ctx, id, fmt.Sprintf("Status alterado para %q.", status))
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
	if err := s.repo.PutMessage(ctx, msg); err != nil {
		return nil, fmt.Errorf("putting message: %w", err)
	}
	if err := s.repo.UpdateTicket(ctx, ticketID, map[string]any{"last_message_at": msg.CreatedAt}); err != nil {
		return nil, fmt.Errorf("updating last_message_at: %w", err)
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
