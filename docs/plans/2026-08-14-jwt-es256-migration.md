# JWT RS256 → ES256 Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:
> executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add ECDSA P-256 (ES256) signing support to ctech-account's JWT issuance and to the shared `jwtverify` library
so access/ID tokens can be cut over from RS256 to ES256 without breaking `ctech-dfe` or `ctech-wallet`, then perform the
cutover.

**Architecture:** Both `ctech-account`'s own key material (`internal/keystore`) and its signing/verification code
(`internal/crypto/jwt.go`) generalize from a hardcoded `*rsa.PrivateKey` to `crypto.Signer` + an explicit `Alg` field
(`"RS256"` | `"ES256"`), carried alongside the key everywhere it's stored or looked up by `kid`. The shared
`ctech-go-common/jwtverify` library (consumed by `ctech-dfe` and `ctech-wallet`) is generalized the same way on the
verification side. Every verification path resolves the expected algorithm **from the `kid`**, not from the token's own
`alg` header, so a token can never claim a different algorithm than the key it was actually issued under (this is the
alg-confusion defense, and it's what makes the algorithm swap safe to do live). The shared library ships and rolls out
to all consumers *before* ctech-account ever signs an ES256 token, so no consumer is ever asked to validate an algorithm
it doesn't understand yet.

**Tech Stack:** Go 1.26, `github.com/golang-jwt/jwt/v5`, AWS SSM (Parameter Store, `SecureString`), Valkey (rotation
lock), Fiber v3.

**Spec:** No separate spec document — this plan was derived directly from reading the current implementation
(`ctech-account/api/internal/crypto/jwt.go`, `ctech-account/api/internal/keystore/*.go`,
`ctech-go-common/jwtverify/verifier.go`) and is self-contained; the Architecture section above is the design.

## Global Constraints

- `crypto.JWTService` today signs **RS256 only. No HS256, no `SECRET_KEY`.** (`api/CLAUDE.md`) — this plan updates that
  line to "RS256 and ES256; no HS256, no `SECRET_KEY`" as part of Task 8's documentation update, alongside the actual
  capability.
- Go 1.26; `errors.AsType[*T]` generic errors — do not downgrade.
- Fiber v3: use `c.Context()`, never `c.UserContext()`.
- Every domain package exports a `Repository` interface; services take interfaces, never concretes (unaffected by this
  plan — no repository changes).
- Every core function must have an integration/unit test (project testing policy).
- **Mandatory Documentation Policy:** every behavior/config/security change ships its doc update in the same change —
  enforced here as Task 8, not deferred.
- No magic strings: algorithm names must be named constants (`keystore.AlgRS256`, `keystore.AlgES256`), not inline
  `"RS256"`/`"ES256"` literals, except in `wellknown.go`'s already-literal OIDC metadata map (matches existing style
  there).
- JWKS/KID rotation impacts all downstream services — cross-project impact must be stated for `ctech-dfe` and
  `ctech-wallet` (stated per-task below; net effect is "redeploy with a bumped dependency, zero code changes").

---

## Task 1: `ctech-go-common/jwtverify` — accept RSA and EC (ES256) keys

**Why first:** this is the verification side used by every downstream consumer. It must support both algorithms *before*
ctech-account ever issues an ES256 token, or the first ES256 token minted would be unverifiable anywhere downstream.

**Files:**

- Modify: `/home/artur/Documents/Projects/Ctech/ctech-go-common/jwtverify/verifier.go`
- Test: `/home/artur/Documents/Projects/Ctech/ctech-go-common/jwtverify/verifier_test.go` (create if it doesn't already
  exist — check first with `ls`)

**Interfaces:**

- Produces: `jwkToKey(k *jwk) (crypto.PublicKey, jwt.SigningMethod, error)` — new; replaces `jwkToRSA` as the single
  dispatch point. `(v *Verifier) keyForKID(ctx, kid) (crypto.PublicKey, jwt.SigningMethod, error)` — signature changed
  (was `(*rsa.PublicKey, error)`). No change to `VerifyClaims`'s public signature or the `Claims` struct.

- [ ] **Step 1: Check for an existing test file and read current coverage**

Run: `ls /home/artur/Documents/Projects/Ctech/ctech-go-common/jwtverify/`

If `verifier_test.go` exists, read it fully before writing new tests below — extend its existing `cache.Backend` test
double / JWKS test-server helpers rather than duplicating them. If it doesn't exist, the steps below build the minimal
fixtures needed.

- [ ] **Step 2: Write the failing tests**

Add to `verifier_test.go` (create the file with this content if none exists; otherwise add these functions and reuse any
existing in-memory `cache.Backend` fake / JWKS test-server helper already present):

```go
package jwtverify

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// memCache is a minimal in-memory cache.Backend fake for these tests.
type memCache struct{ data map[string][]byte }

func newMemCache() *memCache { return &memCache{data: map[string][]byte{}} }

func (m *memCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	v, ok := m.data[key]
	return v, ok, nil
}

func (m *memCache) Set(_ context.Context, key string, value []byte, _ int) error {
	m.data[key] = value
	return nil
}

func (m *memCache) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func ecJWK(t *testing.T, priv *ecdsa.PrivateKey, kid string) jwk {
	t.Helper()
	size := (priv.Curve.Params().BitSize + 7) / 8
	x := make([]byte, size)
	y := make([]byte, size)
	priv.X.FillBytes(x)
	priv.Y.FillBytes(y)
	return jwk{
		Kid: kid, Kty: "EC", Use: "sig", Alg: "ES256", Crv: "P-256",
		X: base64.RawURLEncoding.EncodeToString(x),
		Y: base64.RawURLEncoding.EncodeToString(y),
	}
}

func rsaJWK(t *testing.T, priv *rsa.PrivateKey, kid string) jwk {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes())
	return jwk{Kid: kid, Kty: "RSA", Use: "sig", Alg: "RS256", N: n, E: e}
}

func jwksServer(t *testing.T, keys ...jwk) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwksResponse{Keys: keys})
	}))
}

func TestVerifyClaimsAcceptsES256Token(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating EC key: %v", err)
	}
	srv := jwksServer(t, ecJWK(t, priv, "ec-kid-1"))
	defer srv.Close()

	v := NewVerifier(srv.URL, "", "", newMemCache())

	claims := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"sub": "user-1", "token_use": "access",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	claims.Header["kid"] = "ec-kid-1"
	tokenStr, err := claims.SignedString(priv)
	if err != nil {
		t.Fatalf("signing test token: %v", err)
	}

	got, err := v.VerifyClaims(context.Background(), tokenStr)
	if err != nil {
		t.Fatalf("VerifyClaims returned error for a valid ES256 token: %v", err)
	}
	if got.Sub != "user-1" {
		t.Fatalf("Sub = %q, want %q", got.Sub, "user-1")
	}
}

func TestVerifyClaimsRejectsAlgConfusionRSAKidSignedES256(t *testing.T) {
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating EC key: %v", err)
	}
	// JWKS advertises "shared-kid" as an RSA key.
	srv := jwksServer(t, rsaJWK(t, rsaPriv, "shared-kid"))
	defer srv.Close()

	v := NewVerifier(srv.URL, "", "", newMemCache())

	// Attacker signs with an EC key but claims the RSA kid.
	claims := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"sub": "attacker", "token_use": "access",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	claims.Header["kid"] = "shared-kid"
	tokenStr, err := claims.SignedString(ecPriv)
	if err != nil {
		t.Fatalf("signing test token: %v", err)
	}

	if _, err := v.VerifyClaims(context.Background(), tokenStr); err == nil {
		t.Fatal("VerifyClaims accepted a token signed with the wrong algorithm for its kid")
	}
}

func TestJwkToKeyRejectsUnsupportedKty(t *testing.T) {
	_, _, err := jwkToKey(&jwk{Kid: "x", Kty: "OKP"})
	if err == nil {
		t.Fatal("jwkToKey accepted an unsupported kty")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run:
`cd /home/artur/Documents/Projects/Ctech/ctech-go-common && go test ./jwtverify/... -run 'TestVerifyClaimsAcceptsES256Token|TestVerifyClaimsRejectsAlgConfusionRSAKidSignedES256|TestJwkToKeyRejectsUnsupportedKty' -v`

Expected: FAIL — compile error (`jwk` has no field `Crv`/`X`/`Y`, `jwkToKey` undefined) or, if it compiles by
coincidence, the ES256 test fails with "unsupported JWK metadata".

- [ ] **Step 4: Implement — generalize `jwk` struct and key resolution**

In `/home/artur/Documents/Projects/Ctech/ctech-go-common/jwtverify/verifier.go`:

Replace the imports block:

```go
import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"gopkg.aoctech.app/api-commons/cache"
)
```

Replace the `jwk` struct:

```go
type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}
```

Replace `keyForKID`'s signature and body:

```go
// keyForKID resolves the signing key and its expected algorithm for kid. On
// a cache miss it forces one throttled JWKS refresh so a key rotation at the
// identity provider takes effect immediately instead of after the cache TTL.
// An unresolvable kid is rejected — never silently verified against some
// other key.
func (v *Verifier) keyForKID(ctx context.Context, kid string) (crypto.PublicKey, jwt.SigningMethod, error) {
	keys, err := v.fetchJWKS(ctx, false)
	if err != nil {
		return nil, nil, fmt.Errorf("jwks unavailable: %w", err)
	}
	if k := findKID(keys, kid); k != nil {
		return jwkToKey(k)
	}

	// Unknown kid: the provider may have rotated keys since we cached them.
	keys, err = v.fetchJWKS(ctx, true)
	if err != nil {
		return nil, nil, fmt.Errorf("jwks refresh failed: %w", err)
	}
	if k := findKID(keys, kid); k != nil {
		return jwkToKey(k)
	}
	return nil, nil, fmt.Errorf("no signing key for kid %q", kid)
}
```

Replace `jwkToRSA` with `jwkToKey` + two type-specific helpers:

```go
// jwkToKey converts a JWK to its Go public key and the signing method a
// token using this kid must be signed with. Only RSA and EC (P-256) keys are
// accepted; anything else is rejected outright rather than silently ignored,
// so an unsupported or malformed JWK never opens a verification gap.
func jwkToKey(k *jwk) (crypto.PublicKey, jwt.SigningMethod, error) {
	if k.Kid == "" || (k.Use != "" && k.Use != "sig") {
		return nil, nil, fmt.Errorf("unsupported JWK metadata for kid %q", k.Kid)
	}
	switch k.Kty {
	case "RSA":
		if k.Alg != "" && k.Alg != jwt.SigningMethodRS256.Alg() {
			return nil, nil, fmt.Errorf("unsupported JWK metadata for kid %q", k.Kid)
		}
		pub, err := jwkToRSA(k)
		return pub, jwt.SigningMethodRS256, err
	case "EC":
		if k.Alg != "" && k.Alg != jwt.SigningMethodES256.Alg() {
			return nil, nil, fmt.Errorf("unsupported JWK metadata for kid %q", k.Kid)
		}
		pub, err := jwkToEC(k)
		return pub, jwt.SigningMethodES256, err
	default:
		return nil, nil, fmt.Errorf("unsupported JWK kty %q for kid %q", k.Kty, k.Kid)
	}
}

