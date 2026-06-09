package totp

import "strings"

type TOTPSecret struct {
	PK          string   `dynamodbav:"pk"`     // USER_{user_id}
	SK          string   `dynamodbav:"sk"`     // TOTP_default
	Secret      string   `dynamodbav:"secret"` // base32 TOTP secret
	Verified    bool     `dynamodbav:"verified"`
	BackupCodes []string `dynamodbav:"backup_codes"` // argon2id hashes
	CreatedAt   string   `dynamodbav:"created_at"`
}

const skValue = "TOTP_default"

func BuildPK(userID string) string {
	return "USER_" + userID
}

func BuildSK() string {
	return skValue
}

func (t *TOTPSecret) UserID() string {
	return strings.TrimPrefix(t.PK, "USER_")
}

func (t *TOTPSecret) IsSetup() bool {
	return t != nil && t.Verified
}
