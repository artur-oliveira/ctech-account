package deletion

import (
	"testing"
	"time"
)

func TestStateOpen(t *testing.T) {
	for _, s := range []State{StateAwaitingConfirmation, StatePending, StateLocked, StatePurging} {
		if !s.Open() {
			t.Errorf("%s must hold the open-request marker", s)
		}
	}
	for _, s := range []State{StateCancelled, StateExpired} {
		if s.Open() {
			t.Errorf("%s must release the open-request marker", s)
		}
	}
}

func TestDueMarksCurrentState(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	r := &Request{State: StatePending}
	r.setDue(at)
	if r.DueState != string(StatePending) || r.NextActionAt != "2026-10-07T12:00:00Z" {
		t.Fatalf("due = %q %q", r.DueState, r.NextActionAt)
	}
	r.clearDue()
	if r.DueState != "" || r.NextActionAt != "" {
		t.Fatal("clearDue must drop the request from the due index")
	}
	if !parseTime(ts(at)).Equal(at) {
		t.Fatal("ts/parseTime must round-trip")
	}
}