func jwkToRSA(k *jwk) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("jwk: decode N: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("jwk: decode E: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := int(new(big.Int).SetBytes(eBytes).Int64())
	return &rsa.PublicKey{N: n, E: e}, nil
}

func jwkToEC(k *jwk) (*ecdsa.PublicKey, error) {
	if k.Crv != "P-256" {
		return nil, fmt.Errorf("jwk: unsupported curve %q for kid %q", k.Crv, k.Kid)
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, fmt.Errorf("jwk: decode X: %w", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, fmt.Errorf("jwk: decode Y: %w", err)
	}
	return &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}, nil
}
```

Update `VerifyClaims` (the `keyForKID` call and the `jwt.Parse` callback):

```go
	pubKey, method, err := v.keyForKID(ctx, kid)
	if err != nil {
		return nil, err
	}

	var parseOpts []jwt.ParserOption
	if v.audience != "" {
		parseOpts = append(parseOpts, jwt.WithAudience(v.audience))
	}
	if v.issuer != "" {
		parseOpts = append(parseOpts, jwt.WithIssuer(v.issuer))
	}
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		// Cross-check against the algorithm THIS kid was resolved to — never
		// trust the token's own alg header alone (alg-confusion guard).
		if t.Method.Alg() != method.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return pubKey, nil
	}, append(parseOpts, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg(), jwt.SigningMethodES256.Alg()}))...)
	if err != nil {
		return nil, err
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/artur/Documents/Projects/Ctech/ctech-go-common && go test ./jwtverify/... -v`

Expected: PASS, including the pre-existing RS256 tests in this package (they must still pass unchanged — this is a pure
generalization, no behavior change for RSA-only JWKS).

- [ ] **Step 6: Full package build check**

Run: `cd /home/artur/Documents/Projects/Ctech/ctech-go-common && go build ./... && go vet ./...`

Expected: clean.

- [ ] **Step 7: Commit**

```bash
cd /home/artur/Documents/Projects/Ctech/ctech-go-common
git add jwtverify/verifier.go jwtverify/verifier_test.go
git commit -m "feat(jwtverify): accept ES256 (EC P-256) keys alongside RS256"
```

Bump the module's version per this repo's usual release process (tag, changelog, etc. — follow whatever
`ctech-go-common` already does for releases; this plan does not prescribe a new one). Note the resulting version for
Task 2 (this plan assumes it becomes `v1.6.0`; substitute the actual tag).

---

## Task 2: Roll `jwtverify` v1.6.0 out to `ctech-dfe` and `ctech-wallet`

**Why:** every consumer must be running code that accepts *both* algorithms before ctech-account issues its first ES256
token, or that consumer rejects the new tokens outright. Neither repo's own code changes —
`ctech-dfe/api/internal/middleware/auth.go:71` and `ctech-wallet/api/internal/middleware/auth.go:36` only call
`v.VerifyClaims(...)`, which keeps its signature.

**Files:**

- Modify: `/home/artur/Documents/Projects/Ctech/ctech-dfe/api/go.mod` (bump `gopkg.aoctech.app/api-commons` from
  `v1.5.0` to `v1.6.0`)
- Modify: `/home/artur/Documents/Projects/Ctech/ctech-wallet/api/go.mod` (same bump)

**Interfaces:**

- Consumes: `jwtverify.Verifier.VerifyClaims` — unchanged signature from Task 1.

- [ ] **Step 1: Bump and tidy ctech-dfe**

```bash
cd /home/artur/Documents/Projects/Ctech/ctech-dfe/api
go get gopkg.aoctech.app/api-commons@v1.6.0
go mod tidy
```

- [ ] **Step 2: Run ctech-dfe's existing auth middleware tests**

Run: `cd /home/artur/Documents/Projects/Ctech/ctech-dfe/api && go test ./internal/middleware/... -v`

Expected: PASS, unchanged — this is a dependency bump with no behavior change (JWKS served by ctech-account is still
100% RSA at this point in the migration).

- [ ] **Step 3: Bump and tidy ctech-wallet**

```bash
cd /home/artur/Documents/Projects/Ctech/ctech-wallet/api
go get gopkg.aoctech.app/api-commons@v1.6.0
go mod tidy
```

- [ ] **Step 4: Run ctech-wallet's existing auth middleware tests**

Run: `cd /home/artur/Documents/Projects/Ctech/ctech-wallet/api && go test ./internal/middleware/... -v`

Expected: PASS, unchanged.

- [ ] **Step 5: Commit each repo separately**

```bash
cd /home/artur/Documents/Projects/Ctech/ctech-dfe
git add api/go.mod api/go.sum
git commit -m "chore(deps): bump api-commons to v1.6.0 (ES256 JWT support)"
```

```bash
cd /home/artur/Documents/Projects/Ctech/ctech-wallet
git add api/go.mod api/go.sum
git commit -m "chore(deps): bump api-commons to v1.6.0 (ES256 JWT support)"
```

- [ ] **Step 6: Deploy both services and confirm healthy**

Deploy `ctech-dfe/api` and `ctech-wallet/api` through their normal pipeline. After rollout, hit each service's health
check and confirm existing RS256-authenticated requests still succeed (e.g. an authenticated smoke-test call through
each service's existing auth-required route). **Do not proceed to Task 9 (the production cutover) until both are
confirmed deployed and healthy** — this is the gate that makes the cutover safe.

---

## Task 3: `ctech-account/internal/keystore` — generalize `Key` to `crypto.Signer` + `Alg`

**Files:**

- Modify: `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/keystore/key.go`
- Test: `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/keystore/key_test.go`

**Interfaces:**

- Consumes: nothing outside stdlib.
- Produces: `Key{KID string; Alg string; Private crypto.Signer; CreatedAt time.Time}`,
  `KeyJSON{KID, Alg, PEM, CreatedAt string}`, `Generate(now time.Time, alg string) (*Key, error)`,
  `DeriveKID(pub crypto.PublicKey) (string, error)`, `AlgRS256 = "RS256"`, `AlgES256 = "ES256"` constants. Tasks 4, 5, 6
  consume these exact names.

- [ ] **Step 1: Write the failing tests**

Read the existing `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/keystore/key_test.go` first (it has
`TestGenerateRoundTripsThroughJSON` and `TestDeriveKIDFullSHA256` today — Step 4 below rewrites
`TestGenerateRoundTripsThroughJSON` to be table-driven over both algorithms instead of adding a parallel EC-only test,
to avoid duplicating the round-trip assertions). Add:

```go
func TestGenerateES256ProducesECKey(t *testing.T) {
	key, err := Generate(time.Now(), AlgES256)
	if err != nil {
		t.Fatalf("Generate(ES256): %v", err)
	}
	if key.Alg != AlgES256 {
		t.Fatalf("Alg = %q, want %q", key.Alg, AlgES256)
	}
	if _, ok := key.Private.Public().(*ecdsa.PublicKey); !ok {
		t.Fatalf("Private.Public() is %T, want *ecdsa.PublicKey", key.Private.Public())
	}
}

func TestParseKeyAcceptsLegacyPKCS1RSAWireFormat(t *testing.T) {
	// Regression: keys already stored in SSM before this migration are PKCS1
	// RSA with no "alg" field. ParseKey must keep reading them unchanged.
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}
	legacyJSON := KeyJSON{
		KID:       "legacy-kid",
		PEM:       string(pem.EncodeToMemory(block)),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		// Alg deliberately left empty — legacy wire format never had this field.
	}

	key, err := ParseKey(legacyJSON)
	if err != nil {
		t.Fatalf("ParseKey(legacy PKCS1): %v", err)
	}
	if key.Alg != AlgRS256 {
		t.Fatalf("Alg = %q, want %q", key.Alg, AlgRS256)
	}
	if _, ok := key.Private.Public().(*rsa.PublicKey); !ok {
		t.Fatalf("Private.Public() is %T, want *rsa.PublicKey", key.Private.Public())
	}
}

