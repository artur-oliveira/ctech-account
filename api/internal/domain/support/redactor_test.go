package support

import (
	"context"
	"testing"
)

type fakeTickets struct {
	tickets  map[string]*Ticket    // id -> ticket
	messages map[string][]*Message // ticket id -> messages
	log      []string
}

func (f *fakeTickets) ListByUser(_ context.Context, userID, _ string, _ int32) ([]*Ticket, string, error) {
	var out []*Ticket
	for _, t := range f.tickets {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	return out, "", nil
}
func (f *fakeTickets) ListMessages(_ context.Context, ticketID string) ([]*Message, error) {
	return f.messages[ticketID], nil
}
func (f *fakeTickets) RedactMessage(_ context.Context, ticketID, sk, body string) error {
	for _, m := range f.messages[ticketID] {
		if m.SK == sk {
			m.Body = body
		}
	}
	f.log = append(f.log, "redact:"+sk)
	return nil
}
func (f *fakeTickets) UpdateTicket(_ context.Context, id string, updates map[string]any) error {
	if _, ok := updates["user_id"]; ok {
		f.tickets[id].UserID = ""
	}
	f.log = append(f.log, "drop-owner:"+id)
	return nil
}

func TestRedactor_RedactsBeforeDroppingOwnership(t *testing.T) {
	f := &fakeTickets{
		tickets: map[string]*Ticket{"t1": {PK: BuildPK("t1"), UserID: "u1"}},
		messages: map[string][]*Message{"t1": {
			{SK: "MSG#1", AuthorType: AuthorUser, AuthorID: "u1", Body: "meu CPF é 123"},
			{SK: "MSG#2", AuthorType: AuthorAgent, AuthorID: "agent", Body: "Olá"},
		}},
	}
	r := newRedactorWith(f)
	for i := 0; i < 2; i++ {
		if err := r.RedactUser(context.Background(), "u1"); err != nil {
			t.Fatalf("RedactUser: %v", err)
		}
	}
	if f.messages["t1"][0].Body != RedactedBody || f.messages["t1"][1].Body != "Olá" {
		t.Fatalf("bodies %q / %q", f.messages["t1"][0].Body, f.messages["t1"][1].Body)
	}
	if len(f.log) != 2 || f.log[0] != "redact:MSG#1" || f.log[1] != "drop-owner:t1" {
		t.Fatalf("order %v: messages must be redacted before ownership is dropped, once", f.log)
	}
}
