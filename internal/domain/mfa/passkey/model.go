package passkey

import (
	"encoding/hex"
	"strings"

	"github.com/go-webauthn/webauthn/webauthn"
)

// Credential wraps a webauthn.Credential for DynamoDB persistence alongside display metadata.
type Credential struct {
	PK             string   `dynamodbav:"pk"`                   // USER_{userID}
	SK             string   `dynamodbav:"sk"`                   // PASSKEY_{credentialIDHex}
	Name           string   `dynamodbav:"name"`                 // user-supplied friendly name
	CredentialJSON string   `dynamodbav:"credential_json"`      // JSON-encoded webauthn.Credential
	Transports     []string `dynamodbav:"transports,omitempty"` // ["usb", "nfc", "ble", "internal"]
	AAGUID         string   `dynamodbav:"aaguid,omitempty"`     // authenticator class ID (hex)
	CreatedAt      string   `dynamodbav:"created_at"`
	LastUsedAt     string   `dynamodbav:"last_used_at,omitempty"`
}

// BuildPK builds the DynamoDB partition key for a user's passkey items.
func BuildPK(userID string) string { return "USER_" + userID }

// BuildSK builds the sort key from a WebAuthn credential ID (raw bytes).
func BuildSK(credentialID []byte) string { return "PASSKEY_" + hex.EncodeToString(credentialID) }

func (c *Credential) UserID() string { return strings.TrimPrefix(c.PK, "USER_") }

// CredentialIDHex returns the hex-encoded credential ID extracted from the sort key.
func (c *Credential) CredentialIDHex() string { return strings.TrimPrefix(c.SK, "PASSKEY_") }

// WebAuthnUser implements webauthn.User so a user's credentials can be passed to the go-webauthn library.
type WebAuthnUser struct {
	ID          []byte
	Name        string
	DisplayName string
	Credentials []webauthn.Credential
}

func (u *WebAuthnUser) WebAuthnID() []byte                         { return u.ID }
func (u *WebAuthnUser) WebAuthnName() string                       { return u.Name }
func (u *WebAuthnUser) WebAuthnDisplayName() string                { return u.DisplayName }
func (u *WebAuthnUser) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }
