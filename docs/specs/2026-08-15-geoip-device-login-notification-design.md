# GeoIP Replacement + New-Device Login Notification — Design Spec

Date: 2026-08-15
Status: Approved (design), pending implementation plan
Scope: ctech-account (Go API + cdk secret only; no UI change, no new CDK bucket/Lambda)

## Problem

`internal/geo/geo.go` calls the free `freeipapi.com` API (no key, 3s timeout) from a fire-and-forget
goroutine (`enrichSessionAsync`) fired after every login. The API now appears to be blocking/rate-limiting
this deployment — errors are silently swallowed, so sessions just end up with empty geo fields, with no
alerting. Separately, there's no login notification for a new device/location today, and no "known device"
concept exists anywhere in the domain model.

## Decided approach

Replace the third-party geo API with a local MaxMind GeoLite2 City database, read synchronously (no
network round-trip, so the async-enrichment goroutine goes away). Use the new country field this format
provides — which `freeipapi` never returned — to detect devices/locations not seen before, by comparing
against the user's existing `Session` records (no new device-identity table). Auto-update the local
`.mmdb` file with a per-instance background goroutine, mirroring the existing JWT key-rotation pattern in
`internal/keystore/rotator.go` (in-process ticker, no CDK scheduling), adapted for the fact that deployment
is EC2 + ASG with no shared central store for this file (each instance downloads its own copy directly
from MaxMind — no distributed lock needed, since there's no shared state to corrupt).

## A. GeoIP lookup

- `internal/geo/geo.go`: replace the HTTP client with `github.com/oschwald/geoip2-golang`, backed by a
  `*geoip2.Reader` over a local `.mmdb` file, guarded by an `atomic.Pointer` (swapped by the updater, read
  by every lookup — no mutex contention on the hot path).
- `Lookup(ip)` becomes synchronous and in-process (microseconds, no timeout handling needed). Returns
  `Location{City, Region, Country, Latitude, Longitude}` — `Country` is new (GeoLite2 City includes
  `Country.IsoCode`; freeipapi never provided it).
- Callers (`auth.go` login, MFA challenge, `social.go` Google login) call `geo.Lookup` inline before
  `session.Service.Create`, instead of the current post-creation `enrichSessionAsync` goroutine. Delete
  `enrichSessionAsync` and `session.Service.UpdateGeoData` (no longer needed — geo is known at create time).
- If the reader hasn't loaded yet (fresh instance, first download still in flight) or lookup fails,
  `Lookup` returns a zero `Location` — geo fields stay empty exactly as they silently do today. Not
  finding geo data must never fail a login.

## B. Local `.mmdb` auto-update (per-instance)

- New `internal/geoupdater` package, structurally parallel to `internal/keystore/rotator.go`:
    - Startup: if `MaxMindDBPath` has no file (or the file is unreadable), block startup on one synchronous
      download attempt; if that fails, log and continue with geoip disabled (`geo.Lookup` returns zero
      values) rather than crash-looping the instance over a non-critical feature.
    - Background goroutine: `time.Ticker` at 24h (constant, not env-configurable — no proven need yet),
      jittered 0–1h on first tick so a fleet-wide deploy doesn't hit MaxMind simultaneously.
    - On each tick: if local file's mtime age > 7 days (GeoLite2 ships weekly), download
      `https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-City&license_key=...&suffix=tar.gz`,
      extract the `.mmdb` from the tarball into a temp file, open a new `geoip2.Reader` on it (to validate it
      parses before committing), then rename-over the live path and atomically swap the reader pointer.
    - No distributed lock — unlike JWT rotation there's no shared store (SSM) all instances read from; each
      EC2 instance in the ASG maintains its own local copy independently. Redundant per-instance downloads
      against MaxMind's own download endpoint are expected and within normal license use.
- Config (`internal/config`): `MaxMindAccountID`, `MaxMindLicenseKey` (new SSM `SecureString` params,
  same mechanism as existing secrets), `MaxMindDBPath` (local disk path, e.g.
  `/var/lib/ctech-account/GeoLite2-City.mmdb`).
- CDK: one new SSM parameter definition + instance role `ssm:GetParameter` on it (mirrors the existing
  pattern for other secrets). No new bucket, no Lambda, no EventBridge rule. Assumes outbound HTTPS egress
  already reaches the public internet from the ASG (already required for Google OAuth, SES) — verify
  during implementation rather than assume.

## C. New-device / new-location detection

- No new table. `session.Service` gains `HasSeenDevice(ctx, userID, deviceName, country string) (bool,
  error)`: lists the user's existing (non-expired) sessions and checks whether any prior session — other
  than the one being created — matches both `DeviceName` (existing UA-derived label) and the new
  `GeoCountry` field.
- `Session` model gains `GeoCountry` (`geo_country`), set at creation from the synchronous `geo.Lookup`
  call (alongside the existing `GeoCity`/`GeoRegion`/`GeoLatitude`/`GeoLongitude`).
- Called from the same post-login code paths in `auth.go`/`social.go`, right after computing geo and
  before/around `session.Service.Create`. If `HasSeenDevice` returns false (including on lookup error —
  fail toward *not* alerting, since a false negative here is a missed email, not a security hole; the
  login itself is unaffected either way), send the notification (below).

## D. Notification email

- `internal/email/ses.go` gains `SendNewDeviceLoginEmail(to, deviceName, city, country, ip string,
  when time.Time)`, following the existing `SendPasswordResetEmail` pattern (plain HTML via
  `fmt.Sprintf`/`emailLayout()`, pt-BR copy, no template engine).
- Sent via a short-lived goroutine after the response-relevant work is done (only the email I/O is
  backgrounded — the geo lookup and device check are already synchronous and cheap, so there's no
  correctness reason to background those too).

## E. Audit enrichment

- `audit.EventLoginSuccess` gains a `new_device: "true"` metadata entry when `HasSeenDevice` is false.
  No new event type — this reuses the existing audit write already happening on every login success.

## Out of scope (decided)

- Push notifications — email only.
- Device fingerprinting (canvas/WebAuthn-attestation-based) — that's item 3 (risk-based auth), which will
  likely supersede the coarse `DeviceName`+`Country` heuristic here later.
- CDK automation beyond the one new SSM secret (no S3 bucket, no Lambda, no EventBridge) — each instance
  self-updates independently.

## Cross-project impact

None. No JWT/JWKS/OAuth flow change. `ctech-dfe`/`ctech-wallet` don't call these endpoints and don't
consume `Session` or `account_audit` records.

## Testing

- Unit: `geo.Lookup` against a small test `.mmdb` fixture (MaxMind ships a test DB for exactly this);
  `HasSeenDevice` table-driven over session histories (empty history, matching device, matching country
  only, matching device only, both mismatch); email template rendering; audit metadata includes
  `new_device` only when expected.
- `geoupdater`: fake HTTP server serving a fixture tarball, assert atomic swap happens and old reader
  keeps serving requests until swap completes; startup-missing-file path degrades to disabled geoip
  rather than failing boot.
- Regression: existing login/session tests continue to pass with `GeoCountry` defaulting to empty string
  for pre-existing sessions (zero value, no backfill).

## Rollout order

1. Geo lookup swap (A) + config — no behavior change visible to users yet (geo fields just populate
   again), lowest risk, unblocks everything else.
2. Auto-updater (B) — needed before (A) can stay working past the first 7 days in any environment.
3. Device detection (C) + email (D) + audit (E) — the actual user-facing feature, built on top of A+B.
