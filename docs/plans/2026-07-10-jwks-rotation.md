# JWKS Auto-Rotation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Signing keys live versioned in SSM (`jwk/active` + `jwk/previous`), JWKS serves both, and an in-process loop (Valkey `SetNX` lock) rotates the active key every ~90 days with no deploy and no downstream breakage.

**Architecture:** New `internal/keystore` package (SSM load/save + rotation loop). `crypto.JWTService` becomes multi-key: signs with active, verifies by `kid`, hot-reloads under `sync.RWMutex`. Dev keeps the `RSA_PRIVATE_KEY` env override (single key, no rotation). Spec: `docs/specs/2026-07-10-account-hardening-design.md` §C. **Critical area** (CLAUDE.md: JWKS/KID rotation impacts ctech-dfe) — every task here must keep a previously-issued token verifiable.

**Tech Stack:** Go 1.26, aws-sdk-go-v2 (ssm), Valkey, CDK TypeScript.

## Global Constraints

- Rotation cadence: `keystore.KeyMaxAge = 90 * 24 * time.Hour`; check interval `keystore.CheckInterval = time.Hour`; lock `SET rotate_jwk_lock NX EX 3600`.
- SSM paths: `/ctech-account/{env}/jwk/active`, `/ctech-account/{env}/jwk/previous` (SecureString, JSON `{"kid","pem","created_at"}`).
- Previous key stays served in JWKS until the **next** rotation (~90 d grace ≫ 15-min access / 1-h id token lifetimes).
- RSA 2048, RS256 only — no new algorithms.
- Valkey disabled ⇒ auto-rotation disabled (manual `cmd/rotatekeys` only). SSM write failures must never crash the API.
- KID derivation for new keys: same scheme as `config.loadRSAKey` (first 16 hex chars of SHA-256 over PKIX public key DER) — extract that into a shared helper, do not duplicate.
- Rollout safety: ship Tasks 1–4 (code + manual command) first; enable the auto loop (Task 5) only after `cmd/rotatekeys --init` has run in prod and a deploy has verified dual-KID JWKS.

---

### Task 1: Key material types + shared KID helper

**Files:**
- Create: `internal/keystore/key.go`
- Create: `internal/keystore/key_test.go`
- Modify: `internal/config/config.go` (reuse helper in `loadRSAKey`)

**Interfaces:**
- Produces:
  - `keystore.Key{KID string; Private *rsa.PrivateKey; CreatedAt time.Time}`
  - `keystore.KeyJSON{KID string `json:"kid"`; PEM string `json:"pem"`; CreatedAt string `json:"created_at"`}` — SSM wire format (RFC3339)
  - `keystore.ParseKey(j KeyJSON) (*Key, error)` / `(*Key) MarshalJSON-free helper ToJSON() (KeyJSON, error)`
  - `keystore.Generate(now time.Time) (*Key, error)` — new RSA-2048 key with derived KID
  - `keystore.DeriveKID(pub *rsa.PublicKey) (string, error)` — extracted from `config.loadRSAKey`, used by both

- [ ] **Step 1: Failing tests**

```go
package keystore

import (
	"testing"
	"time"
)

func TestGenerateRoundTripsThroughJSON(t *testing.T) {
	k, err := Generate(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if k.KID == "" || len(k.KID) != 16 {
		t.Errorf("kid = %q", k.KID)
	}
	j, err := k.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseKey(j)
	if err != nil {
		t.Fatal(err)
	}
	if back.KID != k.KID || !back.CreatedAt.Equal(k.CreatedAt) {
		t.Errorf("round trip mismatch: %+v vs %+v", back, k)
	}
	if back.Private.N.Cmp(k.Private.N) != 0 {
		t.Error("private key mismatch after round trip")
	}
}

func TestDeriveKIDIsStable(t *testing.T) {
	k, _ := Generate(time.Now())
	kid1, _ := DeriveKID(&k.Private.PublicKey)
	kid2, _ := DeriveKID(&k.Private.PublicKey)
	if kid1 != kid2 || kid1 != k.KID {
		t.Errorf("kid unstable: %s %s %s", kid1, kid2, k.KID)
	}
}
```

- [ ] **Step 2: Run — verify FAIL** — `go test ./internal/keystore/ -v`.

- [ ] **Step 3: Implement key.go**

