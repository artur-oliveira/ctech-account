package consent

import "strings"

// Grant records a user's approval of an OAuth client's scopes. Stored in the
// sessions table (pk USER_{user_id}, sk CONSENT_{client_id}) without an
// expires_at attribute, so DynamoDB TTL never deletes it — a grant lives until
// the user revokes it.
type Grant struct {
	PK        string   `dynamodbav:"pk"` // USER_{user_id}
	SK        string   `dynamodbav:"sk"` // CONSENT_{client_id}
	Scopes    []string `dynamodbav:"scopes"`
	CreatedAt string   `dynamodbav:"created_at"`
	UpdatedAt string   `dynamodbav:"updated_at"`
}

const skPrefix = "CONSENT_"

func BuildPK(userID string) string {
	return "USER_" + userID
}

func BuildSK(clientID string) string {
	return skPrefix + clientID
}

func (g *Grant) UserID() string {
	return strings.TrimPrefix(g.PK, "USER_")
}

func (g *Grant) ClientID() string {
	return strings.TrimPrefix(g.SK, skPrefix)
}

// Covers reports whether every requested scope was already granted.
func (g *Grant) Covers(requested []string) bool {
	granted := make(map[string]struct{}, len(g.Scopes))
	for _, s := range g.Scopes {
		granted[s] = struct{}{}
	}
	for _, s := range requested {
		if _, ok := granted[s]; !ok {
			return false
		}
	}
	return true
}
