package audit

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

const (
	pkUserPrefix = "USER_"
	pkAnonPrefix = "ANON_"
	skPrefix     = "EVT_"
	// EventTTL is how long audit events are retained (DynamoDB TTL).
	EventTTL = 400 * 24 * time.Hour
)

type Event struct {
	PK        string            `dynamodbav:"pk"`
	SK        string            `dynamodbav:"sk"`
	EventType string            `dynamodbav:"event_type"`
	IP        string            `dynamodbav:"ip,omitempty"`
	UserAgent string            `dynamodbav:"user_agent,omitempty"`
	Metadata  map[string]string `dynamodbav:"metadata,omitempty"`
	CreatedAt string            `dynamodbav:"created_at"`
	ExpiresAt int64             `dynamodbav:"expires_at"`
}

func BuildPK(userID string) string { return pkUserPrefix + userID }

// AnonPK keys events that cannot be attributed to a known user (e.g. failed
// login against an unknown email) by client IP.
func AnonPK(ip string) string { return pkAnonPrefix + ip }

// BuildSK returns a chronologically sortable, collision-safe sort key:
// EVT_{RFC3339Nano}_{4 random bytes hex}.
func BuildSK(t time.Time) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return skPrefix + t.UTC().Format(time.RFC3339Nano) + "_" + hex.EncodeToString(b)
}