```go
package keystore

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"time"
)

type Key struct {
	KID       string
	Private   *rsa.PrivateKey
	CreatedAt time.Time
}

// KeyJSON is the SSM wire format for a signing key.
type KeyJSON struct {
	KID       string `json:"kid"`
	PEM       string `json:"pem"`
	CreatedAt string `json:"created_at"`
}

// DeriveKID returns the first 16 hex chars of SHA-256 over the PKIX public key
// DER — the same scheme config.loadRSAKey has always used, so wrapping the
// legacy key preserves its KID.
func DeriveKID(pub *rsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("marshaling public key: %w", err)
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])[:16], nil
}

func Generate(now time.Time) (*Key, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generating RSA key: %w", err)
	}
	kid, err := DeriveKID(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	return &Key{KID: kid, Private: priv, CreatedAt: now.UTC()}, nil
}

func (k *Key) ToJSON() (KeyJSON, error) {
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k.Private)}
	return KeyJSON{KID: k.KID, PEM: string(pem.EncodeToMemory(block)), CreatedAt: k.CreatedAt.Format(time.RFC3339)}, nil
}

func ParseKey(j KeyJSON) (*Key, error) {
	block, _ := pem.Decode([]byte(j.PEM))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM in key %s", j.KID)
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing key %s: %w", j.KID, err)
	}
	created, err := time.Parse(time.RFC3339, j.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("parsing created_at of key %s: %w", j.KID, err)
	}
	return &Key{KID: j.KID, Private: priv, CreatedAt: created}, nil
}
```

Then refactor `config.loadRSAKey` to call `keystore.DeriveKID` (delete the inline SHA-256 block). Watch for an import cycle: if `config` importing `keystore` cycles (keystore should NOT import config — keep it dependency-free), this is safe.

- [ ] **Step 4: Run — verify PASS** — `go test ./internal/keystore/ ./internal/config/ -v` and `go build ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/keystore/ internal/config/
git commit -m "feat: add keystore key material types with shared KID derivation"
```

---

### Task 2: JWTService multi-key — sign active, verify by kid, hot reload

**Files:**
- Modify: `internal/crypto/jwt.go`
- Modify: `internal/handler/wellknown.go` (serve all JWKs)
- Test: `internal/crypto/jwt_test.go`, `internal/handler/wellknown_test.go`
- Modify: `cmd/api/main.go` + `internal/handler/testhelpers_test.go` (construction)

**Interfaces:**
- Consumes: `keystore.Key` (Task 1).
- Produces:
  - `crypto.NewJWTService(cfg *config.Config, active, previous *keystore.Key) (*JWTService, error)` (previous may be nil)
  - `(*JWTService) Reload(active, previous *keystore.Key)` — swaps keys under write lock
  - `(*JWTService) PublicKeyJWKs() []map[string]any` — active first, previous second
  - `(*JWTService) KID() string` — active KID (kept for compatibility)
  - `Verify` resolves the verification key by the token's `kid` header; unknown kid ⇒ invalid token
  - Existing `PublicKeyJWK()` deleted; `wellknown.go` switches to `PublicKeyJWKs()`

- [ ] **Step 1: Failing tests** (add to `internal/crypto/jwt_test.go`)

```go
func TestVerifyAcceptsPreviousKeyAfterReload(t *testing.T) {
	oldKey, _ := keystore.Generate(time.Now().Add(-91 * 24 * time.Hour))
	newKey, _ := keystore.Generate(time.Now())

	svc := newJWTServiceWithKeys(t, oldKey, nil)
	tok, err := svc.SignAccessToken("u1", "s1", "web", nil, issuer, []string{issuer}, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	svc.Reload(newKey, oldKey) // rotation happened
	if _, err := svc.Verify(tok); err != nil {
		t.Errorf("token signed by previous key must still verify: %v", err)
	}

	tok2, _ := svc.SignAccessToken("u1", "s1", "web", nil, issuer, []string{issuer}, 0, 0, nil)
	claims := parseHeader(t, tok2) // helper: decode JWT header segment
	if claims["kid"] != newKey.KID {
		t.Errorf("new tokens must be signed with active kid, got %v", claims["kid"])
	}
}

func TestVerifyRejectsUnknownKID(t *testing.T) {
	a, _ := keystore.Generate(time.Now())
	b, _ := keystore.Generate(time.Now())
	svcA := newJWTServiceWithKeys(t, a, nil)
	svcB := newJWTServiceWithKeys(t, b, nil)
	tok, _ := svcA.SignAccessToken("u1", "s1", "web", nil, issuer, []string{issuer}, 0, 0, nil)
	if _, err := svcB.Verify(tok); err == nil {
		t.Error("token with unknown kid must be rejected")
	}
}

func TestJWKSListsActiveThenPrevious(t *testing.T) {
	a, _ := keystore.Generate(time.Now())
	p, _ := keystore.Generate(time.Now().Add(-time.Hour))
	svc := newJWTServiceWithKeys(t, a, p)
	jwks := svc.PublicKeyJWKs()
	if len(jwks) != 2 || jwks[0]["kid"] != a.KID || jwks[1]["kid"] != p.KID {
		t.Errorf("jwks: %v", jwks)
	}
}
```

