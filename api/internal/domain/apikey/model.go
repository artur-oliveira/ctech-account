package apikey

import (
	"strings"
	"time"
)

type APIKey struct {
	PK         string   `dynamodbav:"pk"` // USER_{user_id}
	SK         string   `dynamodbav:"sk"` // APIKEY_{key_id}
	KeyPrefix  string   `dynamodbav:"key_prefix"`
	KeyHash    string   `dynamodbav:"key_hash"`
	Name       string   `dynamodbav:"name"`
	Scopes     []string `dynamodbav:"scopes"`
	LastUsedAt string   `dynamodbav:"last_used_at,omitempty"`
	ExpiresAt  int64    `dynamodbav:"expires_at,omitempty"` // 0 = no expiry
	CreatedAt  string   `dynamodbav:"created_at"`
}

func BuildPK(userID string) string {
	return "USER_" + userID
}

func BuildSK(keyID string) string {
	return "APIKEY_" + keyID
}

func (k *APIKey) ID() string {
	return strings.TrimPrefix(k.SK, "APIKEY_")
}

func (k *APIKey) UserID() string {
	return strings.TrimPrefix(k.PK, "USER_")
}

func (k *APIKey) IsExpired() bool {
	if k.ExpiresAt == 0 {
		return false
	}
	return time.Now().Unix() > k.ExpiresAt
}
