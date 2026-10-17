package deletion

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/api-commons/erasure"
)

// UnlinkForgetter removes a product from the user's unlinked set.
type UnlinkForgetter interface {
	RemoveUnlinkedService(ctx context.Context, userID, service string) error
}

// Relinker tells a product that a person who left it is back (spec § 7).
type Relinker struct {
	pub     Publisher
	users   UnlinkForgetter
	clients func(service string) []string
	now     func() time.Time
}

func NewRelinker(pub Publisher, users UnlinkForgetter, clients func(service string) []string) *Relinker {
	return &Relinker{pub: pub, users: users, clients: clients, now: time.Now}
}

// Relink runs on a sign-in through clientID. For every product the user left
// whose login clients include clientID it publishes user.relinked and only then
// forgets the product: a crash in between republishes on the next sign-in, and
// the participant's Relink is a no-op on a record that is not erased. A publish
// error returns the error and forgets nothing, so the sign-in can be refused
// while the product still refuses the person (fail closed).
func (r *Relinker) Relink(ctx context.Context, u *user.User, clientID string) error {
	for _, service := range u.UnlinkedServices {
		if !slices.Contains(r.clients(service), clientID) {
			continue
		}
		if r.pub == nil {
			return fmt.Errorf("deletion: no publisher to relink %s", service)
		}
		err := r.pub.Publish(ctx, erasure.Message{
			Version: erasure.Version, Type: erasure.TypeRelinked, RequestID: uuid.New().String(), Sub: u.ID(),
			Scope: erasure.ScopeService, Services: []string{service}, Attempt: 1, IssuedAt: r.now().UTC(),
		})
		if err != nil {
			return fmt.Errorf("publishing relink for %s: %w", service, err)
		}
		if err := r.users.RemoveUnlinkedService(ctx, u.ID(), service); err != nil {
			return fmt.Errorf("forgetting unlinked %s: %w", service, err)
		}
	}
	return nil
}
