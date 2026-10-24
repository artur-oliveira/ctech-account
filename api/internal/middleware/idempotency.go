package middleware

import (
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/idempotency"
)

// IdempotencyTable is the DynamoDB table (after "{prefix}_") holding claims and
// replayable answers; TTL on `ttl`, created in cdk/lib/dynamodb-stack.ts.
const IdempotencyTable = "account_idempotency"

// Idempotency honors an Idempotency-Key on the route it guards (go-common
// idempotency). The key is OPTIONAL: no key, no protection, no behavior change
// for existing clients. Run it after RequireAuth; the scope is the verified user
// plus scopeExtra (e.g. the organization in the path), so keys never collide
// across users or organizations. Fail closed: with a key and the store down the
// answer is a retryable 503, never an unprotected run.
//
// Only guard routes whose answer holds no secret: the stored answer is replayed
// verbatim and kept in DynamoDB for 24 h.
func Idempotency(store idempotency.Store, scopeExtra func(fiber.Ctx) string) fiber.Handler {
	return idempotency.New(idempotency.Config{
		Store: store,
		Scope: func(c fiber.Ctx) (string, bool) {
			user := GetUserID(c)
			if scopeExtra != nil {
				return user + "#" + scopeExtra(c), user != ""
			}
			return user, user != ""
		},
		Methods: []string{fiber.MethodPost},
	})
}
