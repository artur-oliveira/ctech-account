# GeoIP Replacement + New-Device Login Notification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the broken third-party GeoIP API with a local, auto-updating MaxMind GeoLite2 City database, and use the country data it provides to detect and email-notify users about logins from a new device/location.

**Architecture:** `internal/geo` becomes a synchronous, in-process lookup backed by an `atomic.Pointer[geoip2.Reader]` over a local `.mmdb` file. A new `internal/geoupdater` package (structurally parallel to `internal/keystore`'s rotation loop, but with no distributed lock — each EC2 instance keeps its own independent local copy) downloads and refreshes that file every 24h once it's 7+ days old. `session.Service` gains `HasSeenDevice` (reusing the existing `ListByUserID` repository method — no new repository method) and `Session` gains a `GeoCountry` field. The five login call sites (password, MFA, passkey, Google direct, Google post-terms) call `geo.Lookup` and `HasSeenDevice` synchronously before `session.Service.Create`, and fire a backgrounded SES email when the device/country pair is new. `internal/email` gains `SendNewDeviceLoginEmail`. `audit.EventLoginSuccess` gains a `new_device: "true"` metadata entry on those same events.

**Tech Stack:** Go 1.26, Fiber v3, DynamoDB, Valkey, AWS SES v2, `github.com/oschwald/geoip2-golang` v1.13.0 (new dependency), stdlib `archive/tar` + `compress/gzip` (no new dependency for the updater).

**Spec:** `docs/specs/2026-08-15-geoip-device-login-notification-design.md`

## Global Constraints

- Every HTTP error MUST be an `*apierror.Problem` sent via `problem.Send(c)` — this plan adds no new HTTP error paths, but any new handler code must still follow this if it touches error returns.
- `handler → service → repository` layering: `geo.Lookup` and email-sending happen in the handler layer (same layer `enrichSessionAsync` lived in today); `session.Service` never imports `internal/geo` or `internal/email`.
- Services take repository **interfaces**, never concrete types — `session.Repository` is unchanged in shape except for removing `UpdateGeoData` (no longer called by anything after this plan).
- No magic strings/numbers: MaxMind download URL, staleness threshold, ticker interval, and DB path default are named constants, not inline literals.
- A login must never fail because of a GeoIP or notification-email failure — `geo.Lookup` returns a zero `Location` on any failure, `HasSeenDevice` failing is treated as "not new" (fail toward *not* alerting), and email send failures are logged, never surfaced to the caller.
- Every core function needs a test (unit for services/packages, integration for routes) per `api/CLAUDE.md`'s testing table. `go build ./...`, `go vet ./...`, and `go test ./...` must pass before any task is considered done.
- `README.md` MUST be updated for the two new SSM parameters and the new GeoIP behavior in the same change (Mandatory Documentation Policy, no exceptions).
- Fiber v3: use `c.Context()`, never `c.UserContext()`.

---

### Task 1: `internal/geo` — local MaxMind lookup

**Files:**
- Modify: `api/internal/geo/geo.go` (full rewrite)
- Create: `api/internal/geo/geo_test.go`
- Create: `api/internal/geo/testdata/GeoIP2-City-Test.mmdb` — **already vendored** at this path (22,569 bytes, sha256 `ed972738e4e03a3e56e12041a6af4d91592249d110f7e4a647e5f2fa0e639c09`). This is MaxMind's own public test fixture (from `github.com/maxmind/MaxMind-DB`, `test-data/GeoIP2-City-Test.mmdb`), small enough to commit directly — it is not fetched via `go mod` because MaxMind ships it as a git submodule, which Go's module proxy does not include. No action needed to create this file; just write the test against it.
- Modify: `api/go.mod`, `api/go.sum` (new dependency)

**Interfaces:**
- Produces: `geo.Location{City, Region, Country string; Latitude, Longitude float64}`, `geo.Lookup(ip string) Location`, `geo.SetReader(r *geoip2.Reader)` — the only two exported symbols other than the struct. `internal/geoupdater` (Task 5) is the sole caller of `SetReader`.

- [ ] **Step 1: Add the new dependency**

Run: `cd api && go get github.com/oschwald/geoip2-golang@v1.13.0`

This also pulls in `github.com/oschwald/maxminddb-golang v1.13.0` as an indirect dependency.

- [ ] **Step 2: Write the failing test**

```go
// api/internal/geo/geo_test.go
package geo

import (
	"testing"

	"github.com/oschwald/geoip2-golang"
)

const testDBPath = "testdata/GeoIP2-City-Test.mmdb"

// knownTestIP is one of MaxMind's own fixture entries: London, GB.
const knownTestIP = "81.2.69.142"

func TestLookupReturnsZeroValueWhenNoReaderSet(t *testing.T) {
	SetReader(nil)
	got := Lookup(knownTestIP)
	if got != (Location{}) {
		t.Errorf("expected zero Location with no reader set, got %+v", got)
	}
}

func TestLookupReturnsZeroValueForUnparseableIP(t *testing.T) {
	r, err := geoip2.Open(testDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	SetReader(r)
	defer SetReader(nil)

	got := Lookup("not-an-ip")
	if got != (Location{}) {
		t.Errorf("expected zero Location for unparseable IP, got %+v", got)
	}
}

func TestLookupReturnsPopulatedLocationForKnownIP(t *testing.T) {
	r, err := geoip2.Open(testDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	SetReader(r)
	defer SetReader(nil)

	got := Lookup(knownTestIP)
	want := Location{City: "London", Region: "England", Country: "GB", Latitude: 51.5142, Longitude: -0.0931}
	if got != want {
		t.Errorf("Lookup(%q) = %+v, want %+v", knownTestIP, got, want)
	}
}

func TestLookupReturnsZeroValueForIPNotInDatabase(t *testing.T) {
	r, err := geoip2.Open(testDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	SetReader(r)
	defer SetReader(nil)

	// 0.0.0.0 has no city record in MaxMind's test fixture. geoip2's City()
	// returns a nil error with an all-zero-value City struct in this case
	// (not an error) — Lookup's field-by-field copy naturally yields a zero
	// Location either way, so no special-casing is needed in Lookup itself.
	got := Lookup("0.0.0.0")
	if got != (Location{}) {
		t.Errorf("expected zero Location for an address with no city record, got %+v", got)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd api && go test ./internal/geo/... -v`
Expected: FAIL — `Location`, `Lookup`, `SetReader` either don't compile against the new test or still return the old freeipapi-based shape (no `Country` field, wrong signature).

- [ ] **Step 4: Rewrite `geo.go`**

```go
// api/internal/geo/geo.go
package geo

import (
	"net"
	"sync/atomic"

	"github.com/oschwald/geoip2-golang"
)

// Location is the geo-enrichment attached to a session at login. Every field
// is the empty/zero value when lookup was impossible or failed — geo data is
// best-effort and must never fail a login.
type Location struct {
	City      string
	Region    string
	Country   string // ISO 3166-1 alpha-2, e.g. "BR". Absent from the old freeipapi-based lookup.
	Latitude  float64
	Longitude float64
}

// reader holds the current MaxMind database. nil until internal/geoupdater's
// Startup populates it (or forever, if MaxMind credentials are not configured
// — Lookup degrades to always returning a zero Location).
var reader atomic.Pointer[geoip2.Reader]

// SetReader atomically swaps the active database. Called only by
// internal/geoupdater. The previous reader (if any) is not explicitly closed:
// maxminddb-golang registers a runtime.SetFinalizer that unmaps it once no
// in-flight Lookup still holds a reference and it becomes unreachable, which
// is the only way to swap a memory-mapped reader without risking a concurrent
// in-flight Lookup reading from an already-unmapped region.
func SetReader(r *geoip2.Reader) {
	reader.Store(r)
}

// Lookup returns geolocation data for ip. Returns a zero Location if no
// database is loaded, ip doesn't parse, or the address has no city record —
// callers must treat every field as best-effort and never fail on a zero
// Location.
func Lookup(ip string) Location {
	r := reader.Load()
	if r == nil {
		return Location{}
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return Location{}
	}
	rec, err := r.City(parsed)
	if err != nil {
		return Location{}
	}
	var region string
	if len(rec.Subdivisions) > 0 {
		region = rec.Subdivisions[0].Names["en"]
	}
	return Location{
		City:      rec.City.Names["en"],
		Region:    region,
		Country:   rec.Country.IsoCode,
		Latitude:  rec.Location.Latitude,
		Longitude: rec.Location.Longitude,
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd api && go test ./internal/geo/... -v`
Expected: PASS (all 4 tests).

- [ ] **Step 6: Commit**

```bash
git add api/go.mod api/go.sum api/internal/geo/geo.go api/internal/geo/geo_test.go api/internal/geo/testdata/GeoIP2-City-Test.mmdb
git commit -m "feat(geo): replace freeipapi with local MaxMind GeoLite2 City lookup"
```

---

### Task 2: `session.Service` — GeoCountry, GeoData, HasSeenDevice

**Files:**
- Modify: `api/internal/domain/session/model.go`
- Modify: `api/internal/domain/session/service.go`
- Modify: `api/internal/domain/session/repository.go`
- Modify: `api/internal/domain/session/service_test.go`

**Interfaces:**
- Consumes: nothing new (this task does not import `internal/geo` — the handler layer does the translation, per the Global Constraints layering rule).
- Produces:
  - `session.GeoData{City, Region, Country string; Latitude, Longitude float64}`
  - `session.Service.Create(ctx context.Context, userID, deviceName, ip, userAgent string, amr []string, geoData GeoData) (*Session, string, error)` — same as today plus the trailing `geoData` parameter.
  - `session.Service.HasSeenDevice(ctx context.Context, userID, deviceName, country string) (bool, error)`
  - `Session.GeoCountry string` (dynamodbav `geo_country,omitempty`)
  - `session.Repository` interface: `UpdateGeoData` removed (its only caller, `Service.UpdateGeoData`, is removed too — geo is now set at `Create` time).

- [ ] **Step 1: Write the failing tests**

Add to `api/internal/domain/session/service_test.go` (the existing `mockRepo.Create` and other methods stay as-is; only `UpdateGeoData` is removed from the mock in Step 4 below, and all existing `svc.Create(...)` calls in this file gain a trailing `session.GeoData{}` argument — grep the file for every `.Create(ctx,` call and add `, session.GeoData{}` before the closing paren):

```go
func TestCreateSetsGeoFieldsFromGeoData(t *testing.T) {
	repo := newMockRepo()
	svc := session.NewService(repo)

	geoData := session.GeoData{City: "São Paulo", Region: "SP", Country: "BR", Latitude: -23.55, Longitude: -46.63}
	sess, _, err := svc.Create(context.Background(), "user1", "Chrome on Mac", "1.2.3.4", "UA", []string{session.AMRPassword}, geoData)
	if err != nil {
		t.Fatal(err)
	}
	if sess.GeoCity != "São Paulo" || sess.GeoRegion != "SP" || sess.GeoCountry != "BR" {
		t.Errorf("geo fields not set from GeoData: %+v", sess)
	}
	if sess.GeoLatitude != -23.55 || sess.GeoLongitude != -46.63 {
		t.Errorf("lat/lon not set from GeoData: %+v", sess)
	}
}

func TestHasSeenDeviceFalseWhenNoPriorSessions(t *testing.T) {
	repo := newMockRepo()
	svc := session.NewService(repo)

	seen, err := svc.HasSeenDevice(context.Background(), "user1", "Chrome on Mac", "BR")
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Error("expected false with no prior sessions")
	}
}

func TestHasSeenDeviceTrueWhenDeviceAndCountryMatch(t *testing.T) {
	repo := newMockRepo()
	svc := session.NewService(repo)
	_, _, err := svc.Create(context.Background(), "user1", "Chrome on Mac", "1.2.3.4", "UA", []string{session.AMRPassword},
		session.GeoData{Country: "BR"})
	if err != nil {
		t.Fatal(err)
	}

	seen, err := svc.HasSeenDevice(context.Background(), "user1", "Chrome on Mac", "BR")
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Error("expected true: same device name and country as an existing session")
	}
}

func TestHasSeenDeviceFalseWhenOnlyDeviceMatches(t *testing.T) {
	repo := newMockRepo()
	svc := session.NewService(repo)
	_, _, err := svc.Create(context.Background(), "user1", "Chrome on Mac", "1.2.3.4", "UA", []string{session.AMRPassword},
		session.GeoData{Country: "BR"})
	if err != nil {
		t.Fatal(err)
	}

	seen, err := svc.HasSeenDevice(context.Background(), "user1", "Chrome on Mac", "US")
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Error("expected false: same device but different country never seen before")
	}
}

func TestHasSeenDeviceFalseWhenOnlyCountryMatches(t *testing.T) {
	repo := newMockRepo()
	svc := session.NewService(repo)
	_, _, err := svc.Create(context.Background(), "user1", "Chrome on Mac", "1.2.3.4", "UA", []string{session.AMRPassword},
		session.GeoData{Country: "BR"})
	if err != nil {
		t.Fatal(err)
	}

	seen, err := svc.HasSeenDevice(context.Background(), "user1", "Firefox on Linux", "BR")
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Error("expected false: same country but different device never seen before")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd api && go test ./internal/domain/session/... -v`
Expected: FAIL to compile — `session.GeoData` undefined, `Create` arity mismatch, `HasSeenDevice` undefined.

- [ ] **Step 3: Add `GeoCountry` and `GeoData` to `model.go`**

In `api/internal/domain/session/model.go`, add the field to `Session` right after `GeoRegion`:

```go
	GeoRegion        string  `dynamodbav:"geo_region,omitempty"`
	GeoCountry       string  `dynamodbav:"geo_country,omitempty"`
```

And add this new type near the bottom of the file, after the `Session` struct's own block of consts/helpers:

```go
// GeoData is the geo-enrichment computed by the handler layer (via
// internal/geo.Lookup) and passed into Create — session never imports
// internal/geo directly, keeping the domain layer free of that dependency.
type GeoData struct {
	City      string
	Region    string
	Country   string
	Latitude  float64
	Longitude float64
}
```

- [ ] **Step 4: Update `service.go`: `Create` signature + `HasSeenDevice`, remove `UpdateGeoData`**

In `api/internal/domain/session/service.go`, change `Create`'s signature and body:

```go
func (s *Service) Create(ctx context.Context, userID, deviceName, ip, userAgent string, amr []string, geoData GeoData) (*Session, string, error) {
	rawToken, tokenHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return nil, "", fmt.Errorf("generating refresh token: %w", err)
	}

	sessionID := uuid.New().String()
	now := time.Now().UTC()

	var lastMFA int64
	for _, m := range amr {
		if IsMFAMethod(m) {
			lastMFA = now.Unix()
			break
		}
	}

	sess := &Session{
		PK:               BuildPK(userID),
		SK:               BuildSK(sessionID),
		RefreshTokenHash: tokenHash,
		DeviceName:       deviceName,
		IPAddress:        ip,
		UserAgent:        userAgent,
		CreatedAt:        now.Format(time.RFC3339),
		LastUsedAt:       now.Format(time.RFC3339),
		ExpiresAt:        now.Add(SessionTTL).Unix(),
		AuthTime:         now.Unix(),
		AMR:              amr,
		LastMFAAt:        lastMFA,
		GeoCity:          geoData.City,
		GeoRegion:        geoData.Region,
		GeoCountry:       geoData.Country,
		GeoLatitude:      geoData.Latitude,
		GeoLongitude:     geoData.Longitude,
	}

	if err := s.repo.Create(ctx, sess); err != nil {
		return nil, "", fmt.Errorf("persisting session: %w", err)
	}
	return sess, rawToken, nil
}

// HasSeenDevice reports whether userID has a prior (non-expired) session from
// the same deviceName and country. Called before Create, so the session being
// created is never in the comparison set. Errors are the caller's to decide
// how to treat — the login-notification call sites fail toward "not new"
// (better a missed email than a false alarm).
func (s *Service) HasSeenDevice(ctx context.Context, userID, deviceName, country string) (bool, error) {
	sessions, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("listing sessions: %w", err)
	}
	for _, sess := range sessions {
		if sess.DeviceName == deviceName && sess.GeoCountry == country {
			return true, nil
		}
	}
	return false, nil
}
```

Delete the existing `UpdateGeoData` method from `Service` entirely (it directly precedes `List` in the current file).

- [ ] **Step 5: Remove `UpdateGeoData` from the `Repository` interface and `dynamoRepository`**

In `api/internal/domain/session/repository.go`, delete this line from the `Repository` interface:

```go
	UpdateGeoData(ctx context.Context, userID, sessionID, city, region string, lat, lon float64) error
```

And delete the `dynamoRepository.UpdateGeoData` method (the last method in the file).

- [ ] **Step 6: Update `service_test.go`'s `mockRepo`**

Delete `mockRepo.UpdateGeoData` (lines 40-42 in the current file). Then find every existing `svc.Create(ctx, ...)` call already in this file (there are calls in tests exercising rotation, MFA recording, etc.) and add a trailing `session.GeoData{}` argument to each — `go vet` will point at every call site that still has the old arity if any are missed.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd api && go test ./internal/domain/session/... -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add api/internal/domain/session/
git commit -m "feat(session): add GeoCountry, HasSeenDevice, and geo-at-create-time"
```

---

### Task 3: `SendNewDeviceLoginEmail`

**Files:**
- Modify: `api/internal/email/ses.go`

**Interfaces:**
- Produces: `(c *Client) SendNewDeviceLoginEmail(ctx context.Context, to, firstName, deviceName, city, country, ip string, when time.Time) error`

- [ ] **Step 1: Add the method and template, following the existing `SendPasswordResetEmail` shape**

Add to `api/internal/email/ses.go`, after `SendAccountExistsEmail`:

```go
// SendNewDeviceLoginEmail notifies the user of a successful login from a
// device/country combination not seen on any of their prior sessions.
func (c *Client) SendNewDeviceLoginEmail(ctx context.Context, to, firstName, deviceName, city, country, ip string, when time.Time) error {
	subject := "Novo login detectado — ctech"
	body := newDeviceLoginEmailHTML(deviceName, city, country, ip, when)
	return c.send(ctx, to, subject, newDeviceLoginEmailLayout(firstName, body))
}
```

Add `"time"` to the imports.

Add the template function near the other `*EmailHTML` functions at the bottom of the file:

```go
func newDeviceLoginEmailHTML(deviceName, city, country, ip string, when time.Time) string {
	location := city
	if country != "" {
		if location != "" {
			location += ", "
		}
		location += country
	}
	if location == "" {
		location = "localização desconhecida"
	}
	return fmt.Sprintf(`<p>Detectamos um login na sua conta a partir de um dispositivo ou local que não reconhecemos:</p>
  <ul>
    <li><strong>Dispositivo:</strong> %s</li>
    <li><strong>Local:</strong> %s</li>
    <li><strong>IP:</strong> %s</li>
    <li><strong>Quando:</strong> %s</li>
  </ul>
  <p>Se foi você, pode ignorar este e-mail. Se não reconhece este acesso, redefina sua senha imediatamente.</p>`,
		deviceName, location, ip, when.UTC().Format("02/01/2006 15:04 MST"))
}

func newDeviceLoginEmailLayout(firstName, bodyHTML string) string {
	return emailLayout("Novo login detectado", firstName, bodyHTML,
		"Se não foi você, redefina sua senha e revise os dispositivos conectados em sua conta.")
}
```

- [ ] **Step 2: Write the test**

Create `api/internal/email/ses_test.go`:

```go
package email

import (
	"strings"
	"testing"
	"time"
)

func TestNewDeviceLoginEmailHTMLIncludesAllFields(t *testing.T) {
	when := time.Date(2026, 8, 15, 14, 30, 0, 0, time.UTC)
	body := newDeviceLoginEmailHTML("Chrome on Mac", "São Paulo", "BR", "203.0.113.5", when)

	for _, want := range []string{"Chrome on Mac", "São Paulo, BR", "203.0.113.5", "15/08/2026 14:30 UTC"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestNewDeviceLoginEmailHTMLFallsBackWhenLocationUnknown(t *testing.T) {
	body := newDeviceLoginEmailHTML("Chrome on Mac", "", "", "203.0.113.5", time.Now())
	if !strings.Contains(body, "localização desconhecida") {
		t.Errorf("expected fallback location text, got:\n%s", body)
	}
}

func TestNewDeviceLoginEmailLayoutRendersFullPage(t *testing.T) {
	page := newDeviceLoginEmailLayout("Maria", "<p>corpo</p>")
	if !strings.Contains(page, "Novo login detectado") || !strings.Contains(page, "Maria") || !strings.Contains(page, "corpo") {
		t.Errorf("layout missing expected content:\n%s", page)
	}
}
```

- [ ] **Step 3: Run the tests**

Run: `cd api && go test ./internal/email/... -v`
Expected: PASS.

- [ ] **Step 4: Build the whole module**

Run: `cd api && go build ./...`
Expected: succeeds. (Nothing calls `SendNewDeviceLoginEmail` yet — that's Task 4 — this step just confirms `ses.go` itself compiles cleanly.)

- [ ] **Step 5: Commit**

```bash
git add api/internal/email/
git commit -m "feat(email): add new-device login notification template"
```

---

### Task 4: Wire `geo.Lookup` + `HasSeenDevice` into the five login call sites

**Files:**
- Modify: `api/internal/handler/helpers.go`
- Modify: `api/internal/handler/auth.go`
- Modify: `api/internal/handler/social.go`
- Modify: `api/internal/handler/passkey.go`
- Modify: `api/internal/handler/testhelpers_test.go`

**Interfaces:**
- Consumes: `geo.Lookup(ip string) geo.Location` (Task 1), `session.Service.Create(..., session.GeoData)`, `session.Service.HasSeenDevice(...)` (Task 2), `email.Client.SendNewDeviceLoginEmail(...)` (Task 3, already implemented at this point in the task order).
- Produces: `SocialHandler.emailCli`, `PasskeyHandler.emailCli` fields (mirroring `AuthHandler.emailCli`); `NewSocialHandler` and `NewPasskeyHandler` both gain a trailing `emailCli *email.Client` parameter.

- [ ] **Step 1: Add `emailCli` to `SocialHandler` and `PasskeyHandler`**

In `api/internal/handler/social.go`:

```go
type SocialHandler struct {
	userSvc    *user.Service
	sessionSvc *session.Service
	cache      *cache.Client
	cfg        *config.Config
	audit      *audit.Service
	emailCli   *email.Client // nil when FROM_EMAIL is not set
}

func NewSocialHandler(userSvc *user.Service, sessionSvc *session.Service, c *cache.Client, cfg *config.Config, auditSvc *audit.Service, emailCli *email.Client) *SocialHandler {
	return &SocialHandler{userSvc: userSvc, sessionSvc: sessionSvc, cache: c, cfg: cfg, audit: auditSvc, emailCli: emailCli}
}
```

Add `"gopkg.aoctech.app/account/api/internal/email"` to `social.go`'s imports if not already present (it isn't — `social.go` currently has no email dependency).

In `api/internal/handler/passkey.go`, same shape:

```go
type PasskeyHandler struct {
	passkeySvc *passkey.Service
	userSvc    *user.Service
	sessionSvc *session.Service
	totpSvc    TOTPService
	cache      *cache.Client
	cfg        *config.Config
	audit      *audit.Service
	emailCli   *email.Client // nil when FROM_EMAIL is not set
}

func NewPasskeyHandler(passkeySvc *passkey.Service, userSvc *user.Service, sessionSvc *session.Service, totpSvc TOTPService, valkeyCache *cache.Client, cfg *config.Config, auditSvc *audit.Service, emailCli *email.Client) *PasskeyHandler {
	return &PasskeyHandler{passkeySvc: passkeySvc, userSvc: userSvc, sessionSvc: sessionSvc, totpSvc: totpSvc, cache: valkeyCache, cfg: cfg, audit: auditSvc, emailCli: emailCli}
}
```

Add the same import to `passkey.go`.

- [ ] **Step 2: Replace `enrichSessionAsync` with `sendNewDeviceEmailAsync` in `helpers.go`**

In `api/internal/handler/helpers.go`, delete the entire `enrichSessionAsync` function (and its now-unused `"context"`/`"time"` imports if nothing else in the file needs them — check before removing; `time` is still used by `setAuthCookie` etc., so only drop `"context"` if truly unused elsewhere in the file after this edit). Replace it with:

```go
// sendNewDeviceEmailAsync fires a goroutine sending the new-device login
// notification. Failures are logged, never surfaced — email is best-effort
// and must never block or fail a login.
func sendNewDeviceEmailAsync(emailCli *email.Client, to, firstName, deviceName, city, country, ip string) {
	if emailCli == nil {
		return
	}
	go func() {
		if err := emailCli.SendNewDeviceLoginEmail(context.Background(), to, firstName, deviceName, city, country, ip, time.Now()); err != nil {
			log.Printf("new-device login email failed for %s: %v", to, err)
		}
	}()
}
```

Add `"log"` to `helpers.go`'s imports (not currently imported there).

- [ ] **Step 3: `auth.go` — `issueSession` (password login)**

Replace lines 204-223 (the whole `issueSession` method body from `deviceName := ...` through the closing `}`):

```go
func (h *AuthHandler) issueSession(c fiber.Ctx, u *user.User) error {
	deviceName := parseDeviceName(c.Get("User-Agent"))
	ip := clientIP(c)
	loc := geo.Lookup(ip)
	seen, seenErr := h.sessionSvc.HasSeenDevice(c.Context(), u.ID(), deviceName, loc.Country)
	newDevice := seenErr == nil && !seen

	sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), deviceName, ip, c.Get("User-Agent"), []string{session.AMRPassword},
		session.GeoData{City: loc.City, Region: loc.Region, Country: loc.Country, Latitude: loc.Latitude, Longitude: loc.Longitude})
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	meta := map[string]string{"session_id": sess.ID()}
	if newDevice {
		meta["new_device"] = "true"
		sendNewDeviceEmailAsync(h.emailCli, u.Email, u.FirstName, deviceName, loc.City, loc.Country, ip)
	}
	recordAudit(c, h.audit, u.ID(), audit.EventLoginSuccess, meta)

	setSessionCookies(c, h.cfg, rawToken)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"user_id":    u.ID(),
		"email":      u.Email,
		"first_name": u.FirstName,
		"last_name":  u.LastName,
		"session_id": sess.ID(),
	})
}
```

Add `"gopkg.aoctech.app/account/api/internal/geo"` to `auth.go`'s imports.

- [ ] **Step 4: `auth.go` — `mfaChallenge`**

Replace lines 258-263 (the `Create`/`enrichSessionAsync`/`recordAudit` block inside `mfaChallenge`):

```go
	loc := geo.Lookup(payload.IP)
	seen, seenErr := h.sessionSvc.HasSeenDevice(c.Context(), u.ID(), payload.DeviceName, loc.Country)
	newDevice := seenErr == nil && !seen

	sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), payload.DeviceName, payload.IP, payload.UserAgent, mfaSessionAMR(payload),
		session.GeoData{City: loc.City, Region: loc.Region, Country: loc.Country, Latitude: loc.Latitude, Longitude: loc.Longitude})
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	meta := map[string]string{"method": "totp", "session_id": sess.ID()}
	if newDevice {
		meta["new_device"] = "true"
		sendNewDeviceEmailAsync(h.emailCli, u.Email, u.FirstName, payload.DeviceName, loc.City, loc.Country, payload.IP)
	}
	recordAudit(c, h.audit, u.ID(), audit.EventMFAChallengeSuccess, meta)
