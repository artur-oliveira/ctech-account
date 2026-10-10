package planlimit

import (
	"testing"
	"time"
)

func TestQueueKeys(t *testing.T) {
	if nowSK("u1") != "NOW#u1" {
		t.Fatalf("nowSK = %q", nowSK("u1"))
	}
	at := time.Unix(1760104980, 0)
	if got := atSK(at, "u1"); got != "AT#001760104980#u1" {
		t.Fatalf("atSK = %q", got)
	}
	// Due's upper bound includes every row due at or before now, and none after.
	hi := atUpperBound(at)
	if !(atSK(at, "zzzz") <= hi) || !(atSK(at.Add(time.Second), "a") > hi) {
		t.Fatalf("bound %q is wrong", hi)
	}
}
