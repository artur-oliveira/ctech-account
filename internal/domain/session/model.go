package session

import (
	"strings"
	"time"
)

type Session struct {
	PK               string `dynamodbav:"pk"`
	SK               string `dynamodbav:"sk"`
	RefreshTokenHash string `dynamodbav:"refresh_token_hash"`
	DeviceName       string `dynamodbav:"device_name"`
	IPAddress        string `dynamodbav:"ip_address"`
	UserAgent        string `dynamodbav:"user_agent"`
	CreatedAt        string `dynamodbav:"created_at"`
	LastUsedAt       string `dynamodbav:"last_used_at"`
	ExpiresAt        int64  `dynamodbav:"expires_at"` // Unix epoch — DynamoDB TTL attribute
}

const SessionTTL = 90 * 24 * time.Hour

func BuildPK(userID string) string {
	return "USER_" + userID
}

func BuildSK(sessionID string) string {
	return "SESSION_" + sessionID
}

func (s *Session) ID() string {
	return strings.TrimPrefix(s.SK, "SESSION_")
}

func (s *Session) UserID() string {
	return strings.TrimPrefix(s.PK, "USER_")
}

func (s *Session) IsExpired() bool {
	return time.Now().Unix() > s.ExpiresAt
}
