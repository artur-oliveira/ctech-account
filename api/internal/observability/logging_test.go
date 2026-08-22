package observability

import (
	"context"
	"testing"
)

func TestRequestIDRoundTrip(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req-123")
	if got := RequestID(ctx); got != "req-123" {
		t.Fatalf("RequestID() = %q, want req-123", got)
	}
}

func TestWithEmptyRequestIDPreservesContext(t *testing.T) {
	ctx := context.Background()
	if got := WithRequestID(ctx, ""); got != ctx {
		t.Fatal("empty request ID should not wrap the context")
	}
}
