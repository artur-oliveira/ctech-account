package deletion

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gopkg.aoctech.app/account/api/internal/domain/user"
)

var errNoSuchKey = errors.New("no such key")

type fakeBucket struct{ objects map[string]bool }

func (f *fakeBucket) ListKeys(_ context.Context, prefix string) ([]string, error) {
	var out []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}
func (f *fakeBucket) CopyObject(_ context.Context, src, dst string) error {
	if !f.objects[src] {
		return errNoSuchKey
	}
	f.objects[dst] = true
	return nil
}
func (f *fakeBucket) DeleteObject(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

func TestMoveKYCDocuments_IsIdempotent(t *testing.T) {
	b := &fakeBucket{objects: map[string]bool{
		"kyc/u1/d1": true, "kyc/u1/orphan": true, "kyc/u2/x": true,
	}}
	u := &user.User{PK: user.BuildPK("u1"), KYCDocuments: []user.KYCDocument{{ID: "d1", Key: "kyc/u1/d1"}}}
	for i := 0; i < 2; i++ {
		if err := moveKYCDocuments(context.Background(), b, u); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	if !b.objects[user.KYCRetentionDocKey("u1", "d1")] || len(b.objects) != 2 || !b.objects["kyc/u2/x"] {
		t.Fatalf("objects %v: d1 retained, u1's prefix emptied, u2 untouched", b.objects)
	}
}
