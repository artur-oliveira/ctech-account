package organization

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestCounterKeyShapes(t *testing.T) {
	if got := ownerPK("usr_1"); got != "OWNER#usr_1" {
		t.Errorf("ownerPK = %q", got)
	}
	if spacesCounterSK != "PERSONAL_SPACES" || spacesAttr != "n" || peopleAttr != "people_n" {
		t.Error("the counter keys drifted from spec § 3.2")
	}
}

// The guard pins the value read at the check: a concurrent write that moved
// the counter fails this one instead of being overwritten by it.
func TestGuardExpressions(t *testing.T) {
	update, cond, names, values := guardExpr(peopleAttr, CounterWrite{Expect: Counter{N: 4, Exists: true}, Next: 5}, "attribute_exists(pk)")
	if update != "SET #c = :next" || cond != "attribute_exists(pk) AND #c = :expect" || names["#c"] != "people_n" {
		t.Fatalf("update=%q cond=%q names=%v", update, cond, names)
	}
	if v := values[":expect"].(*types.AttributeValueMemberN).Value; v != "4" {
		t.Fatalf(":expect = %s", v)
	}
	if v := values[":next"].(*types.AttributeValueMemberN).Value; v != "5" {
		t.Fatalf(":next = %s", v)
	}

	_, cond, _, _ = guardExpr(spacesAttr, CounterWrite{Next: 1}, "")
	if cond != "attribute_not_exists(#c)" {
		t.Fatalf("an absent counter: cond = %q", cond)
	}

	// Unlimited still maintains the counter, with no condition (spec § 3.2).
	_, cond, _, values = guardExpr(spacesAttr, CounterWrite{Expect: Counter{N: 9, Exists: true}, Next: 10, Blind: true}, "")
	if cond != "" {
		t.Fatalf("a blind write has a condition: %q", cond)
	}
	if _, ok := values[":expect"]; ok {
		t.Fatal("a blind write carries :expect")
	}
}

func cancelled(reasons ...string) error {
	tce := &types.TransactionCanceledException{Message: aws.String("Transaction cancelled")}
	for _, r := range reasons {
		tce.CancellationReasons = append(tce.CancellationReasons, types.CancellationReason{Code: aws.String(r)})
	}
	return tce
}

func TestTheGuardItemDecidesWhetherTheCounterMoved(t *testing.T) {
	if err := guardOutcome(cancelled("None", "None", "ConditionalCheckFailed"), 2, ErrAlreadyMember); !errors.Is(err, ErrCounterMoved) {
		t.Fatalf("guard condition failed: %v", err)
	}
	if err := guardOutcome(cancelled("ConditionalCheckFailed", "None", "None"), 2, ErrAlreadyMember); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("another item's condition failed: %v", err)
	}
	// A concurrent transaction wrote nothing and evaluated nothing: retry.
	if err := guardOutcome(cancelled("None", "TransactionConflict", "None"), 2, ErrAlreadyMember); !errors.Is(err, ErrCounterMoved) {
		t.Fatalf("a transaction conflict: %v", err)
	}
	if err := guardOutcome(nil, 2, ErrAlreadyMember); err != nil {
		t.Fatalf("success: %v", err)
	}
	boom := errors.New("network")
	if err := guardOutcome(boom, 2, ErrAlreadyMember); !errors.Is(err, boom) {
		t.Fatalf("an unrelated failure: %v", err)
	}
}

// Both fakes must honour the guard exactly as the condition does.
func TestTheFakeHonoursTheGuard(t *testing.T) {
	repo := newFakeRepo()
	org := &Organization{ID: "o1", OwnerUserID: "usr_1", Kind: KindPersonal, CreatedAt: fixedClock()}
	if err := repo.CreateWithOwnerGuarded(t.Context(), org, "Dono", CounterWrite{Expect: Counter{N: 3, Exists: true}, Next: 4}); !errors.Is(err, ErrCounterMoved) {
		t.Fatalf("absent counter expected as 3: %v", err)
	}
	if err := repo.CreateWithOwnerGuarded(t.Context(), org, "Dono", CounterWrite{Next: 1}); err != nil {
		t.Fatal(err)
	}
	if c, _ := repo.SpaceCounter(t.Context(), "usr_1"); c != (Counter{N: 1, Exists: true}) {
		t.Fatalf("counter = %+v", c)
	}
}
