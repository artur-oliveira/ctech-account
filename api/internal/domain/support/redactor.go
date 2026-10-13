package support

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// RedactedBody replaces a deleted user's own support messages.
const RedactedBody = "[mensagem removida a pedido do titular]"

type ticketStore interface {
	ListByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Ticket, string, error)
	ListMessages(ctx context.Context, ticketID string) ([]*Message, error)
	RedactMessage(ctx context.Context, ticketID, messageSK, body string) error
	UpdateTicket(ctx context.Context, id string, updates map[string]any) error
}

// Redactor anonymizes a deleted user's tickets (inventory §3, ruling R9): their
// own message bodies are replaced, then the ticket loses user_id and the NPS
// comment. Agent replies and internal notes stay. Messages go first because a
// ticket is only found through user_id: dropping it first would strand
// unredacted messages if the purge crashed in between.
type Redactor struct{ store ticketStore }

func NewRedactor(db *dynamodb.Client, tablePrefix string) *Redactor {
	return &Redactor{store: NewRepository(db, tablePrefix).(*dynamoRepository)}
}

func newRedactorWith(s ticketStore) *Redactor { return &Redactor{store: s} }

func (r *Redactor) RedactUser(ctx context.Context, userID string) error {
	for {
		tickets, _, err := r.store.ListByUser(ctx, userID, "", 50)
		if err != nil {
			return fmt.Errorf("listing tickets: %w", err)
		}
		if len(tickets) == 0 {
			return nil
		}
		for _, t := range tickets {
			id := t.ID()
			msgs, err := r.store.ListMessages(ctx, id)
			if err != nil {
				return fmt.Errorf("listing messages: %w", err)
			}
			for _, m := range msgs {
				if m.AuthorType == AuthorUser && m.AuthorID == userID && m.Body != RedactedBody {
					if err := r.store.RedactMessage(ctx, id, m.SK, RedactedBody); err != nil {
						return fmt.Errorf("redacting message: %w", err)
					}
				}
			}
			if err := r.store.UpdateTicket(ctx, id, map[string]any{"user_id": nil, "nps_message": nil}); err != nil {
				return fmt.Errorf("dropping ticket owner: %w", err)
			}
		}
	}
}
