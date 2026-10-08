package crypto

import (
	"context"
	"errors"

	commoncache "gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/jwtverify"
	"gopkg.aoctech.app/api-commons/observability"
)

// SetRevocation enables the account-lock revocation check on this service's
// own API. Call once at startup, before serving.
func (s *JWTService) SetRevocation(c commoncache.Backend) { s.revocation = c }

// CheckRevoked fails open when Valkey is unreachable (logged), like
// jwtverify.VerifyClaims: authentication must not depend on the cache.
func (s *JWTService) CheckRevoked(ctx context.Context, sub string, iat int64) error {
	if s.revocation == nil {
		return nil
	}
	err := jwtverify.CheckRevoked(ctx, s.revocation, sub, iat)
	if errors.Is(err, jwtverify.ErrRevocationUnavailable) {
		observability.Warn(ctx, "account: revocation check skipped", err)
		return nil
	}
	return err
}
