package legal_test

import (
	"testing"

	"gopkg.aoctech.app/account/internal/legal"
)

func TestPendingFor(t *testing.T) {
	tests := []struct {
		name           string
		tosVersion     string
		privacyVersion string
		want           legal.Pending
	}{
		{
			// Accounts created before versioning existed carry no version at all.
			name: "never accepted anything",
			want: legal.Pending{ToS: true, Privacy: true},
		},
		{
			name:           "accepted an older version of both",
			tosVersion:     "1.0",
			privacyVersion: "1.0",
			want:           legal.Pending{ToS: true, Privacy: true},
		},
		{
			name:           "up to date",
			tosVersion:     legal.CurrentToSVersion,
			privacyVersion: legal.CurrentPrivacyVersion,
			want:           legal.Pending{},
		},
		{
			// The two documents version independently.
			name:           "only the ToS moved",
			tosVersion:     "1.0",
			privacyVersion: legal.CurrentPrivacyVersion,
			want:           legal.Pending{ToS: true},
		},
		{
			name:           "only the privacy policy moved",
			tosVersion:     legal.CurrentToSVersion,
			privacyVersion: "1.0",
			want:           legal.Pending{Privacy: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := legal.PendingFor(tt.tosVersion, tt.privacyVersion)
			if got != tt.want {
				t.Errorf("PendingFor(%q, %q) = %+v, want %+v", tt.tosVersion, tt.privacyVersion, got, tt.want)
			}
			if got.Any() != (tt.want.ToS || tt.want.Privacy) {
				t.Errorf("Any() = %v, disagrees with %+v", got.Any(), got)
			}
		})
	}
}