```

- [ ] **Step 5: `passkey.go` — WebAuthn login complete**

Replace lines 240-245:

```go
		loc := geo.Lookup(ip)
		seen, seenErr := h.sessionSvc.HasSeenDevice(c.Context(), u.ID(), "Passkey", loc.Country)
		newDevice := seenErr == nil && !seen

		sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), "Passkey", ip, c.Get("User-Agent"), []string{session.AMRWebAuthn},
			session.GeoData{City: loc.City, Region: loc.Region, Country: loc.Country, Latitude: loc.Latitude, Longitude: loc.Longitude})
		if err != nil {
			return apierror.ServerError(c.Path()).Send(c)
		}

		meta := map[string]string{"method": session.AMRWebAuthn, "session_id": sess.ID()}
		if newDevice {
			meta["new_device"] = "true"
			sendNewDeviceEmailAsync(h.emailCli, u.Email, u.FirstName, "Passkey", loc.City, loc.Country, ip)
		}
		recordAudit(c, h.audit, u.ID(), audit.EventLoginSuccess, meta)
```

(Indentation matches the surrounding block in the existing file — verify against the actual file before pasting, since this sits inside an `if`/handler body.)

Add `"gopkg.aoctech.app/account/api/internal/geo"` to `passkey.go`'s imports.

- [ ] **Step 6: `social.go` — `acceptTerms` (Google post-terms) and `issueSessionFromSocial` (Google direct)**

Replace lines 231-236 inside `acceptTerms`:

```go
		loc := geo.Lookup(payload.IP)
		seen, seenErr := h.sessionSvc.HasSeenDevice(c.Context(), u.ID(), payload.DeviceName, loc.Country)
		newDevice := seenErr == nil && !seen

		sess, rawToken, sErr := h.sessionSvc.Create(c.Context(), u.ID(), payload.DeviceName, payload.IP, payload.UserAgent, []string{session.AMRGoogle},
			session.GeoData{City: loc.City, Region: loc.Region, Country: loc.Country, Latitude: loc.Latitude, Longitude: loc.Longitude})
		if sErr != nil {
			return apierror.ServerError(c.Path()).Send(c)
		}

		meta := map[string]string{"method": acceptMethodGoogle, "session_id": sess.ID()}
		if newDevice {
			meta["new_device"] = "true"
			sendNewDeviceEmailAsync(h.emailCli, u.Email, u.FirstName, payload.DeviceName, loc.City, loc.Country, payload.IP)
		}
		recordAudit(c, h.audit, u.ID(), audit.EventLoginSuccess, meta)

		setSessionCookies(c, h.cfg, rawToken)
