package deletion

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/api-commons/erasure"
)

// order records the calls of both collaborators in one sequence.
type order struct{ calls []string }

type orderedPub struct {
	o   *order
	err error
}

func (p *orderedPub) Publish(_ context.Context, m erasure.Message) error {
	p.o.calls = append(p.o.calls, "publish:"+string(m.Type)+":"+m.Services[0])
	return p.err
}

type orderedForget struct{ o *order }

func (f orderedForget) RemoveUnlinkedService(_ context.Context, _, service string) error {
	f.o.calls = append(f.o.calls, "forget:"+service)
	return nil
}

func relinkUser(unlinked ...string) *user.User {
	return &user.User{PK: "USER_u1", UnlinkedServices: unlinked}
}

func clientsOf(service string) []string {
	if service == "poker" {
		return []string{"poker-web"}
	}
	return []string{service + "-web"}
}

// Review Focus 3.
func TestRelinkPublishesBeforeItClears(t *testing.T) {
	o := &order{}
	r := NewRelinker(&orderedPub{o: o}, orderedForget{o}, clientsOf)
	if err := r.Relink(context.Background(), relinkUser("poker"), "poker-web"); err != nil {
		t.Fatal(err)
	}
	if len(o.calls) != 2 || o.calls[0] != "publish:user.relinked:poker" || o.calls[1] != "forget:poker" {
		t.Fatalf("calls = %v, want publish then forget", o.calls)
	}
}

func TestRelinkIgnoresOtherClients(t *testing.T) {
	o := &order{}
	r := NewRelinker(&orderedPub{o: o}, orderedForget{o}, clientsOf)
	if err := r.Relink(context.Background(), relinkUser("poker", "dfe"), "dfe-web"); err != nil {
		t.Fatal(err)
	}
	if len(o.calls) != 2 || o.calls[0] != "publish:user.relinked:dfe" {
		t.Fatalf("calls = %v, want only dfe", o.calls)
	}
}

func TestRelinkForAUserWhoNeverLeftCostsNothing(t *testing.T) {
	o := &order{}
	r := NewRelinker(&orderedPub{o: o}, orderedForget{o}, clientsOf)
	if err := r.Relink(context.Background(), relinkUser(), "poker-web"); err != nil || len(o.calls) != 0 {
		t.Fatalf("err %v calls %v; want nothing", err, o.calls)
	}
}

func TestRelinkKeepsTheRecordWhenThePublishFails(t *testing.T) {
	o := &order{}
	r := NewRelinker(&orderedPub{o: o, err: errors.New("sns down")}, orderedForget{o}, clientsOf)
	if err := r.Relink(context.Background(), relinkUser("poker"), "poker-web"); err == nil {
		t.Fatal("a failed publish must be an error")
	}
	for _, c := range o.calls {
		if c == "forget:poker" {
			t.Fatal("forgot the unlink although the product was not told")
		}
	}
}
