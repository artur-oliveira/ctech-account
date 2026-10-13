package deletion

import (
	"context"
	"fmt"
	"slices"

	"gopkg.aoctech.app/account/api/internal/domain/user"
)

type KYCStore interface {
	ListKeys(ctx context.Context, prefix string) ([]string, error)
	CopyObject(ctx context.Context, srcKey, dstKey string) error
	DeleteObject(ctx context.Context, key string) error
}

// moveKYCDocuments copies the user's confirmed KYC documents to the retention
// prefix (D6), then deletes every object under kyc/{user}/ (confirmed ones and
// abandoned uploads). Listing first makes it idempotent: a document already
// copied and deleted is no longer listed, so it is neither copied again nor
// looked for. Old versions expire through the bucket lifecycle (ruling R7).
func moveKYCDocuments(ctx context.Context, store KYCStore, u *user.User) error {
	userID := u.ID()
	keys, err := store.ListKeys(ctx, "kyc/"+userID+"/")
	if err != nil {
		return err
	}
	for _, d := range u.KYCDocuments {
		if !slices.Contains(keys, d.Key) {
			continue
		}
		if err := store.CopyObject(ctx, d.Key, user.KYCRetentionDocKey(userID, d.ID)); err != nil {
			return fmt.Errorf("retaining kyc document: %w", err)
		}
	}
	for _, k := range keys {
		if err := store.DeleteObject(ctx, k); err != nil {
			return fmt.Errorf("deleting kyc object: %w", err)
		}
	}
	return nil
}
