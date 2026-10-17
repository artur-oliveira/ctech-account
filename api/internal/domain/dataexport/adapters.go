package dataexport

import (
	"context"

	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

// UserContacts adapts the user service to Users. Only an active account may
// export: a locked or purged one has nothing to offer (E7).
type UserContacts struct {
	Users interface {
		GetByID(ctx context.Context, id string) (*user.User, error)
	}
}

func (u UserContacts) Contact(ctx context.Context, userID string) (string, string, bool, error) {
	usr, err := u.Users.GetByID(ctx, userID)
	if err != nil {
		return "", "", false, err
	}
	return usr.Email, usr.FirstName, usr.IsEnabled && usr.DeletionState == "" && usr.PurgedAt == "", nil
}

// AuditRecorder adapts the audit service to Auditor.
type AuditRecorder struct{ Audit *audit.Service }

func (a AuditRecorder) Record(ctx context.Context, userID, event string, meta map[string]string) {
	a.Audit.Record(ctx, audit.Entry{UserID: userID, Type: event, Metadata: meta})
}