func TestParseKeyRejectsAlgMismatch(t *testing.T) {
	key, err := Generate(time.Now(), AlgES256)
	if err != nil {
		t.Fatalf("Generate(ES256): %v", err)
	}
	j, err := key.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	j.Alg = AlgRS256 // corrupt: claims RSA but the PEM is an EC key

	if _, err := ParseKey(j); err == nil {
		t.Fatal("ParseKey accepted a KeyJSON whose stored Alg does not match its key material")
	}
}
```

Rewrite `TestGenerateRoundTripsThroughJSON` to be table-driven (replacing the existing RSA-only body):

```go
func TestGenerateRoundTripsThroughJSON(t *testing.T) {
	for _, alg := range []string{AlgRS256, AlgES256} {
		t.Run(alg, func(t *testing.T) {
			k, err := Generate(time.Now(), alg)
			if err != nil {
				t.Fatalf("Generate(%s): %v", alg, err)
			}
			j, err := k.ToJSON()
			if err != nil {
				t.Fatalf("ToJSON: %v", err)
			}
			back, err := ParseKey(j)
			if err != nil {
				t.Fatalf("ParseKey: %v", err)
			}
			if back.KID != k.KID || back.Alg != k.Alg {
				t.Fatalf("round trip mismatch: got KID=%s Alg=%s, want KID=%s Alg=%s", back.KID, back.Alg, k.KID, k.Alg)
			}
			switch alg {
			case AlgRS256:
				origPub := k.Private.Public().(*rsa.PublicKey)
				gotPub := back.Private.Public().(*rsa.PublicKey)
				if origPub.N.Cmp(gotPub.N) != 0 {
					t.Fatal("RSA public key N did not round-trip")
				}
			case AlgES256:
				origPub := k.Private.Public().(*ecdsa.PublicKey)
				gotPub := back.Private.Public().(*ecdsa.PublicKey)
				if origPub.X.Cmp(gotPub.X) != 0 || origPub.Y.Cmp(gotPub.Y) != 0 {
					t.Fatal("EC public key X/Y did not round-trip")
				}
			}
			if len(k.KID) != 64 {
				t.Fatalf("KID length = %d, want 64 (full SHA-256 hex)", len(k.KID))
			}
		})
	}
}
```

Add `"crypto/ecdsa"` to the test file's imports.

- [ ] **Step 2: Run tests to verify they fail**

Run:
`cd /home/artur/Documents/Projects/Ctech/ctech-account/api && go test ./internal/keystore/... -run 'TestGenerateES256ProducesECKey|TestParseKeyAcceptsLegacyPKCS1RSAWireFormat|TestParseKeyRejectsAlgMismatch|TestGenerateRoundTripsThroughJSON' -v`

Expected: FAIL — compile errors (`AlgES256`/`AlgRS256` undefined, `Generate` takes 1 arg not 2).

- [ ] **Step 3: Implement — rewrite `key.go`**

Replace the full content of `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/keystore/key.go`:

```go
// Package keystore manages the versioned RS256/ES256 signing keys: material
// types, SSM-backed storage (jwk/active + jwk/previous) and automatic
// rotation.
package keystore

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"time"
)

const (
	rsaKeyBits = 3072

	// AlgRS256 and AlgES256 are the only signing algorithms this service
	// supports. Every Key carries one of these two values.
	AlgRS256 = "RS256"
	AlgES256 = "ES256"
)

// Key is a signing key with its metadata. Private is *rsa.PrivateKey when
// Alg == AlgRS256, or *ecdsa.PrivateKey when Alg == AlgES256 — Generate and
// ParseKey are the only two constructors, and both enforce this invariant.
type Key struct {
	KID       string
	Alg       string
	Private   crypto.Signer
	CreatedAt time.Time
}

// KeyJSON is the SSM wire format for a signing key. Alg is absent ("") on
// keys stored before this migration — the pre-ES256 wire format was always
// PKCS1 RSA with no algorithm field.
type KeyJSON struct {
	KID       string `json:"kid"`
	Alg       string `json:"alg,omitempty"`
	PEM       string `json:"pem"`
	CreatedAt string `json:"created_at"`
}

// DeriveKID returns the full SHA-256 hex (256 bits) over the PKIX public key
// DER — the same scheme config.loadSigningKey has always used, so wrapping
// the legacy key preserves its KID. SEC-044: previously truncated to 64
// bits; now the full digest is used for collision resistance. This only
// affects newly derived KIDs; existing keys keep their explicitly stored KID.
func DeriveKID(pub crypto.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("marshaling public key: %w", err)
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}

// Generate creates a new signing key of the given algorithm, stamped with
// now. alg must be AlgRS256 or AlgES256.
func Generate(now time.Time, alg string) (*Key, error) {
	var signer crypto.Signer
	switch alg {
	case AlgRS256:
		priv, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
		if err != nil {
			return nil, fmt.Errorf("generating RSA key: %w", err)
		}
		signer = priv
	case AlgES256:
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generating EC key: %w", err)
		}
		signer = priv
	default:
		return nil, fmt.Errorf("unsupported signing algorithm %q", alg)
	}
	kid, err := DeriveKID(signer.Public())
	if err != nil {
		return nil, err
	}
	return &Key{KID: kid, Alg: alg, Private: signer, CreatedAt: now.UTC()}, nil
}

// ToJSON serializes the key to its SSM wire format. Always PKCS8 going
// forward (RSA and EC both marshal through the same call) — this is a
// forward-only format change; ParseKey still reads the pre-migration PKCS1
// format for keys already stored in SSM.
func (k *Key) ToJSON() (KeyJSON, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k.Private)
	if err != nil {
		return KeyJSON{}, fmt.Errorf("marshaling key %s: %w", k.KID, err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	return KeyJSON{
		KID:       k.KID,
		Alg:       k.Alg,
		PEM:       string(pem.EncodeToMemory(block)),
		CreatedAt: k.CreatedAt.Format(time.RFC3339),
	}, nil
}

// ParseKey deserializes a key from its SSM wire format. Handles both the
// pre-migration wire format (PEM type "RSA PRIVATE KEY", PKCS1, always RSA,
// no Alg field) and the current format (PEM type "PRIVATE KEY", PKCS8, RSA
// or EC, Alg field set).
func ParseKey(j KeyJSON) (*Key, error) {
	block, _ := pem.Decode([]byte(j.PEM))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM in key %s", j.KID)
	}

	var signer crypto.Signer
	var alg string
	switch block.Type {
	case "RSA PRIVATE KEY":
		priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing key %s: %w", j.KID, err)
		}
		signer, alg = priv, AlgRS256
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing key %s: %w", j.KID, err)
		}
		switch key := parsed.(type) {
		case *rsa.PrivateKey:
			signer, alg = key, AlgRS256
		case *ecdsa.PrivateKey:
			signer, alg = key, AlgES256
		default:
			return nil, fmt.Errorf("key %s: unsupported private key type %T", j.KID, parsed)
		}
	default:
		return nil, fmt.Errorf("key %s: unsupported PEM block type %q", j.KID, block.Type)
	}
	if j.Alg != "" && j.Alg != alg {
		return nil, fmt.Errorf("key %s: stored alg %q does not match key material %q", j.KID, j.Alg, alg)
	}

	created, err := time.Parse(time.RFC3339, j.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("parsing created_at of key %s: %w", j.KID, err)
	}
	return &Key{KID: j.KID, Alg: alg, Private: signer, CreatedAt: created}, nil
}