```

Replace lines 332-346 (`issueSessionFromSocial` in full):

```go
func (h *SocialHandler) issueSessionFromSocial(c fiber.Ctx, u *user.User) error {
	deviceName := parseDeviceName(c.Get("User-Agent"))
	ip := clientIP(c)
	loc := geo.Lookup(ip)
	seen, seenErr := h.sessionSvc.HasSeenDevice(c.Context(), u.ID(), deviceName, loc.Country)
	newDevice := seenErr == nil && !seen

	sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), deviceName, ip, c.Get("User-Agent"), []string{session.AMRGoogle},
		session.GeoData{City: loc.City, Region: loc.Region, Country: loc.Country, Latitude: loc.Latitude, Longitude: loc.Longitude})
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	meta := map[string]string{"method": "google", "session_id": sess.ID()}
	if newDevice {
		meta["new_device"] = "true"
		sendNewDeviceEmailAsync(h.emailCli, u.Email, u.FirstName, deviceName, loc.City, loc.Country, ip)
	}
	recordAudit(c, h.audit, u.ID(), audit.EventLoginSuccess, meta)

	// ctech_rt is set alongside ctech_session so the /token refresh_token grant can
	// rotate the session without JS access to the HttpOnly ctech_session cookie.
	setSessionCookies(c, h.cfg, rawToken)
	return nil
}
```

Add `"gopkg.aoctech.app/account/api/internal/geo"` to `social.go`'s imports.

- [ ] **Step 7: Update `testhelpers_test.go`**

In `api/internal/handler/testhelpers_test.go`:
- Delete `memSessionRepo.UpdateGeoData` (the method currently sitting between `DeleteRefreshToken` and `UpdateMFA`).
- Change the three constructor call sites:
  - `handler.NewPasskeyHandler(passkeySvc, userSvc, sessionSvc, noop, disabledCache, cfg, auditSvc)` → append `, nil` (twice — once per call site, lines 217 and 230 in the current file).
  - `handler.NewSocialHandler(userSvc, sessionSvc, socialCache, cfg, auditSvc)` → append `, nil` (line 239).

- [ ] **Step 8: Update `main.go` wiring (constructor calls only — full geoupdater wiring is Task 6)**

In `api/cmd/api/main.go`, update the two constructor calls to pass `emailCli`:

```go
socialH := handler.NewSocialHandler(userSvc, sessionSvc, valkeyClient, cfg, auditSvc, emailCli)
```
```go
passkeyH := handler.NewPasskeyHandler(passkeySvc, userSvc, sessionSvc, totpSvc, valkeyClient, cfg, auditSvc, emailCli)
```

- [ ] **Step 9: Build and run the full handler test suite**

Run: `cd api && go build ./... && go test ./internal/handler/... -v`
Expected: compiles; all existing handler tests still pass (none of them assert on `GeoCity` being populated post-login today, so removing the async enrichment doesn't break an existing assertion — verified during plan research). If any test constructs a `session.Service` directly and calls `.Create` with the old 6-arg signature, add a trailing `session.GeoData{}`.

- [ ] **Step 10: Commit**

```bash
git add api/internal/handler/
git commit -m "feat(handler): synchronous geo lookup + new-device detection on login"
```

---

### Task 5: `internal/geoupdater` — per-instance auto-update

**Files:**
- Create: `api/internal/geoupdater/updater.go`
- Create: `api/internal/geoupdater/updater_test.go`

**Interfaces:**
- Consumes: `geo.SetReader(*geoip2.Reader)` (Task 1)
- Produces:
  - `geoupdater.Config{DBPath, AccountID, LicenseKey string; Interval, StaleAfter time.Duration; Now func() time.Time; HTTPClient *http.Client}`
  - `geoupdater.Startup(ctx context.Context, cfg Config)` — call once at boot, blocking.
  - `geoupdater.Run(ctx context.Context, cfg Config)` — blocks; run in a goroutine, mirrors `keystore.RunRotator`'s calling convention.
  - `geoupdater.DefaultInterval = 24 * time.Hour`, `geoupdater.DefaultStaleAfter = 7 * 24 * time.Hour` (exported constants main.go wires in).

- [ ] **Step 1: Write the failing tests**

```go
// api/internal/geoupdater/updater_test.go
package geoupdater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/geo"
)

