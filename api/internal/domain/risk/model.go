package risk

import (
	"context"
	"time"
)

// Signal is one contributing factor to an Assessment, named for a category a
// future Evaluator will detect (VPN/Tor use, multiple accounts, suspicious
// activity — spec §9). Score is that signal's contribution; Detail is a
// reviewer-facing note.
type Signal struct {
	Name   string
	Score  int
	Detail string
}

// Assessment is the latest risk snapshot for one KYC submission. It is
// informational only — nothing in kyc.Service gates on Score; a human
// reviewer sees it via cmd/kyc show.
type Assessment struct {
	Score       int
	Signals     []Signal
	EvaluatedAt string // RFC3339
}

// Evaluator scores a submission's fraud risk from the acting user and IP.
// userID+ip already thread through every call site, so a real IP-reputation
// lookup (VPN/Tor) or multi-account correlation plugs in without a signature
// change.
type Evaluator interface {
	Evaluate(ctx context.Context, userID, ip string) (Assessment, error)
}

// NoopEvaluator is the only Evaluator implementation until real detectors are
// defined.
//
// ponytail: always scores zero — swap in a concrete Evaluator once VPN/Tor,
// multi-account, or suspicious-activity detection criteria exist; Evaluate's
// signature already carries what those need (userID, ip).
type NoopEvaluator struct{}

func (NoopEvaluator) Evaluate(_ context.Context, _, _ string) (Assessment, error) {
	return Assessment{EvaluatedAt: time.Now().UTC().Format(time.RFC3339)}, nil
}