// parseLegacyPEM decodes the pre-rotation raw PEM parameter (PKCS#1 or
// PKCS#8) into a Key with its KID derived — identical to
// config.loadSigningKey. Always RSA: this wraps the single key that existed
// before key rotation (and before ES256 support) was introduced.
func parseLegacyPEM(pemStr string, now time.Time) (*Key, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("legacy key: failed to decode PEM block")
	}

	var priv *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		parsed, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("legacy key: parsing PKCS1: %w", err)
		}
		priv = parsed
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("legacy key: parsing PKCS8: %w", err)
		}
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("legacy key: not RSA")
		}
		priv = rsaKey
	default:
		return nil, fmt.Errorf("legacy key: unsupported PEM block type %q", block.Type)
	}

	kid, err := DeriveKID(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	return &Key{KID: kid, Alg: AlgRS256, Private: priv, CreatedAt: now.UTC()}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/artur/Documents/Projects/Ctech/ctech-account/api && go test ./internal/keystore/... -v`

Expected: PASS — including pre-existing `TestDeriveKIDFullSHA256` (signature-compatible: `crypto.PublicKey` accepts
`*rsa.PublicKey` unchanged) and the `ssm_test.go`/`rotator_test.go` tests, which Task 4 will touch next; if any of those
fail to compile right now because they call `Generate(now)` with one argument, that's expected — Task 4 fixes those call
sites. Confirm at minimum that `key_test.go`'s own tests pass in isolation:

Run: `cd /home/artur/Documents/Projects/Ctech/ctech-account/api && go vet ./internal/keystore/... 2>&1 | head -50`

Expected output at this point: compile errors only in `ssm.go`/`ssm_test.go`/`rotator.go` (which call `Generate`
/reference `Key.Private` as `*rsa.PrivateKey`) — that's the known, expected state until Task 4. `key.go`/`key_test.go`
themselves must compile and pass cleanly.

- [ ] **Step 5: Commit**

```bash
cd /home/artur/Documents/Projects/Ctech/ctech-account
git add api/internal/keystore/key.go api/internal/keystore/key_test.go
git commit -m "feat(keystore): generalize Key to crypto.Signer + Alg for ES256 support"
```

(This commit intentionally leaves the package non-building until Task 4 lands — both tasks are part of one PR/branch if
you're following `superpowers:finishing-a-development-branch`; do not merge to main between Task 3 and Task 4.)

---

## Task 4: `ctech-account/internal/keystore` — algorithm-aware rotation

**Files:**

- Modify: `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/keystore/ssm.go`
- Modify: `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/keystore/rotator.go`
- Modify: `/home/artur/Documents/Projects/Ctech/ctech-account/api/cmd/rotatekeys/main.go`
- Test: `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/keystore/ssm_test.go`

**Interfaces:**

- Consumes: `keystore.Key`, `keystore.Generate(now, alg)`, `keystore.AlgRS256`, `keystore.AlgES256` from Task 3.
- Produces: `Rotate(ctx, store, now time.Time, alg string) (*Key, error)` — signature changed (added `alg` param). Task
  9 (the production cutover) invokes this indirectly via `rotatekeys -alg ES256`.

- [ ] **Step 1: Write the failing tests**

Read `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/keystore/ssm_test.go` fully first — it has
`TestRotatePromotesActiveToPrevious` and `TestInitFromLegacyWrapsPEMPreservingKID` using a fake `SSMAPI`. Update the
existing `TestRotatePromotesActiveToPrevious` call site to pass an explicit alg (it currently calls
`Rotate(ctx, store, now)` — add `, AlgRS256` as the new third argument, preserving its current assertions unchanged),
and add:

```go
func TestRotateWithExplicitAlgSwitchesAlgorithm(t *testing.T) {
	fake := newFakeSSMAPI(t) // reuse whatever the existing tests' fake constructor is named
	store := NewStore(fake, "test")

	rsaKey, err := Generate(time.Now(), AlgRS256)
	if err != nil {
		t.Fatalf("Generate(RS256): %v", err)
	}
	if err := store.Save(context.Background(), rsaKey, nil); err != nil {
		t.Fatalf("seeding active RSA key: %v", err)
	}

	newKey, err := Rotate(context.Background(), store, time.Now(), AlgES256)
	if err != nil {
		t.Fatalf("Rotate(ES256): %v", err)
	}
	if newKey.Alg != AlgES256 {
		t.Fatalf("new active Alg = %q, want %q", newKey.Alg, AlgES256)
	}

	active, previous, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if active.Alg != AlgES256 {
		t.Fatalf("active.Alg after rotation = %q, want %q", active.Alg, AlgES256)
	}
	if previous == nil || previous.Alg != AlgRS256 || previous.KID != rsaKey.KID {
		t.Fatalf("previous key after cutover = %+v, want the demoted RSA key %s", previous, rsaKey.KID)
	}
}
```

(Adjust `newFakeSSMAPI(t)` to whatever the actual existing test helper/constructor is named in `ssm_test.go` — read the
file first and reuse it verbatim; do not introduce a second fake.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/artur/Documents/Projects/Ctech/ctech-account/api && go build ./internal/keystore/... 2>&1`

Expected: FAIL to compile — `Rotate` still takes 3 args (ctx, store, now) not 4.

- [ ] **Step 3: Implement — `ssm.go`**

In `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/keystore/ssm.go`, replace the `Rotate` function:

```go
// Rotate generates a new active key using alg and demotes the current active
// to previous. Returns the new key.
func Rotate(ctx context.Context, store *Store, now time.Time, alg string) (*Key, error) {
	active, _, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	newKey, err := Generate(now, alg)
	if err != nil {
		return nil, err
	}
	if err := store.Save(ctx, newKey, active); err != nil {
		return nil, err
	}
	return newKey, nil
}
```

- [ ] **Step 4: Implement — `rotator.go`**

In `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/keystore/rotator.go`, update the `tick` function's
rotation call so the routine 90-day rotation **keeps the current active key's algorithm** (it must never silently change
algorithm on its own — only the explicit CLI cutover in Step 5 does that):

```go
		} else if won {
			newKey, rotErr := Rotate(ctx, cfg.Store, cfg.Now(), active.Alg)
			if rotErr != nil {
				return fmt.Errorf("rotating key: %w", rotErr)
			}
```

(This is the only change in `rotator.go` — everything else in `tick`/`RunRotator` is unchanged.)

- [ ] **Step 5: Implement — `cmd/rotatekeys/main.go`**

Replace the full content of `/home/artur/Documents/Projects/Ctech/ctech-account/api/cmd/rotatekeys/main.go`:

```go
// Command rotatekeys manages the versioned RS256/ES256 signing keys in SSM.
//
//	rotatekeys -env prod -init            # one-time: wrap legacy rsa-private-key into jwk/active (KID preserved)
//	rotatekeys -env prod                  # manual rotation: new active key, same algorithm as the current active key
//	rotatekeys -env prod -alg ES256       # algorithm cutover: new active key on the given algorithm, old active becomes previous
//
// Instances reload keys from SSM every few minutes, so a rotation propagates
// without a deploy; the previous key stays in JWKS until the next rotation.
package main

import (
	"context"
	"flag"
	"log"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"gopkg.aoctech.app/account/api/internal/keystore"
)

func main() {
	env := flag.String("env", "", "environment (e.g. prod)")
	initMode := flag.Bool("init", false, "wrap legacy rsa-private-key parameter into jwk/active")
	algFlag := flag.String("alg", "", "signing algorithm for the new active key (RS256 or ES256); defaults to the current active key's algorithm")
	flag.Parse()
	if *env == "" {
		log.Fatal("-env is required")
	}

	ctx := context.Background()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("loading AWS config: %v", err)
	}
	client := ssm.NewFromConfig(awsCfg)
	store := keystore.NewStore(client, *env)

	if *initMode {
		if err := keystore.InitFromLegacy(ctx, store, client, time.Now()); err != nil {
			log.Fatalf("init: %v", err)
		}
		log.Println("legacy key wrapped into jwk/active (KID preserved)")
		return
	}

	alg := *algFlag
	if alg == "" {
		active, _, loadErr := store.Load(ctx)
		if loadErr != nil {
			log.Fatalf("loading current active key: %v", loadErr)
		}
		alg = active.Alg
	} else if alg != keystore.AlgRS256 && alg != keystore.AlgES256 {
		log.Fatalf("invalid -alg %q: must be %s or %s", alg, keystore.AlgRS256, keystore.AlgES256)
	}

	newKey, err := keystore.Rotate(ctx, store, time.Now(), alg)
	if err != nil {
		log.Fatalf("rotate: %v", err)
	}
	log.Printf("rotated: new active kid=%s alg=%s (instances pick it up within 5m; previous kid stays in JWKS)", newKey.KID, newKey.Alg)
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd /home/artur/Documents/Projects/Ctech/ctech-account/api && go test ./internal/keystore/... -v`