// buildFixtureTarGz packs the geo package's test-fixture .mmdb into a
// tar.gz matching the shape of MaxMind's real download response (one
// directory entry containing a versioned dir, then the .mmdb file inside it
// — the extractor must find the .mmdb by extension, not by a fixed path).
func buildFixtureTarGz(t *testing.T) []byte {
	t.Helper()
	mmdbBytes, err := os.ReadFile("../geo/testdata/GeoIP2-City-Test.mmdb")
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	name := "GeoLite2-City_20260101/GeoLite2-City.mmdb"
	if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(mmdbBytes)), Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(mmdbBytes); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUpdateDownloadsExtractsValidatesAndSwaps(t *testing.T) {
	geo.SetReader(nil)
	defer geo.SetReader(nil)

	fixture := buildFixtureTarGz(t)
	var gotAuthUser, gotAuthPass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthUser, gotAuthPass, _ = r.BasicAuth()
		w.Write(fixture)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := Config{
		DBPath:      filepath.Join(dir, "GeoLite2-City.mmdb"),
		AccountID:   "acct123",
		LicenseKey:  "key456",
		downloadURL: srv.URL,
		HTTPClient:  srv.Client(),
	}

	if err := update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if gotAuthUser != "acct123" || gotAuthPass != "key456" {
		t.Errorf("expected basic auth acct123:key456, got %s:%s", gotAuthUser, gotAuthPass)
	}
	if _, err := os.Stat(cfg.DBPath); err != nil {
		t.Errorf("expected file at %s: %v", cfg.DBPath, err)
	}

	loc := geo.Lookup("81.2.69.142")
	if loc.City != "London" {
		t.Errorf("expected geo.Lookup to use the newly swapped reader, got %+v", loc)
	}
}

