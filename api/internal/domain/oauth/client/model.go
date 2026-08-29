package client

import (
	"net/url"
	"strings"
)

type OAuthClient struct {
	PK               string   `dynamodbav:"pk"`
	Name             string   `dynamodbav:"name"`
	ClientSecretHash string   `dynamodbav:"client_secret_hash,omitempty"`
	ClientType       string   `dynamodbav:"client_type"` // public | confidential
	RedirectURIs     []string `dynamodbav:"redirect_uris"`
	AllowedScopes    []string `dynamodbav:"allowed_scopes"`
	// Audience lists resource server identifiers (URLs or client IDs) embedded in issued access tokens.
	// Defaults to []string{clientID} when empty — set to the actual service URL for inter-service tokens.
	Audience []string `dynamodbav:"audience,omitempty"`
	// FirstParty marks clients operated by the platform itself (accounts UI, dfe).
	// They skip the consent screen. NEVER settable through the self-service API —
	// only via manual/admin seeding, otherwise any user could bypass consent.
	FirstParty  bool   `dynamodbav:"first_party,omitempty"`
	OwnerUserID string `dynamodbav:"owner_user_id"`
	// ManagedResourceID binds a dedicated scope-registry publisher to exactly
	// one Resource Server. It is operator-only and never exposed by self-service
	// client creation/update endpoints.
	ManagedResourceID string `dynamodbav:"managed_resource_id,omitempty"`
	CreatedAt         string `dynamodbav:"created_at"`
	UpdatedAt         string `dynamodbav:"updated_at"`
}

func BuildPK(clientID string) string {
	return "CLIENT_" + clientID
}

func (c *OAuthClient) ID() string {
	return strings.TrimPrefix(c.PK, "CLIENT_")
}

// EffectiveAudience returns the configured audience list, or []string{clientID} if none is set.
func (c *OAuthClient) EffectiveAudience() []string {
	if len(c.Audience) > 0 {
		return c.Audience
	}
	return []string{c.ID()}
}

func (c *OAuthClient) IsPublic() bool {
	return c.ClientType == "public"
}

func (c *OAuthClient) IsRedirectURIAllowed(uri string) bool {
	for _, allowed := range c.RedirectURIs {
		if allowed == uri {
			return true
		}
	}
	return false
}

// IsRegisteredOrigin reports whether uri shares a scheme+host with at least one
// registered redirect_uri.
//
// Two callers: RP-initiated logout's post_logout_redirect_uri, and the
// organization handoff's return_to. Neither has its own registration list on
// this model, and reusing the callback origin prevents an open redirect in both
// without adding client configuration. One implementation on purpose — a second
// copy of an origin check is how the two drift apart and one ends up
// permissive.
func (c *OAuthClient) IsRegisteredOrigin(uri string) bool {
	target, err := url.Parse(uri)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return false
	}
	for _, allowed := range c.RedirectURIs {
		a, err := url.Parse(allowed)
		if err != nil {
			continue
		}
		if a.Scheme == target.Scheme && a.Host == target.Host {
			return true
		}
	}
	return false
}

func (c *OAuthClient) HasScope(scope string) bool {
	for _, s := range c.AllowedScopes {
		if s == scope {
			return true
		}
	}
	return false
}

// FilterScopes returns only the requested scopes that are allowed for this client.
func (c *OAuthClient) FilterScopes(requested []string) []string {
	result := make([]string, 0, len(requested))
	for _, s := range requested {
		if c.HasScope(s) {
			result = append(result, s)
		}
	}
	return result
}
