package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"
)

func TestGetDel_ConsumesOnce(t *testing.T) {
	c := NewInMemory()
	ctx := context.Background()

	if err := c.Set(ctx, "k", "value", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	var got string
	if err := c.GetDel(ctx, "k", &got); err != nil {
		t.Fatalf("first GetDel: %v", err)
	}
	if got != "value" {
		t.Fatalf("want %q, got %q", "value", got)
	}

	// Second read must miss — the key was consumed atomically.
	var again string
	if err := c.GetDel(ctx, "k", &again); err != ErrNotFound {
		t.Fatalf("second GetDel: want ErrNotFound, got %v", err)
	}
}

func TestGetDel_MissingKey(t *testing.T) {
	c := NewInMemory()
	var got string
	if err := c.GetDel(context.Background(), "absent", &got); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestGetDel_DisabledCache(t *testing.T) {
	c, _ := New("") // disabled
	var got string
	if err := c.GetDel(context.Background(), "k", &got); err != ErrNotFound {
		t.Fatalf("disabled cache: want ErrNotFound, got %v", err)
	}
}

func TestIncrAndCount_Window(t *testing.T) {
	c := NewInMemory()
	ctx := context.Background()

	if n, _ := c.Count(ctx, "rl"); n != 0 {
		t.Fatalf("initial Count: want 0, got %d", n)
	}

	for i := int64(1); i <= 3; i++ {
		n, err := c.Incr(ctx, "rl", time.Minute)
		if err != nil {
			t.Fatalf("Incr: %v", err)
		}
		if n != i {
			t.Fatalf("Incr #%d: want %d, got %d", i, i, n)
		}
	}

	if n, _ := c.Count(ctx, "rl"); n != 3 {
		t.Fatalf("Count after 3 incr: want 3, got %d", n)
	}
}

func TestIncr_WindowExpiryResets(t *testing.T) {
	c := NewInMemory()
	ctx := context.Background()

	if _, err := c.Incr(ctx, "rl", time.Millisecond); err != nil {
		t.Fatalf("Incr: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	// Expired window: counter must restart at 1, not accumulate.
	n, err := c.Incr(ctx, "rl", time.Minute)
	if err != nil {
		t.Fatalf("Incr after expiry: %v", err)
	}
	if n != 1 {
		t.Fatalf("want counter reset to 1, got %d", n)
	}
}

func TestSetNXAcquiresOnce(t *testing.T) {
	c := NewInMemory()
	ctx := context.Background()

	ok, err := c.SetNX(ctx, "lock", "1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("first SetNX: ok=%v err=%v", ok, err)
	}
	ok, err = c.SetNX(ctx, "lock", "1", time.Minute)
	if err != nil || ok {
		t.Fatalf("second SetNX must lose: ok=%v err=%v", ok, err)
	}
}

func TestSetNXDisabledClientIsNoop(t *testing.T) {
	c, _ := New("")
	ok, err := c.SetNX(context.Background(), "lock", "1", time.Minute)
	if err != nil || ok {
		t.Fatalf("disabled client: ok=%v err=%v", ok, err)
	}
}

// TestSetNXErr_IgnoresExistingKey guards the 2026-07-20 regression where
// Incr mistook valkey.Nil (returned by SET NX when the key already exists)
// for a transport failure and 503'd every 2nd+ request in a rate-limit window.
func TestSetNXErr_IgnoresExistingKey(t *testing.T) {
	if err := setNXErr(valkey.Nil); err != nil {
		t.Fatalf("valkey.Nil (key already exists) must be non-fatal, got %v", err)
	}
	if err := setNXErr(nil); err != nil {
		t.Fatalf("nil must be non-fatal, got %v", err)
	}
	transport := errors.New("connection refused")
	if err := setNXErr(transport); !errors.Is(err, transport) {
		t.Fatalf("real transport error must be wrapped and surfaced, got %v", err)
	}
}