- [ ] **Step 2: Run — verify FAIL** — `go test ./internal/crypto/ -v`.

- [ ] **Step 3: Implement**

Rewrite `JWTService` internals (keep exported method signatures used elsewhere — `SignAccessToken`, `SignIDToken`, `Verify`, `KID`; keep the step-up claims from the step-up plan if already merged):

```go
type JWTService struct {
	mu           sync.RWMutex
	active       *keystore.Key
	previous     *keystore.Key // nil until first rotation
	selfAudience string
	issuer       string
}

func NewJWTService(cfg *config.Config, active, previous *keystore.Key) (*JWTService, error) {
	if active == nil {
		return nil, fmt.Errorf("active signing key is nil")
	}
	return &JWTService{active: active, previous: previous, selfAudience: cfg.Audience, issuer: cfg.BaseURL}, nil
}

func (j *JWTService) Reload(active, previous *keystore.Key) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.active, j.previous = active, previous
}

func (j *JWTService) sign(claims jwt.MapClaims) (string, error) {
	j.mu.RLock()
	key := j.active
	j.mu.RUnlock()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = key.KID
	return token.SignedString(key.Private)
}

// keyForKID returns the public key matching kid, or nil.
func (j *JWTService) keyForKID(kid string) *rsa.PublicKey {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.active != nil && j.active.KID == kid {
		return &j.active.Private.PublicKey
	}
	if j.previous != nil && j.previous.KID == kid {
		return &j.previous.Private.PublicKey
	}
	return nil
}
```

`Verify` keyfunc becomes:

```go
	parsed, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		kid, _ := token.Header["kid"].(string)
		pub := j.keyForKID(kid)
		if pub == nil {
			return nil, fmt.Errorf("unknown kid %q", kid)
		}
		return pub, nil
	}, opts...)
```

`PublicKeyJWKs()` builds the same JWK map as today per key (extract the map-building into `jwkFor(pub *rsa.PublicKey, kid string) map[string]any`). `wellknown.go`: `"keys": jwtSvc.PublicKeyJWKs()`.

Construction: `cmd/api/main.go` and `testhelpers_test.go` wrap the config key: `active := &keystore.Key{KID: cfg.PublicKeyKID, Private: cfg.RSAPrivateKey, CreatedAt: time.Now()}` for now (Task 3 replaces this with the real loader; dev-env path keeps exactly this wrap).

- [ ] **Step 4: Run — verify PASS** — `go test ./...` (wellknown test asserts ≥1 key; add the 2-key case).

- [ ] **Step 5: Commit**

```bash
git add internal/ cmd/
git commit -m "feat: multi-key JWTService with kid-based verification and hot reload"
```

---

### Task 3: SSM store + boot loading + cache SetNX

**Files:**
- Create: `internal/keystore/ssm.go`
- Create: `internal/keystore/ssm_test.go` (interface-mocked SSM client)
- Modify: `internal/cache/valkey.go` + `internal/cache/valkey_test.go` (add `SetNX`)
- Modify: `internal/config/config.go`, `cmd/api/main.go` (boot path)

**Interfaces:**
- Produces:
  - `keystore.SSMAPI` interface: `GetParameter(ctx, *ssm.GetParameterInput, ...) (*ssm.GetParameterOutput, error)`; `PutParameter(ctx, *ssm.PutParameterInput, ...) (*ssm.PutParameterOutput, error)` (satisfied by `*ssm.Client`)
  - `keystore.NewStore(client SSMAPI, environment string) *Store`
  - `(*Store) Load(ctx) (active *Key, previous *Key, err error)` — previous nil when the parameter is absent (`ParameterNotFound` is not an error)
  - `(*Store) Save(ctx, active, previous *Key) error` — writes **previous first**, then active (crash between the two leaves old active still valid)
  - `cache.(*Client).SetNX(ctx, key string, value string, ttl time.Duration) (bool, error)` — false when key exists or cache disabled
  - Boot: `RSA_PRIVATE_KEY` env set ⇒ legacy single-key mode (dev); else load from SSM via `keystore.Store` (region from `AWS_REGION`)

