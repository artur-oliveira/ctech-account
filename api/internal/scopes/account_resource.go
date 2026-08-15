package scopes

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
)

const (
	AccountResourceID        = "account"
	SystemAccountPublisher   = "system://ctech-account"
	AccountProfileRead       = "account:profile:read"
	AccountProfileWrite      = "account:profile:write"
	AccountSecurityWrite     = "account:security:write"
	AccountSessionsRead      = "account:sessions:read"
	AccountSessionsRevoke    = "account:sessions:revoke"
	AccountActivityRead      = "account:activity:read"
	AccountAPIKeysRead       = "account:api-keys:read"
	AccountAPIKeysWrite      = "account:api-keys:write"
	AccountOAuthClientsRead  = "account:oauth-clients:read"
	AccountOAuthClientsWrite = "account:oauth-clients:write"
	AccountConsentsRead      = "account:consents:read"
	AccountConsentsRevoke    = "account:consents:revoke"
	AccountMFARead           = "account:mfa:read"
	AccountMFAWrite          = "account:mfa:write"
	AccountKYCRead           = "account:kyc:read"
	AccountKYCWrite          = "account:kyc:write"
	AccountTermsWrite        = "account:terms:write"
)

//go:embed account-scope-manifest.json
var accountManifestJSON []byte

// AccountManifest returns a fresh copy of the Resource Server contract owned
// by this repository. OIDC scopes deliberately remain outside this manifest.
func AccountManifest() (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(accountManifestJSON, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decoding embedded Account scope manifest: %w", err)
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, fmt.Errorf("validating embedded Account scope manifest: %w", err)
	}
	return manifest, nil
}

// AccountUserScopes is the complete permission set the trusted Account SPA
// needs for its self-service routes. A new slice is returned on every call.
func AccountUserScopes() []string {
	result := []string{
		AccountProfileRead,
		AccountProfileWrite,
		AccountSecurityWrite,
		AccountSessionsRead,
		AccountSessionsRevoke,
		AccountActivityRead,
		AccountAPIKeysRead,
		AccountAPIKeysWrite,
		AccountOAuthClientsRead,
		AccountOAuthClientsWrite,
		AccountConsentsRead,
		AccountConsentsRevoke,
		AccountMFARead,
		AccountMFAWrite,
		AccountKYCRead,
		AccountKYCWrite,
		AccountTermsWrite,
	}
	sort.Strings(result)
	return result
}

// AccountPublicScopes returns the public active subset advertised by RFC 9728.
func AccountPublicScopes() ([]string, error) {
	manifest, err := AccountManifest()
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(manifest.Scopes))
	for _, scope := range manifest.Scopes {
		if scope.Visibility == VisibilityPublic && scope.Status == StatusActive {
			result = append(result, scope.Scope)
		}
	}
	sort.Strings(result)
	return result, nil
}