func TestUpdateRejectsInvalidArchive(t *testing.T) {
	geo.SetReader(nil)
	defer geo.SetReader(nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a tar.gz"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := Config{
		DBPath:      filepath.Join(dir, "GeoLite2-City.mmdb"),
		AccountID:   "acct123",
		LicenseKey:  "key456",
		downloadURL: srv.URL,
		HTTPClient:  srv.Client(),
	}

	if err := update(context.Background(), cfg); err == nil {
		t.Error("expected an error for a non-tar.gz response")
	}
	if _, err := os.Stat(cfg.DBPath); !os.IsNotExist(err) {
		t.Error("a failed update must not leave a partial file at DBPath")
	}
}

func TestMaybeUpdateSkipsWhenFileIsFresh(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "GeoLite2-City.mmdb")
	if err := os.WriteFile(dbPath, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	cfg := Config{
		DBPath: dbPath, AccountID: "a", LicenseKey: "k",
		downloadURL: srv.URL, HTTPClient: srv.Client(),
		StaleAfter: 7 * 24 * time.Hour,
		Now:        time.Now,
	}
	if err := maybeUpdate(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("must not download when the existing file is younger than StaleAfter")
	}
}

func TestStartupOpensExistingFileWithoutDownloading(t *testing.T) {
	geo.SetReader(nil)
	defer geo.SetReader(nil)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "GeoLite2-City.mmdb")
	fixture, err := os.ReadFile("../geo/testdata/GeoIP2-City-Test.mmdb")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, fixture, 0o644); err != nil {
		t.Fatal(err)
	}

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	Startup(context.Background(), Config{DBPath: dbPath, downloadURL: srv.URL, HTTPClient: srv.Client()})

	if called {
		t.Error("Startup must not download when a file already exists at DBPath")
	}
	if loc := geo.Lookup("81.2.69.142"); loc.City != "London" {
		t.Errorf("expected Startup to load the existing file into geo.reader, got %+v", loc)
	}
}