- [ ] **Step 1: Failing tests** — `ssm_test.go` with a `fakeSSM` map-backed mock: Load round-trip, Load with missing previous, Load with missing active (error), Save writes both params SecureString with `Overwrite: true` and previous-before-active order (record call order in the fake). `valkey_test.go`: SetNX true-then-false with real ttl semantics if the test suite uses a real/miniredis client — follow whatever the existing cache tests do; disabled client returns `(false, nil)`.

- [ ] **Step 2: Run — verify FAIL.**

- [ ] **Step 3: Implement**

`ssm.go` core:

```go
const (
	activeParamFmt   = "/ctech-account/%s/jwk/active"
	previousParamFmt = "/ctech-account/%s/jwk/previous"
)

func (s *Store) Load(ctx context.Context) (*Key, *Key, error) {
	active, err := s.getKey(ctx, fmt.Sprintf(activeParamFmt, s.env))
	if err != nil {
		return nil, nil, fmt.Errorf("loading active jwk: %w", err)
	}
	previous, err := s.getKey(ctx, fmt.Sprintf(previousParamFmt, s.env))
	if err != nil {
		var nf *types.ParameterNotFound
		if errors.As(err, &nf) {
			return active, nil, nil
		}
		return nil, nil, fmt.Errorf("loading previous jwk: %w", err)
	}
	return active, previous, nil
}
```

(`getKey`: GetParameter WithDecryption → `json.Unmarshal` into `KeyJSON` → `ParseKey`. `Save`: marshal each to JSON, `PutParameter` Type SecureString, Overwrite true — previous first.)

`cache.SetNX`: use valkey-go builder `c.client.B().Set().Key(key).Value(value).Nx().Ex(ttl).Build()`; result `IsCacheHit`/nil-error string "OK" ⇒ true, `valkey.IsValkeyNil`-style empty reply ⇒ false (mirror error-handling idiom of existing `Incr`).

Boot (`cmd/api/main.go`): replace the Task-2 temporary wrap:

```go
var jwtSvc *crypto.JWTService
if os.Getenv("RSA_PRIVATE_KEY") != "" {
	active := &keystore.Key{KID: cfg.PublicKeyKID, Private: cfg.RSAPrivateKey, CreatedAt: time.Now().UTC()}
	jwtSvc, err = crypto.NewJWTService(cfg, active, nil)
} else {
	store := keystore.NewStore(ssm.NewFromConfig(awsCfg), cfg.Environment)
	active, previous, loadErr := store.Load(ctx)
	if loadErr != nil {
		log.Fatalf("loading signing keys from SSM: %v", loadErr)
	}
	jwtSvc, err = crypto.NewJWTService(cfg, active, previous)
}
```

`config.Load` change: `RSA_PRIVATE_KEY` becomes optional — when absent, skip `loadRSAKey` and leave `RSAPrivateKey`/`PublicKeyKID` empty (main decides the path). Keep the hard error only in the env path (invalid PEM still fails).

- [ ] **Step 4: Run — verify PASS** — `go test ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/ cmd/
git commit -m "feat: load signing keys from versioned SSM parameters with cache SetNX"
```

---

### Task 4: cmd/rotatekeys — init + manual rotation

**Files:**
- Create: `cmd/rotatekeys/main.go`
- Test: covered by `keystore` unit tests (Task 3 fake SSM) — add `keystore.Rotate` + `keystore.InitFromLegacy` functions there so the command is a thin shell

**Interfaces:**
- Consumes: `keystore.Store`, `keystore.Generate`.
- Produces:
  - `keystore.Rotate(ctx, store *Store, now time.Time) (*Key, error)` — Load → Save(previous←old active, active←new) → return new key
  - `keystore.InitFromLegacy(ctx, store *Store, legacy SSMAPI-read of `/ctech-account/{env}/rsa-private-key`, now time.Time) error` — wraps legacy PEM into `jwk/active` (KID via `DeriveKID`, `created_at = now`); errors if `jwk/active` already exists
  - CLI: `rotatekeys -env prod [-init]` using `AWS_REGION` from env

