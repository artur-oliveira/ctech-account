package risk

import (
	"context"
	"testing"
	"time"
)

func TestNoopEvaluatorReturnsZeroScore(t *testing.T) {
	a, err := NoopEvaluator{}.Evaluate(context.Background(), "user-1", "203.0.113.1")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if a.Score != 0 || len(a.Signals) != 0 {
		t.Fatalf("assessment = %+v, want zero score and no signals", a)
	}
	if _, err := time.Parse(time.RFC3339, a.EvaluatedAt); err != nil {
		t.Fatalf("EvaluatedAt = %q is not RFC3339: %v", a.EvaluatedAt, err)
	}
}
