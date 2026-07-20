package totp

import "strings"

type TOTPSecret struct {
	PK              string   `dynamodbav:"pk"`                       // USER_{user_id}
	SK              string   `dynamodbav:"sk"`                       // TOTP_default
	EncryptedSecret string   `dynamodbav:"secret"`                  // base64 AES-256-GCM ciphertext of the TOTP secret
	Secret          string   `dynamodbav:"-"`                       // plaintext TOTP secret — in-memory only, never persisted
	Verified        bool     `dynamodbav:"verified"`
	BackupCodes     []string `dynamodbav:"backup_codes"`            // argon2id hashes
	Version         int64    `dynamodbav:"version"`                 // monotonic; CAS guard for single-use backup codes
	CreatedAt       string   `dynamodbav:"created_at"`
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