- [ ] **Step 1: Failing tests** (in `internal/keystore/rotate_test.go`, using the Task-3 fake)

```go
func TestRotatePromotesActiveToPrevious(t *testing.T) {
	fake := newFakeSSM()
	store := NewStore(fake, "test")
	first, _ := Generate(time.Now().Add(-100 * 24 * time.Hour))
	_ = store.Save(context.Background(), first, nil)

	newKey, err := Rotate(context.Background(), store, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	active, previous, _ := store.Load(context.Background())
	if active.KID != newKey.KID || previous == nil || previous.KID != first.KID {
		t.Errorf("active=%s previous=%v", active.KID, previous)
	}
}

func TestInitFromLegacyRefusesWhenActiveExists(t *testing.T) { /* Save an active, expect InitFromLegacy error */ }
func TestInitFromLegacyWrapsPEMPreservingKID(t *testing.T)   { /* put legacy PEM param in fake, init, Load, assert KID == DeriveKID(pub) */ }
```

Write all three bodies fully.

- [ ] **Step 2: Run — verify FAIL.**

- [ ] **Step 3: Implement** `Rotate`/`InitFromLegacy` in keystore, then the thin main:

```go
package main

import (
	"context"
	"flag"
	"log"
	"time"

	"gopkg.aoctech.app/account/internal/keystore"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

func main() {
	env := flag.String("env", "", "environment (e.g. prod)")
	initMode := flag.Bool("init", false, "wrap legacy rsa-private-key parameter into jwk/active")
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
		log.Println("legacy key wrapped into jwk/active")
		return
	}
	newKey, err := keystore.Rotate(ctx, store, time.Now())
	if err != nil {
		log.Fatalf("rotate: %v", err)
	}
	log.Printf("rotated: new active kid=%s (instances pick it up within 1h; JWKS keeps previous kid)", newKey.KID)
}
```

- [ ] **Step 4: Run — verify PASS** — `go test ./internal/keystore/ -v && go build ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/keystore/ cmd/rotatekeys/
git commit -m "feat: add rotatekeys command with legacy key migration"
```

---

### Task 5: Rotation loop (hourly reload + 90d auto-rotate under Valkey lock)

**Files:**
- Create: `internal/keystore/rotator.go`
- Create: `internal/keystore/rotator_test.go`
- Modify: `cmd/api/main.go` (start loop; only in SSM mode)

**Interfaces:**
- Consumes: `Store.Load`, `Rotate`, `cache.(*Client).SetNX`, `(*crypto.JWTService).Reload`.
- Produces: `keystore.RunRotator(ctx context.Context, cfg RotatorConfig)` where

```go
type RotatorConfig struct {
	Store    *Store
	Reload   func(active, previous *Key) // jwtSvc.Reload
	TryLock  func(ctx context.Context) (bool, error) // wraps cache.SetNX("rotate_jwk_lock","1",time.Hour)
	Interval time.Duration // CheckInterval in prod; short in tests
	MaxAge   time.Duration // KeyMaxAge
	Now      func() time.Time
}
```

- [ ] **Step 1: Failing tests** — drive the loop body directly (export `tick(ctx, cfg) error` and have `RunRotator` loop over it; test `tick`, not goroutine timing):

```go
func TestTickReloadsWithoutRotationWhenKeyYoung(t *testing.T) {
	// active 10 days old → Reload called with same kid, no Rotate (fake SSM PutParameter count unchanged)
}

func TestTickRotatesWhenOldAndLockWon(t *testing.T) {
	// active 91 days old, TryLock=true → after tick, store has new active, old kid is previous, Reload got both
}

func TestTickSkipsRotationWhenLockLost(t *testing.T) {
	// active 91 days old, TryLock=false → no PutParameter calls; Reload still called (picks up other instance's work)
}

func TestTickSurvivesSSMError(t *testing.T) {
	// fake returns error on GetParameter → tick returns error, does not panic; caller logs and continues
}
```

Write all four bodies fully against the fake SSM + stub funcs.

- [ ] **Step 2: Run — verify FAIL.**

- [ ] **Step 3: Implement rotator.go**

