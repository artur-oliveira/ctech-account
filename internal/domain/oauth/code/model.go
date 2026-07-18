package code

type AuthCode struct {
	UserID              string   `json:"user_id"`
	SessionID           string   `json:"session_id"`
	ClientID            string   `json:"client_id"`
	RedirectURI         string   `json:"redirect_uri"`
	Scopes              []string `json:"scopes"`
	CodeChallenge       string   `json:"code_challenge"`
	CodeChallengeMethod string   `json:"code_challenge_method"`
	MFAVerified         bool     `json:"mfa_verified"`
	Nonce               string   `json:"nonce,omitempty"`
	CreatedAt           string   `json:"created_at"`
}
