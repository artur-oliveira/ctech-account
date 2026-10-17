package deletion

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/api-commons/erasure"
)

type fakeServiceElig struct {
	blockers []erasure.Blocker
	calls    []string
}

func (f *fakeServiceElig) CheckService(_ context.Context, _, service string) ([]erasure.Blocker, error) {
	f.calls = append(f.calls, service)
	return f.blockers, nil
}

type countingOwnership struct{ calls int }

func (o *countingOwnership) Assess(context.Context, string) ([]Workspace, []erasure.Blocker, error) {
	o.calls++
	return nil, nil, nil
}

func (f *fixture) withServices(elig *fakeServiceElig) {
	f.svc = f.svc.WithServices(func(name string) (ServiceTarget, bool) {
		if name == "poker" || name == "dfe" {
			return ServiceTarget{Service: name, LoginClients: []string{name + "-web"}}, true
		}
		return ServiceTarget{}, false
	}, elig)
}

func TestRequestForAServiceAsksOnlyThatParticipant(t *testing.T) {
	f := newFixture(t)
	elig, own := &fakeServiceElig{}, &countingOwnership{}
	f.svc = f.svc.WithOwnership(own)
	f.withServices(elig)

	r, err := f.svc.RequestService(ctx, "u1", "poker")
	if err != nil {
		t.Fatal(err)
	}
	if r.Scope != ScopeService || r.Service != "poker" || r.State != StateAwaitingConfirmation {
		t.Fatalf("request = %+v", r)
	}
	if len(elig.calls) != 1 || elig.calls[0] != "poker" {
		t.Fatalf("participants asked = %v, want only poker", elig.calls)
	}
	if own.calls != 0 {
		t.Fatal("a service unlink must not run the account ownership assessment (U1, U2)")
	}
}

func TestRequestForAnUnknownServiceIsRefused(t *testing.T) {
	f := newFixture(t)
	f.withServices(&fakeServiceElig{})
	if _, err := f.svc.RequestService(ctx, "u1", "wallet"); !errors.Is(err, ErrUnknownService) {
		t.Fatalf("err = %v, want ErrUnknownService (wallet has no login client)", err)
	}
	if _, err := f.svc.Status(ctx, "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a refused request must not be stored")
	}
}

func TestRequestForAServiceReportsItsBlockers(t *testing.T) {
	f := newFixture(t)
	f.withServices(&fakeServiceElig{blockers: []erasure.Blocker{{Code: "poker.seated_at_table"}}})
	_, err := f.svc.RequestService(ctx, "u1", "poker")
	var blocked *BlockedError
	if !errors.As(err, &blocked) || len(blocked.Blockers) != 1 || blocked.Blockers[0].Code != "poker.seated_at_table" {
		t.Fatalf("err = %v, want BlockedError(poker.seated_at_table)", err)
	}
}

// Review Focus 6: the single open-request marker covers both scopes.
func TestAnOpenServiceRequestBlocksANewRequest(t *testing.T) {
	f := newFixture(t)
	f.withServices(&fakeServiceElig{})
	if _, err := f.svc.RequestService(ctx, "u1", "poker"); err != nil {
		t.Fatal(err)
	}
	if r, err := f.svc.RequestService(ctx, "u1", "dfe"); err != nil {
		// an unconfirmed request is replaced (lost e-mail), same as the account scope
		t.Fatalf("replacing an unconfirmed request: %v", err)
	} else if r.Service != "dfe" {
		t.Fatalf("service = %q", r.Service)
	}
	confirmed, _ := f.svc.Status(ctx, "u1")
	if _, err := f.svc.Confirm(ctx, confirmed.ID, f.mail.confirmToken); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RequestService(ctx, "u1", "poker"); !errors.Is(err, ErrOpenRequest) {
		t.Fatalf("a second service request: %v, want ErrOpenRequest", err)
	}
	if _, err := f.svc.Request(ctx, "u1"); !errors.Is(err, ErrOpenRequest) {
		t.Fatalf("an account request: %v, want ErrOpenRequest", err)
	}
}
