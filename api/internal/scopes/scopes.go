// Package scopes defines the scope grammar shared by OAuth clients and API keys.
//
// Two families exist:
//
//   - OIDC scopes: openid, profile, email — identity claims for humans.
//   - Service scopes: service:resource:action (e.g. dfe:nfes:write) — permissions
//     on a downstream resource server. The identity provider validates only the
//     grammar and grantability; the named service enforces the semantics.
package scopes

import "regexp"

// OIDC scopes understood natively by this identity provider.
const (
	OpenID  = "openid"
	Profile = "profile"
	Email   = "email"
	// KYC exposes the kyc_level claim (identity verification level) in tokens
	// and userinfo. Identity-family scope: consentable, human sign-in only.
	KYC = "kyc"
)

// oidcScopes is the set of identity scopes that bypass the service grammar.
var oidcScopes = map[string]struct{}{
	OpenID:  {},
	Profile: {},
	Email:   {},
	KYC:     {},
}

// servicePattern matches service:resource:action (action optional →
// service:resource grants all actions on the resource). Segments are
// lowercase alphanumerics with inner hyphens/underscores; '*' is allowed as
// a full segment wildcard for resource or action (e.g. dfe:*:read, dfe:nfe:*).
var servicePattern = regexp.MustCompile(
	`^[a-z0-9][a-z0-9_-]*:(\*|[a-z0-9][a-z0-9_-]*)(:(\*|[a-z0-9][a-z0-9_-]*))?$`,
)

// IsOIDC reports whether s is one of the built-in identity scopes.
func IsOIDC(s string) bool {
	_, ok := oidcScopes[s]
	return ok
}

// IsValid reports whether s is a well-formed scope (OIDC or service grammar).
func IsValid(s string) bool {
	return IsOIDC(s) || servicePattern.MatchString(s)
}

// Validate returns the first malformed scope in ss, or "" when all are valid.
func Validate(ss []string) string {
	for _, s := range ss {
		if !IsValid(s) {
			return s
		}
	}
	return ""
}
