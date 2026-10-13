package deletion

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"gopkg.aoctech.app/api-commons/erasure"
)

type fakeRemote struct {
	blockers []erasure.Blocker
	err      error
	gotIDs   []string
}

func (f *fakeRemote) Check(_ context.Context, _ string, ids []string) ([]erasure.Blocker, error) {
	f.gotIDs = ids
	return f.blockers, f.err
}

func TestCompositeOwnership_MergesLocalAndRemote(t *testing.T) {
	remote := &fakeRemote{blockers: []erasure.Blocker{{Code: "remote"}}}
	c := NewCompositeOwnership(&fakeOwnership{sole: []Workspace{{ID: "o1"}}, blockers: []erasure.Blocker{{Code: "local"}}}, remote)
	sole, blockers, err := c.Assess(ctx, "u1")
	if err != nil || len(sole) != 1 || len(blockers) != 2 {
		t.Fatalf("sole %v blockers %v err %v", sole, blockers, err)
	}
	if !reflect.DeepEqual(remote.gotIDs, []string{"o1"}) {
		t.Fatalf("participants must be told the sole-member workspaces, got %v", remote.gotIDs)
	}
}

func TestCompositeOwnership_RemoteErrorIsNotEligible(t *testing.T) {
	c := NewCompositeOwnership(&fakeOwnership{}, &fakeRemote{err: errors.New("dfe down")})
	if _, _, err := c.Assess(ctx, "u1"); err == nil {
		t.Fatal("an unreachable participant must fail the assessment, never pass it")
	}
}

func TestGraceEnd_RemoteDownStaysPending(t *testing.T) {
	f := newFixture(t)
	remote := &fakeRemote{}
	f.svc = f.svc.WithOwnership(NewCompositeOwnership(&fakeOwnership{}, remote))
	r := f.confirm(t)
	remote.err = errors.New("wallet down")
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx)
	if s := f.stored(t, r.ID); s.State != StatePending || s.DueState != string(StatePending) {
		t.Fatalf("must stay pending and due: %+v", s)
	}
}

func TestRequest_AssessFailureIsUnavailable(t *testing.T) {
	f := newFixture(t)
	f.svc = f.svc.WithOwnership(NewCompositeOwnership(&fakeOwnership{}, &fakeRemote{err: errors.New("down")}))
	if _, err := f.svc.Request(ctx, "u1"); !errors.Is(err, ErrEligibilityUnavailable) {
		t.Fatalf("want ErrEligibilityUnavailable, got %v", err)
	}
}