```go
const (
	KeyMaxAge     = 90 * 24 * time.Hour
	CheckInterval = time.Hour
	lockKey       = "rotate_jwk_lock"
	lockTTL       = time.Hour
)

func tick(ctx context.Context, cfg RotatorConfig) error {
	active, previous, err := cfg.Store.Load(ctx)
	if err != nil {
		return fmt.Errorf("reloading keys: %w", err)
	}

	if cfg.Now().Sub(active.CreatedAt) > cfg.MaxAge {
		won, lockErr := cfg.TryLock(ctx)
		if lockErr != nil {
			slog.Warn("keystore: lock attempt failed, skipping rotation this tick", "error", lockErr)
		} else if won {
			newKey, rotErr := Rotate(ctx, cfg.Store, cfg.Now())
			if rotErr != nil {
				return fmt.Errorf("rotating key: %w", rotErr)
			}
			slog.Info("keystore: rotated signing key", "new_kid", newKey.KID, "old_kid", active.KID)
			active, previous = newKey, active
		}
	}

	cfg.Reload(active, previous)
	return nil
}

// RunRotator reloads keys every Interval and rotates when the active key
// exceeds MaxAge. Errors are logged, never fatal — signing continues on the
// last good keys.
func RunRotator(ctx context.Context, cfg RotatorConfig) {
	t := time.NewTicker(cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := tick(ctx, cfg); err != nil {
				slog.Error("keystore: rotation tick failed", "error", err)
			}
		}
	}
}
```

main.go (SSM mode only, after jwtSvc construction):

```go
if valkeyClient.Enabled() {
	go keystore.RunRotator(ctx, keystore.RotatorConfig{
		Store:  store,
		Reload: jwtSvc.Reload,
		TryLock: func(ctx context.Context) (bool, error) {
			return valkeyClient.SetNX(ctx, "rotate_jwk_lock", "1", time.Hour)
		},
		Interval: keystore.CheckInterval,
		MaxAge:   keystore.KeyMaxAge,
		Now:      time.Now,
	})
}
```

- [ ] **Step 4: Run — verify PASS** — `go test ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/keystore/ cmd/
git commit -m "feat: hourly key reload with 90-day auto-rotation under Valkey lock"
```

---

### Task 6: CDK (IAM + user-data) + docs

**Files:**
- Modify: `cdk/lib/iam-stack.ts:47-52` (add PutParameter on jwk path)
- Modify: `cdk/lib/compute-stack.ts:248,266` (drop `RSA_PRIVATE_KEY` fetch/export from start.sh)
- Modify: `README.md` (config vars: RSA_PRIVATE_KEY now dev-only; SSM key scheme; rotatekeys usage), `PLAN.md` (KID rotation note now automated)

**Interfaces:**
- Consumes: everything above deployed.

- [ ] **Step 1: IAM** — extend the existing SSM policy statement block:

```ts
    // SSM — JWK rotation writes its own versioned parameters
    role.addToPolicy(new iam.PolicyStatement({
      actions: ['ssm:PutParameter'],
      resources: [
        `arn:aws:ssm:*:*:parameter/ctech-account/${environment}/jwk/*`,
      ],
    }));
```

(`ssm:GetParameter` already covers `/ctech-account/${environment}/*`.)

- [ ] **Step 2: user-data** — delete the `RSA_PRIVATE_KEY=$(aws ssm get-parameter ...)` line and the `export RSA_PRIVATE_KEY` line from start.sh in `compute-stack.ts`. `PUBLIC_KEY_KID` fetch/export can also go (KID now travels inside the JWK JSON) — remove both lines.

- [ ] **Step 3: Build** — `cd cdk && npm run build` passes; `npx cdk diff` shows only the IAM statement + launch-template user-data change.

- [ ] **Step 4: Docs** — README: new section "Signing key rotation" (SSM paths, 90d cadence, grace semantics, `go run ./cmd/rotatekeys -env prod [-init]`, dev override). PLAN.md architecture note: replace the manual 4-step KID rotation bullet with a pointer to the automated scheme.

- [ ] **Step 5: Commit**

```bash
git add cdk/ README.md PLAN.md
git commit -m "feat(cdk): grant jwk parameter writes and stop injecting RSA key via user-data"
```

**Prod rollout (operator steps, after merge):**
1. `go run ./cmd/rotatekeys -env prod -init` (wraps legacy key — KID unchanged, zero token impact).
2. Deploy backend + CDK.
3. Verify `GET /.well-known/jwks.json` still serves the same KID; ctech-dfe unaffected.
4. Optionally force one rotation (`rotatekeys -env prod`) and re-verify dual-KID JWKS + old tokens still validate.
