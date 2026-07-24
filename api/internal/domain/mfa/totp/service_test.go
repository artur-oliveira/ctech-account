package totp

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"gopkg.aoctech.app/account/api/internal/crypto"
)

func validCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generating valid totp code: %v", err)
	}
	return code
}

// Two concurrent Verify calls must only let the first generate backup codes;
// the second is idempotent and must not clobber them (CON-016).
func TestVerifyConcurrentNoClobber(t *testing.T) {
	repo := newMockRepository()
	svc := &Service{repo: repo}
	ctx := context.Background()

	const userID = "u_concurrent"
	const secret = "JBSWY3DPEHPK3PXP"
	if err := repo.Create(ctx, &TOTPSecret{PK: BuildPK(userID), SK: BuildSK(), Secret: secret, Verified: false}); err != nil {
		t.Fatal(err)
	}
	code := validCode(t, secret)

	var wg sync.WaitGroup
	results := make([]struct {
		codes []string
		err   error
	}, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes, err := svc.Verify(ctx, userID, code)
			results[i].codes = codes
			results[i].err = err
		}(i)
	}
	wg.Wait()

	nonNil := 0
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("goroutine %d: unexpected error: %v", i, r.err)
		}
		if r.codes != nil {
			nonNil++
		}
	}
	if nonNil != 1 {
		t.Fatalf("expected exactly one confirm to generate backup codes, got %d", nonNil)
	}

	stored, err := repo.Get(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.BackupCodes) != 10 {
		t.Fatalf("expected 10 stored backup codes, got %d", len(stored.BackupCodes))
	}
}

// Reusing a backup code must be rejected the second time (CON-017).
func TestValidateBackupCodeDoubleSpend(t *testing.T) {
	repo := newMockRepository()
	svc := &Service{repo: repo}
	ctx := context.Background()

	const userID = "u_doublespend"
	const secret = "JBSWY3DPEHPK3PXP"

	rawCodes := []string{"123456", "234567", "345678"}
	hashes := make([]string, len(rawCodes))
	for i, c := range rawCodes {
		h, err := crypto.HashPassword(c)
		if err != nil {
			t.Fatal(err)
		}
		hashes[i] = h
	}
	if err := repo.Create(ctx, &TOTPSecret{
		PK: BuildPK(userID), SK: BuildSK(), Secret: secret,
		Verified: true, BackupCodes: hashes, Version: 1,
	}); err != nil {
		t.Fatal(err)
	}

	ok, err := svc.Validate(ctx, userID, "123456")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("first use of backup code should succeed")
	}

	ok, err = svc.Validate(ctx, userID, "123456")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("second use of the same backup code must be rejected")
	}
}

// Two concurrent backup-code consumptions of the same code: exactly one wins
// (CON-017 TOCTOU).
func TestConsumeBackupCodeConcurrent(t *testing.T) {
	repo := newMockRepository()
	ctx := context.Background()

	const userID = "u_toctou"
	hashes := []string{"h1", "h2", "h3"}
	if err := repo.Create(ctx, &TOTPSecret{
		PK: BuildPK(userID), SK: BuildSK(), Secret: "x",
		Verified: true, BackupCodes: hashes, Version: 1,
	}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	applied := make([]bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Both consume h1 (index 0) with the version they read (1).
			a, err := repo.ConsumeBackupCode(ctx, userID, []string{"h2", "h3"}, 1)
			if err != nil {
				t.Errorf("consume: %v", err)
			}
			applied[i] = a
		}(i)
	}
	wg.Wait()

	got := 0
	for _, a := range applied {
		if a {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("expected exactly one backup-code consume to apply, got %d", got)
	}
}