Expected: PASS, all of them — including the updated `TestRotatePromotesActiveToPrevious` and the new
`TestRotateWithExplicitAlgSwitchesAlgorithm`.

Run: `cd /home/artur/Documents/Projects/Ctech/ctech-account/api && go build ./cmd/rotatekeys/...`

Expected: builds clean.

- [ ] **Step 7: Commit**

```bash
cd /home/artur/Documents/Projects/Ctech/ctech-account
git add api/internal/keystore/ssm.go api/internal/keystore/ssm_test.go api/internal/keystore/rotator.go api/cmd/rotatekeys/main.go
git commit -m "feat(keystore): algorithm-aware rotation and rotatekeys -alg cutover flag"
```

---

## Task 5: `ctech-account/internal/config` — generalize dev-mode key loading

**Files:**

- Modify: `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/config/config.go`
- Test: `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/config/config_test.go` (check whether it exists
  first; create if not)

**Interfaces:**

- Consumes: `keystore.AlgRS256`, `keystore.AlgES256`, `keystore.DeriveKID` from Task 3.
- Produces: `Config.SigningKey crypto.Signer` and `Config.SigningKeyAlg string` — replace the old
  `Config.RSAPrivateKey *rsa.PrivateKey` field. Task 6 consumes these two exact field names.

- [ ] **Step 1: Check for an existing config test file**

Run: `ls /home/artur/Documents/Projects/Ctech/ctech-account/api/internal/config/`

- [ ] **Step 2: Write the failing tests**

Add (to the existing test file, or create `config_test.go` with `package config`):

```go
package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"

	"gopkg.aoctech.app/account/api/internal/keystore"
)

func TestLoadSigningKeyFromRSAPrivateKeyEnv(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}
	t.Setenv("RSA_PRIVATE_KEY", string(pem.EncodeToMemory(block)))
	t.Setenv("EC_PRIVATE_KEY", "")

	signer, alg, kid, err := loadSigningKey()
	if err != nil {
		t.Fatalf("loadSigningKey: %v", err)
	}
	if alg != keystore.AlgRS256 {
		t.Fatalf("alg = %q, want %q", alg, keystore.AlgRS256)
	}
	if _, ok := signer.Public().(*rsa.PublicKey); !ok {
		t.Fatalf("signer.Public() is %T, want *rsa.PublicKey", signer.Public())
	}
	if kid == "" {
		t.Fatal("kid must be derived when PUBLIC_KEY_KID is unset")
	}
}

func TestLoadSigningKeyFromECPrivateKeyEnv(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating EC key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshaling EC key: %v", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	t.Setenv("RSA_PRIVATE_KEY", "")
	t.Setenv("EC_PRIVATE_KEY", string(pem.EncodeToMemory(block)))

	signer, alg, _, err := loadSigningKey()
	if err != nil {
		t.Fatalf("loadSigningKey: %v", err)
	}
	if alg != keystore.AlgES256 {
		t.Fatalf("alg = %q, want %q", alg, keystore.AlgES256)
	}
	if _, ok := signer.Public().(*ecdsa.PublicKey); !ok {
		t.Fatalf("signer.Public() is %T, want *ecdsa.PublicKey", signer.Public())
	}
}

func TestLoadSigningKeyRejectsBothEnvVarsSet(t *testing.T) {
	t.Setenv("RSA_PRIVATE_KEY", "dummy")
	t.Setenv("EC_PRIVATE_KEY", "dummy")

	if _, _, _, err := loadSigningKey(); err == nil {
		t.Fatal("loadSigningKey accepted both RSA_PRIVATE_KEY and EC_PRIVATE_KEY set simultaneously")
	}
	_ = os.Unsetenv // keep os imported if unused elsewhere in this file
}

func TestLoadSigningKeyReturnsNilWhenUnset(t *testing.T) {
	t.Setenv("RSA_PRIVATE_KEY", "")
	t.Setenv("EC_PRIVATE_KEY", "")

	signer, alg, kid, err := loadSigningKey()
	if err != nil {
		t.Fatalf("loadSigningKey: %v", err)
	}
	if signer != nil || alg != "" || kid != "" {
		t.Fatalf("expected all-zero return when neither env var is set, got signer=%v alg=%q kid=%q", signer, alg, kid)
	}
}

```

