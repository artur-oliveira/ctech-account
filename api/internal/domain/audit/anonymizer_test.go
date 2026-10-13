package audit

import (
	"context"
	"testing"
)

type fakeEvents struct {
	events     []*Event
	anonymized map[string]bool // sk -> dropped metadata
}

func (f *fakeEvents) QueryByUser(_ context.Context, _, cursor string, _ int32) ([]*Event, string, error) {
	if cursor != "" {
		return nil, "", nil
	}
	return f.events, "", nil
}
func (f *fakeEvents) Anonymize(_ context.Context, _ string, sk string, dropMetadata bool) error {
	f.anonymized[sk] = dropMetadata
	return nil
}

func TestAnonymizer_KeepsDeletionEvidence(t *testing.T) {
	f := &fakeEvents{anonymized: map[string]bool{}, events: []*Event{
		{PK: BuildPK("u1"), SK: "EVT_1", EventType: EventLoginSuccess, IP: "1.2.3.4", UserAgent: "UA"},
		{PK: BuildPK("u1"), SK: "EVT_2", EventType: "account.deletion.requested", IP: "1.2.3.4", Metadata: map[string]string{"request_id": "r1"}},
		{PK: BuildPK("u1"), SK: "EVT_3", EventType: EventLoginSuccess}, // already clean
	}}
	if err := newAnonymizerWith(f).AnonymizeUser(context.Background(), "u1"); err != nil {
		t.Fatalf("AnonymizeUser: %v", err)
	}
	if drop, ok := f.anonymized["EVT_1"]; !ok || !drop {
		t.Fatal("a regular event must lose ip/ua and metadata")
	}
	if drop, ok := f.anonymized["EVT_2"]; !ok || drop {
		t.Fatal("a deletion event must lose ip/ua but keep its metadata")
	}
	if _, ok := f.anonymized["EVT_3"]; ok {
		t.Fatal("an already-clean event must not be rewritten")
	}
}
