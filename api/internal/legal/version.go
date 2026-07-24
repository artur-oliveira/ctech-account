// Package legal holds the current version identifiers for the platform's
// Terms of Service and Privacy Policy. Bump these when the published text at
// /terms or /privacy changes materially; existing acceptances are versioned
// snapshots and are never rewritten.
package legal

const (
	CurrentToSVersion     = "3.0"
	CurrentPrivacyVersion = "3.0"
)

// Pending reports which documents a user still has to accept. Acceptance is an
// exact version match, so an account that accepted an older version — or one
// created before versioning existed, with no stored version at all — is pending
// and gets re-gated on its next /authorize.
type Pending struct {
	ToS     bool `json:"tos"`
	Privacy bool `json:"privacy"`
}

func (p Pending) Any() bool { return p.ToS || p.Privacy }

func PendingFor(tosVersion, privacyVersion string) Pending {
	return Pending{
		ToS:     tosVersion != CurrentToSVersion,
		Privacy: privacyVersion != CurrentPrivacyVersion,
	}
}