(Drop the `_ = os.Unsetenv` line and the `"os"` import if it turns out unused after you check the rest of the file —
it's only there in case no other test in the file already imports `os`.)

- [ ] **Step 3: Run tests to verify they fail**

Run:
`cd /home/artur/Documents/Projects/Ctech/ctech-account/api && go test ./internal/config/... -run 'TestLoadSigningKey' -v`

Expected: FAIL — `loadSigningKey` undefined (current function is `loadRSAKey`, no `EC_PRIVATE_KEY` support).

- [ ] **Step 4: Implement**

In `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/config/config.go`:

Update imports — add `"crypto"`, `"crypto/ecdsa"`:

```go
import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
	"gopkg.aoctech.app/account/api/internal/keystore"
)
```

In the `Config` struct, replace:

```go
	RSAPrivateKey  *rsa.PrivateKey
	PublicKeyKID   string
```

with:

```go
	SigningKey     crypto.Signer
	SigningKeyAlg  string
	PublicKeyKID   string
```

In `Load()`, replace:

```go
	privateKey, kid, err := loadRSAKey()
	if err != nil {
		return nil, fmt.Errorf("loading RSA key: %w", err)
	}
```

with:

```go
	signingKey, signingAlg, kid, err := loadSigningKey()
	if err != nil {
		return nil, fmt.Errorf("loading signing key: %w", err)
	}
```

Find the `return &Config{...}` literal further down in `Load()` and replace whichever two lines currently set
`RSAPrivateKey: privateKey,` and `PublicKeyKID: kid,` with:

```go
		SigningKey:    signingKey,
		SigningKeyAlg: signingAlg,
		PublicKeyKID:  kid,
```

Replace the `loadRSAKey` function entirely with:

```go
// loadSigningKey parses the dev-mode signing key from RSA_PRIVATE_KEY
// (RS256) or EC_PRIVATE_KEY (ES256) — mutually exclusive; set at most one.
// When neither is set, returns zero values without error — production loads
// versioned keys from SSM instead (see internal/keystore).
func loadSigningKey() (crypto.Signer, string, string, error) {
	rsaPEM := os.Getenv("RSA_PRIVATE_KEY")
	ecPEM := os.Getenv("EC_PRIVATE_KEY")
	if rsaPEM != "" && ecPEM != "" {
		return nil, "", "", fmt.Errorf("RSA_PRIVATE_KEY and EC_PRIVATE_KEY are mutually exclusive")
	}
	if rsaPEM == "" && ecPEM == "" {
		return nil, "", "", nil
	}

	pemStr, alg := rsaPEM, keystore.AlgRS256
	if ecPEM != "" {
		pemStr, alg = ecPEM, keystore.AlgES256
	}

	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, "", "", fmt.Errorf("failed to decode PEM block")
	}

	var signer crypto.Signer
	var err error
	switch alg {
	case keystore.AlgRS256:
		signer, err = parseRSASigner(block)
	case keystore.AlgES256:
		signer, err = parseECSigner(block)
	}
	if err != nil {
		return nil, "", "", fmt.Errorf("parsing %s key: %w", alg, err)
	}

	kid := os.Getenv("PUBLIC_KEY_KID")
	if kid == "" {
		derived, derErr := keystore.DeriveKID(signer.Public())
		if derErr != nil {
			return nil, "", "", derErr
		}
		kid = derived
	}

	return signer, alg, kid, nil
}

func parseRSASigner(block *pem.Block) (crypto.Signer, error) {
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("key is not RSA")
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block type: %s", block.Type)
	}
}

func parseECSigner(block *pem.Block) (crypto.Signer, error) {
	switch block.Type {
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		ecKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("key is not EC")
		}
		return ecKey, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block type: %s", block.Type)
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/artur/Documents/Projects/Ctech/ctech-account/api && go test ./internal/config/... -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/artur/Documents/Projects/Ctech/ctech-account
git add api/internal/config/config.go api/internal/config/config_test.go
git commit -m "feat(config): support EC_PRIVATE_KEY (ES256) alongside RSA_PRIVATE_KEY in dev mode"
```

---

## Task 6: `ctech-account/internal/crypto` — generalize `JWTService` sign/verify/JWKS

**Files:**

- Modify: `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/crypto/jwt.go`
- Test: `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/crypto/jwt_test.go`
- Modify (call sites only): `/home/artur/Documents/Projects/Ctech/ctech-account/api/cmd/api/main.go:82`
  (`cfg.RSAPrivateKey != nil` → `cfg.SigningKey != nil`)

**Interfaces:**

- Consumes: `keystore.Key{KID, Alg, Private crypto.Signer, CreatedAt}`,
  `config.Config{SigningKey, SigningKeyAlg, PublicKeyKID}` from Tasks 3 and 5.
- Produces: unchanged public method signatures on `JWTService` (`SignAccessToken`, `SignIDToken`, `Verify`,
  `PublicKeyJWKs`, `KID`, `Reload`, `AccessTokenTTLSeconds`) — Task 7 (`wellknown.go`) is unaffected by any signature
  change here, only by the JWKS *content* now potentially containing an EC entry.

- [ ] **Step 1: Write the failing tests**

Read `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/crypto/jwt_test.go` fully first (reuse its
existing test-fixture helpers for building a `*JWTService` — likely a helper that builds one from a generated RSA key;
extend it rather than duplicating). Add:

```go
func TestSignAndVerifyES256(t *testing.T) {
	key, err := keystore.Generate(time.Now(), keystore.AlgES256)
	if err != nil {
		t.Fatalf("keystore.Generate(ES256): %v", err)
	}
	cfg := &config.Config{Audience: "test-aud", AppURL: "https://issuer.test"}
	svc, err := NewJWTServiceWithKeys(cfg, key, nil)
	if err != nil {
		t.Fatalf("NewJWTServiceWithKeys: %v", err)
	}

	tok, err := svc.SignAccessToken("user-1", "sess-1", "client-1", []string{"openid"}, "https://issuer.test", []string{"test-aud"}, 0, 0, nil, "")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}

	claims, err := svc.Verify(tok)
	if err != nil {
		t.Fatalf("Verify(ES256 token): %v", err)
	}
	if claims["sub"] != "user-1" {
		t.Fatalf("sub = %v, want user-1", claims["sub"])
	}
}

func TestVerifyRejectsAlgConfusionAcrossActiveAndPrevious(t *testing.T) {
	// active is EC, previous is RSA — a token whose kid names the RSA
	// (previous) key must be rejected if it's actually signed with ES256,
	// and vice versa.
	ecKey, err := keystore.Generate(time.Now(), keystore.AlgES256)
	if err != nil {
		t.Fatalf("keystore.Generate(ES256): %v", err)
	}
	rsaKey, err := keystore.Generate(time.Now(), keystore.AlgRS256)
	if err != nil {
		t.Fatalf("keystore.Generate(RS256): %v", err)
	}
	cfg := &config.Config{Audience: "test-aud", AppURL: "https://issuer.test"}
	svc, err := NewJWTServiceWithKeys(cfg, ecKey, rsaKey)
	if err != nil {
		t.Fatalf("NewJWTServiceWithKeys: %v", err)
	}

	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub": "attacker", "iss": "https://issuer.test", "aud": []string{"test-aud"},
		"token_use": "access", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	// Sign with ES256 but claim the RSA (previous) key's kid.
	forged := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	forged.Header["kid"] = rsaKey.KID
	tokenStr, err := forged.SignedString(ecKey.Private)
	if err != nil {
		t.Fatalf("signing forged token: %v", err)
	}

	if _, err := svc.Verify(tokenStr); err == nil {
		t.Fatal("Verify accepted a token signed with the wrong algorithm for its kid")
	}
}

func TestJWKSRendersBothKeyTypesDuringCutover(t *testing.T) {
	ecKey, err := keystore.Generate(time.Now(), keystore.AlgES256)
	if err != nil {
		t.Fatalf("keystore.Generate(ES256): %v", err)
	}
	rsaKey, err := keystore.Generate(time.Now(), keystore.AlgRS256)
	if err != nil {
		t.Fatalf("keystore.Generate(RS256): %v", err)
	}
	cfg := &config.Config{Audience: "test-aud", AppURL: "https://issuer.test"}
	svc, err := NewJWTServiceWithKeys(cfg, ecKey, rsaKey)
	if err != nil {
		t.Fatalf("NewJWTServiceWithKeys: %v", err)
	}

	jwks := svc.PublicKeyJWKs()
	if len(jwks) != 2 {
		t.Fatalf("len(jwks) = %d, want 2", len(jwks))
	}
	if jwks[0]["kty"] != "EC" || jwks[0]["crv"] != "P-256" || jwks[0]["kid"] != ecKey.KID {
		t.Fatalf("active JWK = %+v, want EC/P-256 kid=%s", jwks[0], ecKey.KID)
	}
	if jwks[1]["kty"] != "RSA" || jwks[1]["kid"] != rsaKey.KID {
		t.Fatalf("previous JWK = %+v, want RSA kid=%s", jwks[1], rsaKey.KID)
	}
}
```

Adjust the `SignAccessToken` call's argument list above if the existing test file already has a shorter test helper
wrapping it — reuse that helper instead of calling the 10-argument method directly, to match this file's existing style.

- [ ] **Step 2: Run tests to verify they fail**

Run:
`cd /home/artur/Documents/Projects/Ctech/ctech-account/api && go test ./internal/crypto/... -run 'TestSignAndVerifyES256|TestVerifyRejectsAlgConfusionAcrossActiveAndPrevious|TestJWKSRendersBothKeyTypesDuringCutover' -v`

Expected: FAIL to compile at this point — `jwt.go` still hardcodes `*rsa.PublicKey`/`jwt.SigningMethodRS256` and
`Key.Private` is now `crypto.Signer` (from Task 3), so `&key.Private.PublicKey` no longer compiles.

- [ ] **Step 3: Implement — rewrite `jwt.go`**

Replace the full content of `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/crypto/jwt.go`:

```go
package crypto

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/keystore"
	"gopkg.aoctech.app/account/api/internal/utils"
)

// JWTService signs with the active key and verifies against active+previous,
// resolved by the token's kid header. Keys are hot-reloadable (Reload) so the
// rotation loop can swap them without a restart. Signs RS256 or ES256
// depending on the active key's Alg; verification accepts either, resolved
// per-kid.
type JWTService struct {
	mu             sync.RWMutex
	active         *keystore.Key
	previous       *keystore.Key // nil until first rotation
	selfAudience   string        // Verify() rejects tokens whose aud doesn't contain this value
	issuer         string        // Verify() rejects tokens whose iss doesn't match this value
	accessTokenTTL time.Duration
	idTokenTTL     time.Duration
}

// NewJWTService wraps the single key loaded by config (RSA_PRIVATE_KEY or
// EC_PRIVATE_KEY env — dev mode, no rotation) as the active key.
func NewJWTService(cfg *config.Config) (*JWTService, error) {
	if cfg.SigningKey == nil {
		return nil, fmt.Errorf("signing key is nil")
	}
	active := &keystore.Key{KID: cfg.PublicKeyKID, Alg: cfg.SigningKeyAlg, Private: cfg.SigningKey, CreatedAt: time.Now().UTC()}
	return NewJWTServiceWithKeys(cfg, active, nil)
}

// NewJWTServiceWithKeys builds the service from explicit key material
// (SSM-loaded in production). previous may be nil.
func NewJWTServiceWithKeys(cfg *config.Config, active, previous *keystore.Key) (*JWTService, error) {
	if active == nil || active.Private == nil {
		return nil, fmt.Errorf("active signing key is nil")
	}
	accessTTL := cfg.AccessTokenTTL
	if accessTTL == 0 {
		accessTTL = 15 * time.Minute
	}
	return &JWTService{
		active:         active,
		previous:       previous,
		selfAudience:   cfg.Audience,
		issuer:         cfg.AppURL,
		accessTokenTTL: accessTTL,
		idTokenTTL:     time.Hour,
	}, nil
}

// Reload swaps the key set. Safe for concurrent use with signing/verification.
func (j *JWTService) Reload(active, previous *keystore.Key) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.active, j.previous = active, previous
}

// SignAccessToken creates a 15-minute JWT access token, signed with the
// active key's algorithm (RS256 or ES256).
// audience identifies the resource server(s) (backend API URLs).
// clientID is the OAuth client_id; set as azp (authorized party) claim.
// authTime/lastMFAAt/amr mirror the session's step-up state (RFC 8176/OIDC);
// zero values are omitted — api_key-derived tokens carry none of them and can
// therefore never pass a step-up check.
// kycLevel is the user's identity verification level; empty omits the claim
// (callers pass it only when the kyc scope was granted).
func (j *JWTService) SignAccessToken(userID, sessionID, clientID string, scopes []string, issuer string, audience []string, authTime, lastMFAAt int64, amr []string, kycLevel string) (string, error) {
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":       userID,
		"sid":       sessionID,
		"scope":     strings.Join(scopes, " "),
		"iss":       issuer,
		"aud":       utils.DeduplicateInPlace(audience),
		"azp":       clientID,
		"token_use": "access",
		"iat":       now.Unix(),
		"exp":       now.Add(j.accessTokenTTL).Unix(),
	}
	if authTime > 0 {
		claims["auth_time"] = authTime
	}
	if lastMFAAt > 0 {
		claims["last_mfa_at"] = lastMFAAt
	}
	if len(amr) > 0 {
		claims["amr"] = amr
	}
	if kycLevel != "" {
		claims["kyc_level"] = kycLevel
	}
	return j.sign(claims)
}

// SignIDToken creates a 1-hour JWT id_token per OIDC spec, signed with the
// active key's algorithm (RS256 or ES256).
// kycLevel is included as the kyc_level claim when non-empty.
func (j *JWTService) SignIDToken(userID, email, name, preferredUsername, givenName, familyName string, emailVerified bool, clientID, nonce, issuer, kycLevel string) (string, error) {
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":                userID,
		"email":              email,
		"name":               name,
		"preferred_username": preferredUsername,
		"given_name":         givenName,
		"family_name":        familyName,
		"iss":                issuer,
		"aud":                []string{clientID},
		"token_use":          "id_token",
		"iat":                now.Unix(),
		"exp":                now.Add(j.idTokenTTL).Unix(),
		"email_verified":     emailVerified,
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	if kycLevel != "" {
		claims["kyc_level"] = kycLevel
	}
	return j.sign(claims)
}

func (j *JWTService) sign(claims jwt.MapClaims) (string, error) {
	j.mu.RLock()
	key := j.active
	j.mu.RUnlock()

	method := jwt.GetSigningMethod(key.Alg)
	if method == nil {
		return "", fmt.Errorf("unsupported signing algorithm %q for kid %s", key.Alg, key.KID)
	}
	token := jwt.NewWithClaims(method, claims)
	token.Header["kid"] = key.KID
	return token.SignedString(key.Private)
}

// keyForKID returns the public key and its algorithm matching kid, or
// (nil, "") when unknown.
func (j *JWTService) keyForKID(kid string) (crypto.PublicKey, string) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.active != nil && j.active.KID == kid {
		return j.active.Private.Public(), j.active.Alg
	}
	if j.previous != nil && j.previous.KID == kid {
		return j.previous.Private.Public(), j.previous.Alg
	}
	return nil, ""
}

// Verify validates a JWT (RS256 or ES256) and returns its claims. The
// verification key is resolved by the token's kid header; tokens signed
// with an unknown kid are rejected. The token's actual algorithm is
// cross-checked against the algorithm that specific kid was issued under —
// never trusted from the header alone (alg-confusion guard). It also
// rejects tokens whose aud claim does not contain j.selfAudience, and whose
// iss claim does not match this service's issuer.
func (j *JWTService) Verify(tokenStr string) (jwt.MapClaims, error) {
	opts := []jwt.ParserOption{jwt.WithAudience(j.selfAudience)}
	if j.issuer != "" {
		opts = append(opts, jwt.WithIssuer(j.issuer))
	}
	parsed, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		kid, _ := token.Header["kid"].(string)
		pub, alg := j.keyForKID(kid)
		if pub == nil {
			return nil, fmt.Errorf("unknown kid %q", kid)
		}
		if token.Method.Alg() != alg {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return pub, nil
	}, append(opts, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg(), jwt.SigningMethodES256.Alg()}))...)
	if err != nil {
		return nil, fmt.Errorf("verifying token: %w", err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	// SEC-011: this service only ever validates bearer access tokens. Reject
	// anything that isn't explicitly an access token (e.g. an id_token replayed
	// as a bearer credential). id_tokens are verified by resource servers.
	if tu, ok := claims["token_use"].(string); !ok || tu != "access" {
		return nil, fmt.Errorf("token_use claim missing or not \"access\"")
	}
	return claims, nil
}

// jwkFor renders one public key as a JWK map. Supports RSA and EC (P-256)
// public keys — the only two types keystore.Key.Private can ever hold
// (enforced by keystore.Generate/ParseKey), so an unreachable third type here
// indicates a broken invariant elsewhere, not a runtime input to validate.
func jwkFor(pub crypto.PublicKey, alg, kid string) map[string]any {
	switch key := pub.(type) {
	case *rsa.PublicKey:
		n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
		return map[string]any{
			"kty": "RSA",
			"use": "sig",
			"alg": alg,
			"kid": kid,
			"n":   n,
			"e":   e,
		}
	case *ecdsa.PublicKey:
		size := (key.Curve.Params().BitSize + 7) / 8
		x := make([]byte, size)
		y := make([]byte, size)
		key.X.FillBytes(x)
		key.Y.FillBytes(y)
		return map[string]any{
			"kty": "EC",
			"use": "sig",
			"alg": alg,
			"kid": kid,
			"crv": "P-256",
			"x":   base64.RawURLEncoding.EncodeToString(x),
			"y":   base64.RawURLEncoding.EncodeToString(y),
		}
	default:
		panic(fmt.Sprintf("jwkFor: unsupported public key type %T for kid %s", pub, kid))
	}
}

// PublicKeyJWKs returns the JWKS key set: active first, previous second (when
// present). The previous key stays served for a full rotation period so
// tokens signed just before a rotation always verify downstream.
func (j *JWTService) PublicKeyJWKs() []map[string]any {
	j.mu.RLock()
	defer j.mu.RUnlock()
	keys := []map[string]any{jwkFor(j.active.Private.Public(), j.active.Alg, j.active.KID)}
	if j.previous != nil {
		keys = append(keys, jwkFor(j.previous.Private.Public(), j.previous.Alg, j.previous.KID))
	}
	return keys
}

// KID returns the current active key ID.
func (j *JWTService) KID() string {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.active.KID
}

// AccessTokenTTLSeconds returns the configured access-token lifetime in
// seconds, for advertising in the token endpoint's expires_in (BUG-027).
func (j *JWTService) AccessTokenTTLSeconds() int {
	return int(j.accessTokenTTL.Seconds())
}
```

- [ ] **Step 4: Fix the one external call site in `main.go`**

In `/home/artur/Documents/Projects/Ctech/ctech-account/api/cmd/api/main.go`, change:

```go
	if cfg.RSAPrivateKey != nil {
```

to:

```go
	if cfg.SigningKey != nil {
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/artur/Documents/Projects/Ctech/ctech-account/api && go test ./internal/crypto/... -v`

Expected: PASS — all new tests plus every pre-existing `jwt_test.go` test (RS256-only tests must still pass unchanged;
`jwkFor`'s RSA branch produces byte-identical output to the old dedicated RSA-only function).

Run: `cd /home/artur/Documents/Projects/Ctech/ctech-account/api && go build ./... && go vet ./...`

Expected: clean build across the whole module (this is the point where the module first compiles again since Task 3).

- [ ] **Step 6: Commit**

```bash
cd /home/artur/Documents/Projects/Ctech/ctech-account
git add api/internal/crypto/jwt.go api/internal/crypto/jwt_test.go api/cmd/api/main.go
git commit -m "feat(crypto): sign/verify ES256 alongside RS256 with per-kid alg-confusion guard"
```

---

## Task 7: `ctech-account/internal/handler` — advertise ES256 in OIDC discovery

**Files:**

- Modify: `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/handler/wellknown.go`
- Test: `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/handler/wellknown_test.go`

**Interfaces:**

- Consumes: `JWTService.PublicKeyJWKs()` (unchanged signature, Task 6).

- [ ] **Step 1: Read the existing test**

Read `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/handler/wellknown_test.go:43-68` (`TestJWKS`)
fully — it currently asserts `jwk["kty"] != "RSA"` and `jwk["alg"] != "RS256"` against whatever test key the handler
test suite's shared fixture builds. Check `testhelpers_test.go` for how that fixture key is generated.

- [ ] **Step 2: Write the failing test**

Add to `wellknown_test.go`:

```go
func TestOpenIDConfigurationAdvertisesBothSigningAlgs(t *testing.T) {
	app, _ := newTestWellKnownApp(t) // reuse whatever this file's existing app-builder helper is named
	req := httptest.NewRequest(http.MethodGet, "/.well-known/openid-configuration", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	algs, ok := body["id_token_signing_alg_values_supported"].([]any)
	if !ok {
		t.Fatalf("id_token_signing_alg_values_supported missing or wrong type: %v", body["id_token_signing_alg_values_supported"])
	}
	want := map[string]bool{"RS256": false, "ES256": false}
	for _, a := range algs {
		if s, ok := a.(string); ok {
			if _, tracked := want[s]; tracked {
				want[s] = true
			}
		}
	}
	for alg, seen := range want {
		if !seen {
			t.Fatalf("id_token_signing_alg_values_supported missing %s: got %v", alg, algs)
		}
	}
}
```

(Replace `newTestWellKnownApp(t)` with whatever `wellknown_test.go`'s actual existing Fiber test-app constructor is
named — reuse it, don't add a second one.)

- [ ] **Step 3: Run test to verify it fails**

Run:
`cd /home/artur/Documents/Projects/Ctech/ctech-account/api && go test ./internal/handler/... -run TestOpenIDConfigurationAdvertisesBothSigningAlgs -v`

Expected: FAIL — current response only lists `["RS256"]`.

- [ ] **Step 4: Implement**

In `/home/artur/Documents/Projects/Ctech/ctech-account/api/internal/handler/wellknown.go`, change:

```go
		"id_token_signing_alg_values_supported": []string{"RS256"},
```

to:

```go
		"id_token_signing_alg_values_supported": []string{"RS256", "ES256"},
```

- [ ] **Step 5: Run tests to verify they pass**

Run:
`cd /home/artur/Documents/Projects/Ctech/ctech-account/api && go test ./internal/handler/... -run 'TestJWKS|TestOpenIDConfigurationAdvertisesBothSigningAlgs' -v`

Expected: PASS. `TestJWKS` must still pass unchanged (the test fixture's active key remains RSA at this point in the
migration — the discovery document simply now advertises that ES256 is *also* accepted, ahead of the actual cutover).

- [ ] **Step 6: Commit**

```bash
cd /home/artur/Documents/Projects/Ctech/ctech-account
git add api/internal/handler/wellknown.go api/internal/handler/wellknown_test.go
git commit -m "feat(oidc): advertise ES256 in id_token_signing_alg_values_supported"
```

---

## Task 8: Documentation updates (Mandatory Documentation Policy)

**Files:**

- Modify: `/home/artur/Documents/Projects/Ctech/ctech-account/api/CLAUDE.md`
- Modify: `/home/artur/Documents/Projects/Ctech/ctech-account/README.md`
- Modify: `/home/artur/Documents/Projects/Ctech/ctech-account/CONDUCT.md` (if it documents the RS256-only constraint —
  check first)

**Interfaces:** none (docs only).

- [ ] **Step 1: Update `api/CLAUDE.md`**

Change:

```
- `crypto.JWTService` signs RS256 only. No HS256, no `SECRET_KEY`.
```

to:

```
- `crypto.JWTService` signs RS256 or ES256 (per the active key's algorithm). No HS256, no `SECRET_KEY`.
```

Also update the `internal/crypto/` line in the Architecture tree:

```
  crypto/         JWTService (RS256), bcrypt helpers, PKCE
```

to:

```
  crypto/         JWTService (RS256/ES256), bcrypt helpers, PKCE
```

- [ ] **Step 2: Update `README.md`**

Run: `grep -n "RSA_PRIVATE_KEY\|RS256\|PUBLIC_KEY_KID" /home/artur/Documents/Projects/Ctech/ctech-account/README.md`

For each match documenting the `RSA_PRIVATE_KEY` env var or RS256-only signing, add the `EC_PRIVATE_KEY` env var
alongside it (mutually exclusive with `RSA_PRIVATE_KEY`, dev-mode only, selects ES256) and note that production keys
(SSM-backed) can be either algorithm depending on `rotatekeys -alg`.

- [ ] **Step 3: Check and update `CONDUCT.md`**

Run: `grep -n "RS256\|RSA" /home/artur/Documents/Projects/Ctech/ctech-account/CONDUCT.md`

If it documents "RS256 only" as a hard constraint, update it the same way as Step 1.

- [ ] **Step 4: Commit**

```bash
cd /home/artur/Documents/Projects/Ctech/ctech-account
git add api/CLAUDE.md README.md CONDUCT.md
git commit -m "docs: document ES256 signing support and EC_PRIVATE_KEY dev env var"
```

---

## Task 9: Production cutover (manual, no code — runbook)

**Prerequisites:** Tasks 1–8 merged and deployed to production `ctech-account`; Task 2's rollout to `ctech-dfe` and
`ctech-wallet` confirmed healthy (both running `api-commons` ≥ v1.6.0 in production).

**Why this is safe now:** `ctech-account`'s own JWKS/verify code (Task 6) and every downstream verifier (Task 1,
deployed via Task 2) already accept ES256 tokens, resolved per-kid — before this task runs, they simply never see one,
because the active key is still RSA. This task's only job is flipping which algorithm the *next* signed token uses.

- [ ] **Step 1: Confirm rollout gate**

Verify `ctech-dfe/api` and `ctech-wallet/api` are running builds with `api-commons` ≥ v1.6.0 in production (check the
deployed build's `go.mod`/version endpoint per this repo's usual deploy verification, or `gh` release/deploy log). Do
not proceed if either is unconfirmed.

- [ ] **Step 2: Run the cutover rotation**

```bash
cd /home/artur/Documents/Projects/Ctech/ctech-account/api
go run ./cmd/rotatekeys -env prod -alg ES256
```

Expected output:
`rotated: new active kid=<new-ec-kid> alg=ES256 (instances pick it up within 5m; previous kid stays in JWKS)`.

This demotes the current RSA active key to `previous` (still served in JWKS) and makes a freshly generated EC P-256 key
the new `active`. No restart needed — `RunRotator`'s 5-minute reload ticker (`keystore.CheckInterval`) picks it up on
every running instance within 5 minutes.

- [ ] **Step 3: Verify the JWKS endpoint reflects the cutover**

```bash
curl -s https://accounts.aoctech.app/.well-known/jwks.json | jq '.keys[] | {kid, kty, alg}'
```

Expected: one entry with `kty: "EC"`, `alg: "ES256"` (the new active key) and one entry with `kty: "RSA"`,
`alg: "RS256"` (the demoted previous key).

- [ ] **Step 4: Verify a freshly issued token is ES256 and verifies end-to-end**

Perform a normal login/token-exchange against production (or staging pointed at the same JWKS, if a staging cutover is
done first — recommended: run Steps 1–3 in staging before prod). Decode the returned access token's header (e.g.
`echo $TOKEN | cut -d. -f1 | base64 -d`) and confirm `"alg":"ES256"`. Then confirm that token is accepted by a
`ctech-dfe` and a `ctech-wallet` authenticated endpoint (a normal authenticated smoke-test call against each, same as
Task 2 Step 6).

- [ ] **Step 5: Monitor**

Watch each service's auth-failure/error rate (whatever dashboard/logs this project normally uses for auth errors) for at
least one full access-token TTL window (15 minutes) plus one id_token TTL window (1 hour) after cutover — this is the
window during which both RSA-signed (pre-cutover) and EC-signed (post-cutover) tokens are simultaneously in flight and
must both keep verifying.

- [ ] **Step 6: Rollback plan (if verification failures spike)**

```bash
cd /home/artur/Documents/Projects/Ctech/ctech-account/api
go run ./cmd/rotatekeys -env prod -alg RS256
```

This generates a fresh RSA active key (demoting the EC key to `previous`), reverting new-token issuance to RS256 within
5 minutes across all instances. Any EC-signed tokens issued during the brief ES256 window keep verifying (the EC key
remains in JWKS as `previous`) until they naturally expire (≤1 hour).

- [ ] **Step 7: After the cutover is confirmed stable (no rollback needed)**

No further action required — the next scheduled 90-day rotation (`keystore.KeyMaxAge`) will rotate the EC key forward
using `Rotate(..., active.Alg)` (Task 4), i.e. it stays ES256 from here on, and the demoted RSA `previous` key is
naturally replaced by that next rotation's new EC `previous`. Nothing needs to be manually cleaned up.

---

## Self-Review Notes

- **Spec coverage:** every code location identified during research (jwt.go signing/verify/JWKS, keystore
  Key/Generate/rotation/SSM wire format, config dev-key loading, wellknown discovery doc, rotatekeys CLI, jwtverify
  shared library, dfe/wallet consumption) has a corresponding task. The production cutover itself (the actual algorithm
  switch) is Task 9, deliberately last and gated on Tasks 1–8 plus the Task 2 rollout confirmation.
- **Placeholder scan:** no TBD/TODO — every step shows the literal code change. `newFakeSSMAPI(t)` and
  `newTestWellKnownApp(t)` in Tasks 4 and 7 are explicitly flagged as "read the file, reuse its existing helper name"
  rather than invented names, since the actual helper names in those pre-existing test files weren't captured verbatim
  during research.
- **Type consistency:** `keystore.Key.Alg` / `keystore.AlgRS256` / `keystore.AlgES256` (Task 3) are the exact names used
  unchanged through Tasks 4, 5, 6. `config.Config.SigningKey` / `SigningKeyAlg` (Task 5) are the exact names consumed in
  Task 6. `Rotate(ctx, store, now, alg)`'s 4-argument signature (Task 4) is used identically in Task 9's runbook.
