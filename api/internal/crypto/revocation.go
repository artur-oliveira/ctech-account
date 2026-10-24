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

// CheckRevoked answers whether the token's subject was revoked at or after iat.
// An unreachable list follows the family policy (go-common authguard): reads
// (failClosed=false) pass, logged, because authentication must not depend on
// the cache; writes (failClosed=true) get jwtverify.ErrRevocationUnavailable.
func (s *JWTService) CheckRevoked(ctx context.Context, sub string, iat int64, failClosed bool) error {
	if s.revocation == nil {
		return nil
	}
	err := jwtverify.CheckRevoked(ctx, s.revocation, sub, iat)
	if errors.Is(err, jwtverify.ErrRevocationUnavailable) && !failClosed {
		observability.Warn(ctx, "account: revocation check skipped", err)
		return nil
	}
	return err
}