func TestStartupDegradesGeoWhenNoFileAndDownloadFails(t *testing.T) {
	geo.SetReader(nil)
	defer geo.SetReader(nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	dir := t.TempDir()
	Startup(context.Background(), Config{
		DBPath: filepath.Join(dir, "GeoLite2-City.mmdb"),
		AccountID: "bad", LicenseKey: "bad",
		downloadURL: srv.URL, HTTPClient: srv.Client(),
	})

	// Must not panic or block forever; geo lookups just stay disabled.
	if loc := geo.Lookup("81.2.69.142"); loc != (geo.Location{}) {
		t.Errorf("expected geo disabled after failed startup download, got %+v", loc)
	}
}
```

Note: `Config` needs an unexported `downloadURL` field so tests can point at an `httptest.Server` instead of the real MaxMind endpoint — production code never sets it, so it defaults to the real constant (see Step 2).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd api && go test ./internal/geoupdater/... -v`
Expected: FAIL to compile — package doesn't exist yet.

- [ ] **Step 3: Write `updater.go`**

```go
// Package geoupdater keeps the local MaxMind GeoLite2 City database
// (internal/geo's reader) fresh. Each EC2 instance in the ASG downloads and
// refreshes its own independent copy — there is no shared store for this
// file (unlike internal/keystore's SSM-backed signing keys), so no
// distributed lock is needed; redundant per-instance downloads against
// MaxMind's own endpoint are expected and within normal license use.
package geoupdater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschwald/geoip2-golang"
	"gopkg.aoctech.app/account/api/internal/geo"
)

const (
	// DefaultInterval is how often the background loop checks staleness.
	DefaultInterval = 24 * time.Hour
	// DefaultStaleAfter is the file age past which a refresh is attempted.
	// GeoLite2 ships weekly; 7 days keeps a fresh copy without hammering
	// MaxMind's endpoint.
	DefaultStaleAfter = 7 * 24 * time.Hour
	// maxStartupJitter spreads a fleet-wide deploy's first tick over up to an
	// hour so every ASG instance doesn't hit MaxMind at the same moment.
	maxStartupJitter = time.Hour

	realDownloadURL = "https://download.maxmind.com/geoip/databases/GeoLite2-City/download?suffix=tar.gz"
)

// Config wires the updater to its collaborators.
type Config struct {
	DBPath     string
	AccountID  string
	LicenseKey string
	Interval   time.Duration
	StaleAfter time.Duration
	Now        func() time.Time
	HTTPClient *http.Client
	// downloadURL overrides realDownloadURL in tests. Zero value in
	// production, which resolveDownloadURL turns into the real endpoint.
	downloadURL string
}

func (cfg Config) resolveDownloadURL() string {
	if cfg.downloadURL != "" {
		return cfg.downloadURL
	}
	return realDownloadURL
}

func (cfg Config) httpClient() *http.Client {
	if cfg.HTTPClient != nil {
		return cfg.HTTPClient
	}
	return http.DefaultClient
}

// Startup ensures internal/geo has a reader loaded before the server starts
// accepting traffic where possible, without ever blocking boot indefinitely.
// If DBPath already has a file, it's opened as-is (even if stale — the
// background Run loop refreshes it on its next tick). If not, one blocking
// download attempt is made; on failure, geo lookups stay disabled (zero
// Location) rather than crash-looping the instance over a non-critical
// feature.
func Startup(ctx context.Context, cfg Config) {
	if info, err := os.Stat(cfg.DBPath); err == nil && info.Size() > 0 {
		r, err := geoip2.Open(cfg.DBPath)
		if err != nil {
			slog.Warn("geoupdater: existing db file is unreadable, geo lookups disabled until next refresh", "path", cfg.DBPath, "error", err)
			return
		}
		geo.SetReader(r)
		return
	}
	if err := update(ctx, cfg); err != nil {
		slog.Warn("geoupdater: initial download failed, geo lookups disabled", "error", err)
	}
}

// Run refreshes the database every cfg.Interval once it's older than
// cfg.StaleAfter, jittering the first tick by up to an hour. Blocks until ctx
// is cancelled — run in a goroutine, same convention as keystore.RunRotator.
func Run(ctx context.Context, cfg Config) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(rand.Int63n(int64(maxStartupJitter)))):
	}

	t := time.NewTicker(cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := maybeUpdate(ctx, cfg); err != nil {
				slog.Error("geoupdater: update tick failed", "error", err)
			}
		}
	}
}

// maybeUpdate downloads a fresh database only if the local file is missing
// or older than cfg.StaleAfter.
func maybeUpdate(ctx context.Context, cfg Config) error {
	info, err := os.Stat(cfg.DBPath)
	stale := err != nil || cfg.Now().Sub(info.ModTime()) > cfg.StaleAfter
	if !stale {
		return nil
	}
	return update(ctx, cfg)
}

// update downloads the database tar.gz, extracts the .mmdb entry, validates
// it opens successfully, atomically replaces DBPath, and swaps the live
// reader. A failure at any step leaves the existing file and reader
// untouched.
func update(ctx context.Context, cfg Config) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.resolveDownloadURL(), nil)
	if err != nil {
		return fmt.Errorf("building download request: %w", err)
	}
	req.SetBasicAuth(cfg.AccountID, cfg.LicenseKey)

	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("downloading database: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading database: unexpected status %d", resp.StatusCode)
	}

	tmpPath, err := extractMMDB(resp.Body, filepath.Dir(cfg.DBPath))
	if err != nil {
		return fmt.Errorf("extracting database: %w", err)
	}

	r, err := geoip2.Open(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("validating downloaded database: %w", err)
	}

	if err := os.Rename(tmpPath, cfg.DBPath); err != nil {
		_ = r.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("installing downloaded database: %w", err)
	}

	geo.SetReader(r)
	return nil
}

// extractMMDB reads a gzipped tar stream and writes the first entry whose
// name ends in ".mmdb" to a temp file in destDir (same filesystem as the
// eventual DBPath, so the caller's os.Rename is atomic). Returns the temp
// file's path.
func extractMMDB(body io.Reader, destDir string) (string, error) {
	gz, err := gzip.NewReader(body)
	if err != nil {
		return "", fmt.Errorf("opening gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("no .mmdb entry found in archive")
		}
		if err != nil {
			return "", fmt.Errorf("reading tar entry: %w", err)
		}
		if !strings.HasSuffix(hdr.Name, ".mmdb") {
			continue
		}

		tmp, err := os.CreateTemp(destDir, "geolite2-city-*.mmdb.tmp")
		if err != nil {
			return "", fmt.Errorf("creating temp file: %w", err)
		}
		if _, err := io.Copy(tmp, tr); err != nil {
			tmp.Close()
			_ = os.Remove(tmp.Name())
			return "", fmt.Errorf("writing temp file: %w", err)
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmp.Name())
			return "", fmt.Errorf("closing temp file: %w", err)
		}
		return tmp.Name(), nil
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd api && go test ./internal/geoupdater/... -v`
Expected: PASS (all 6 tests).

- [ ] **Step 5: Commit**

```bash
git add api/internal/geoupdater/
git commit -m "feat(geoupdater): per-instance auto-update of the MaxMind database"
```

---

### Task 6: Config, main.go wiring, CDK, and docs

**Files:**
- Modify: `api/internal/config/config.go`
- Modify: `api/cmd/api/main.go`
- Modify: `cdk/lib/compute-stack.ts`
- Modify: `README.md`
- Modify: `PLAN.md`

**Interfaces:**
- Consumes: `geoupdater.Startup`, `geoupdater.Run`, `geoupdater.Config`, `geoupdater.DefaultInterval`, `geoupdater.DefaultStaleAfter` (Task 5)
- Produces: `config.Config.MaxMindAccountID`, `config.Config.MaxMindLicenseKey`, `config.Config.MaxMindDBPath` — no new interfaces consumed by later tasks (this is the last task).

- [ ] **Step 1: Add config fields**

In `api/internal/config/config.go`, add to the `Config` struct (after `AppURL`):

```go
	// MaxMind GeoLite2 City credentials (MAXMIND_ACCOUNT_ID / MAXMIND_LICENSE_KEY
	// env vars). When either is empty, GeoIP lookups stay permanently disabled
	// (geo.Lookup always returns a zero Location) — mirrors how an absent
	// KYCDocumentsBucket disables document verification.
	MaxMindAccountID  string
	MaxMindLicenseKey string
	// MaxMindDBPath is the local path the .mmdb file is stored at and
	// refreshed in place (MAXMIND_DB_PATH env var).
	MaxMindDBPath string
```

In `Load()`, add to the returned `&Config{...}` literal:

```go
		MaxMindAccountID:  os.Getenv("MAXMIND_ACCOUNT_ID"),
		MaxMindLicenseKey: os.Getenv("MAXMIND_LICENSE_KEY"),
		MaxMindDBPath:     getEnv("MAXMIND_DB_PATH", "/var/lib/ctech-account/GeoLite2-City.mmdb"),
```

- [ ] **Step 2: Wire `geoupdater` into `main.go`**

In `api/cmd/api/main.go`, add near the JWK rotation block (after it, before the repository construction section starting at `userRepo := ...`):

```go
	// GeoIP: per-instance auto-updating local MaxMind database. Disabled
	// (geo.Lookup always zero-value) when credentials aren't configured —
	// same pattern as KYCDocumentsBucket/PhoneVerificationEnabled.
	if cfg.MaxMindAccountID != "" && cfg.MaxMindLicenseKey != "" {
		geoCfg := geoupdater.Config{
			DBPath:     cfg.MaxMindDBPath,
			AccountID:  cfg.MaxMindAccountID,
			LicenseKey: cfg.MaxMindLicenseKey,
			Interval:   geoupdater.DefaultInterval,
			StaleAfter: geoupdater.DefaultStaleAfter,
			Now:        time.Now,
		}
		geoupdater.Startup(ctx, geoCfg)
		go geoupdater.Run(ctx, geoCfg)
	} else {
		log.Println("MAXMIND_ACCOUNT_ID/MAXMIND_LICENSE_KEY not set — GeoIP lookups disabled")
	}
```

Add `"gopkg.aoctech.app/account/api/internal/geoupdater"` to `main.go`'s imports.

- [ ] **Step 3: Run the full test suite**

Run: `cd api && go build ./... && go vet ./... && go test ./...`
Expected: all packages compile, `go vet` is clean, all tests pass.

- [ ] **Step 4: CDK — fetch the two new secrets in `start.sh`, add the DB path as a static env var**

In `cdk/lib/compute-stack.ts`, add to the static env file block (after `KYC_DOCUMENTS_BUCKET=...`):

```typescript
      `MAXMIND_DB_PATH=/var/lib/ctech-account/GeoLite2-City.mmdb`,
```

And to `start.sh`'s secret-fetching block (after the `FROM_EMAIL=...` line):

```typescript
      `MAXMIND_ACCOUNT_ID=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/maxmind-account-id" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
      `MAXMIND_LICENSE_KEY=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/maxmind-license-key" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
```

And to the `export` list right after:

```typescript
      `export MAXMIND_ACCOUNT_ID`,
      `export MAXMIND_LICENSE_KEY`,
```

No IAM change is needed: `iam-stack.ts` already grants `ssm:GetParameter` on the whole `/ctech-account/${environment}/*` subtree (lines 49-55), which covers these two new parameter names.

Run: `cd cdk && npx cdk synth > /dev/null` to confirm the stack still synthesizes cleanly.

- [ ] **Step 5: Document in `README.md`**

Add a new subsection near the existing GeoIP-adjacent content (search for where session/device info is documented, or add after "### Rate limiting (`429`)"):

```markdown
### GeoIP + new-device login notification

Session geo-enrichment (`geo_city`/`geo_region`/`geo_country`/`geo_latitude`/`geo_longitude`) comes
from a local MaxMind GeoLite2 City database (`internal/geo`), looked up synchronously at login —
no third-party API call, no async enrichment race. The database auto-updates per-instance
(`internal/geoupdater`): downloaded once at boot if missing, refreshed every 24h once 7+ days old,
directly from MaxMind using `MAXMIND_ACCOUNT_ID`/`MAXMIND_LICENSE_KEY`. Absent either credential,
GeoIP stays disabled and geo fields are simply empty (never fails a login).

When a login's device name + country combination doesn't match any of the user's existing
sessions, the login gets `new_device: "true"` audit metadata and an email notification
(`email.SendNewDeviceLoginEmail`).
```

Update the SSM parameter list at "§2 — Provision the signing key" to mention the two new params:

```markdown
`base-url`, `allowed-origins`, `app-url`, `google-client-id`, `google-client-secret`,
`cookie-domain`, `from-email`, `internal-token`, `maxmind-account-id`, `maxmind-license-key`
```

(Replacing the existing sentence's parameter list, same location.)

- [ ] **Step 6: Update `PLAN.md`**

Add two rows to the "SSM Parameters Required" table:

```markdown
| `/ctech-account/{env}/maxmind-account-id`   | SecureString | MaxMind account ID for GeoLite2 City auto-updates          |
| `/ctech-account/{env}/maxmind-license-key`  | SecureString | MaxMind license key for GeoLite2 City auto-updates          |
```

Add a bullet to "Architecture Notes":

```markdown
- GeoIP: local MaxMind GeoLite2 City DB (`internal/geo`), per-instance auto-update every 24h once
  7+ days stale (`internal/geoupdater`, no distributed lock — no shared store for this file across
  the ASG). New-device login (device name + country not seen before) gets `new_device: "true"` audit
  metadata + email notification (see README §GeoIP + new-device login notification).
```

- [ ] **Step 7: Final full-repo check**

Run: `cd api && go build ./... && go vet ./... && go test ./...` and `cd cdk && npx cdk synth > /dev/null`.
Expected: everything green.

- [ ] **Step 8: Commit**

```bash
git add api/internal/config/config.go api/cmd/api/main.go cdk/lib/compute-stack.ts README.md PLAN.md
git commit -m "feat(geo): wire MaxMind auto-update config, CDK secrets, and docs"
```

---

## Cross-Project Impact (carried from the spec — restate at PR time)

None. No JWT/JWKS/OAuth flow change. `ctech-dfe`/`ctech-wallet` don't call these endpoints and don't consume `Session` or `account_audit` records.
