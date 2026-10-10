package billingclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

func TestEntitlementsAsksForTheFinanceOwner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/entitlements" || r.URL.Query().Get("customer_ref") != "USER_u1" ||
			r.URL.Query().Get("owner_key") != "finance" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("request = %s %s auth=%q", r.Method, r.URL, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"entitled":false,"subscriptions":[],"default":{"price_id":"p_free","plan":"free","metadata":{"quota_spaces":"1","quota_people_per_space":"1"}}}`))
	}))
	defer srv.Close()

	e, err := New(srv.URL+"/", staticToken("tok")).Entitlements(context.Background(), CustomerRef("u1"), OwnerKeyFinance)
	if err != nil {
		t.Fatal(err)
	}
	if e.Default == nil || e.Default.Plan != "free" || e.Default.Metadata["quota_spaces"] != "1" {
		t.Fatalf("entitlements = %+v", e)
	}
}

func TestEntitlementsRetriesOnceOnA5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"entitled":true,"subscriptions":[]}`))
	}))
	defer srv.Close()
	if _, err := New(srv.URL, staticToken("t")).Entitlements(context.Background(), "USER_u", OwnerKeyFinance); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

// A 4xx is billing answering, wrongly for us: not retried, still unavailable.
func TestEntitlementsDoesNotRetryA4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := New(srv.URL, staticToken("t")).Entitlements(context.Background(), "USER_u", OwnerKeyFinance)
	if !errors.Is(err, ErrUnavailable) || calls.Load() != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls.Load())
	}
}

func TestEntitlementsGivesUpWithinTwoSeconds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	start := time.Now()
	_, err := New(srv.URL, staticToken("t")).Entitlements(context.Background(), "USER_u", OwnerKeyFinance)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if elapsed := time.Since(start); elapsed > EntitlementsTimeout+500*time.Millisecond {
		t.Fatalf("took %v, budget %v", elapsed, EntitlementsTimeout)
	}
}

func TestReportLevel(t *testing.T) {
	var got map[string]any
	var key string
	status := http.StatusCreated
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1.0/usage/levels" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		key = r.Header.Get("Idempotency-Key")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := New(srv.URL, staticToken("t"))
	at := time.Date(2026, 10, 10, 14, 3, 0, 0, time.UTC)
	report := LevelReport{CustomerRef: "USER_u1", Meter: MeterSpaces, Value: 4, OccurredAt: at, IdempotencyKey: LevelKey("u1", MeterSpaces, at)}

	if err := c.ReportLevel(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	if got["customer_ref"] != "USER_u1" || got["meter"] != "finance_spaces" || got["value"] != float64(4) ||
		got["occurred_at"] != "2026-10-10T14:03:00Z" || got["idempotency_key"] != "lvl:u1:finance_spaces:1791640980000" {
		t.Fatalf("body = %v", got)
	}
	if key != "lvl:u1:finance_spaces:1791640980000" {
		t.Fatalf("Idempotency-Key = %q", key)
	}
	status = http.StatusConflict // same key already recorded
	if err := c.ReportLevel(context.Background(), report); err != nil {
		t.Fatalf("409 is delivered: %v", err)
	}
	status = http.StatusInternalServerError
	if err := c.ReportLevel(context.Background(), report); err == nil {
		t.Fatal("a 500 was taken as delivered")
	}
}

// Billing answers 409 concurrent_update when another report moved the latest
// level under this one: nothing was recorded, so it is not delivered and the
// queue must retry it. Only idempotency_key_reused is a 409 that means "done".
func TestAConcurrentUpdateIsNotDelivered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"type":"/problems/conflict","status":409,"code":"concurrent_update"}`))
	}))
	defer srv.Close()
	at := time.Date(2026, 10, 10, 14, 3, 0, 0, time.UTC)
	err := New(srv.URL, staticToken("t")).ReportLevel(context.Background(),
		LevelReport{CustomerRef: "USER_u1", Meter: MeterSpaces, Value: 4, OccurredAt: at, IdempotencyKey: LevelKey("u1", MeterSpaces, at)})
	if err == nil {
		t.Fatal("a concurrent_update 409 was taken as delivered")
	}
}
