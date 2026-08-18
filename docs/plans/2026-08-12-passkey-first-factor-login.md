# Passkey: production hotfix and true first-factor login

## Status

Implemented and hardened on 2026-08-12. Production WebAuthn verification on the real origin remains a post-deploy smoke
test.

## Context

Two independent defects existed:

1. WebAuthn configuration derived `RPID` and `RPOrigins` from `BASE_URL`, the API origin (`accountsapi.aoctech.app`),
   although ceremonies run in the SPA at
   `APP_URL` (`accounts.aoctech.app`). This caused production `SecurityError`s while remaining invisible locally, where
   both URLs default to localhost.
2. A registered passkey was advertised as a second factor after password login. Passkeys must replace the password as a
   primary factor; only TOTP remains a second factor.

## Security design decision

An initial prototype added `POST /v1.0/auth/passkeys/check` with
`{email} → {has_passkey}` and changed the form after email blur. That endpoint was removed during security review: a
`true` response necessarily confirms both account existence and its authentication method. Rate limiting and identical
`false` responses for unknown/no-passkey accounts do not eliminate that oracle.

The final implementation uses discoverable Conditional WebAuthn instead:

- the username input declares `autocomplete="username webauthn"`;
- browsers supporting `PublicKeyCredential.isConditionalMediationAvailable()`
  receive a discoverable challenge and call `navigator.credentials.get` with
  `mediation:"conditional"`;
- the browser offers only locally matching credentials without sending the typed email to an enrollment-probe endpoint;
- password, Google and an explicit passkey button remain available;
- unsupported, cancelled, offline and rate-limited conditional initialization fails open to those controls;
- beginning an explicit password or passkey action aborts the conditional ceremony so two login paths cannot complete
  concurrently.

## Backend implementation (`api/`)

### WebAuthn configuration hotfix

- `APP_URL` is resolved before WebAuthn configuration.
- Default `RPID` is `APP_URL`'s hostname, not `BASE_URL`'s hostname.
- `RPOrigins` starts with `APP_URL` and includes explicitly configured browser origins; the API's own origin is not
  implicitly accepted.
- Startup logs a warning when the configured RP ID cannot match an RP origin. Public suffixes such as `com` and `co.uk`
  are rejected by the match check.
- OAuth issuer and default audience still derive from `BASE_URL`.
- Regression tests cover split API/SPA origins, an explicit shared-domain RP ID, mismatch warnings and public-suffix
  rejection.

### First-factor passkey contract

- Removed `/v1.0/auth/mfa/passkey/begin|complete`.
- Password login considers only TOTP for `requires_mfa`; owning a passkey never blocks or changes password login.
- Kept discoverable `/v1.0/auth/passkeys/authenticate/begin|complete` for both Conditional UI and the explicit passkey
  button.
- Challenge creation is throughput-limited to 20 requests/minute/IP and fails closed when Valkey cannot enforce the
  limit.
- Successful and failed passkey authentication attempts are written to the security audit log.

### AMR correctness

The MFA token now preserves its primary authentication method across the TOTP handoff:

| Login chain     | Session/token `amr`  |
|-----------------|----------------------|
| Password        | `["pwd"]`            |
| Password + TOTP | `["pwd","otp"]`      |
| Passkey         | `["webauthn"]`       |
| Passkey + TOTP  | `["webauthn","otp"]` |

During a rolling deployment, old five-minute MFA tokens lack `primary_amr`. Compatibility logic recognizes the legacy
`DeviceName="Passkey"`; all other legacy tokens retain the password default.

## Frontend implementation (`ui/`)

- Login inputs remain controlled and available throughout the flow.
- Conditional UI is progressive enhancement; no account-enrollment probe exists.
- The explicit passkey flow reuses the same discoverable begin/complete API.
- Explicit `AbortError`/`NotAllowedError` cancellation gets dedicated copy rather than being mislabeled as a network
  failure.
- Conditional and explicit ceremonies accept an `AbortSignal`; component cleanup and explicit login actions cancel
  pending mediation.
- Mobile form controls and auth buttons use at least 44px touch targets.
- `/login/mfa` is TOTP-only even if an older backend briefly supplies `"passkey"`
  in `mfa_methods` during rollout.
- English and pt-BR locales document the cancellation state.

## Infrastructure and configuration (`cdk/`)

The EC2 bootstrap reads the optional parameter
`/ctech-account/{env}/webauthn-rpid` and exports `WEBAUTHN_RPID`. When absent, the API safely defaults to `APP_URL`'s
hostname. The instance role already grants read access to `/ctech-account/{env}/*`.

Use the override only when several SPA subdomains intentionally share passkeys. For the normal single-origin deployment,
keep it absent.

## Tests

Backend coverage includes:

- password login for an account with passkey and no TOTP creates a direct session;
- the enumerating `/auth/passkeys/check` route does not exist;
- passkey failures create `login.failed` audit events with `method=webauthn`;
- password and passkey primary AMR survive the TOTP handoff, including a legacy passkey MFA token;
- split-origin WebAuthn configuration and mismatch/public-suffix cases.

Frontend coverage includes:

- password/passkey/Google fallbacks without Conditional UI support;
- explicit passkey completion;
- explicit cancellation copy;
- Conditional UI invocation with `mediation:"conditional"` and `AbortSignal`;
- fail-open behavior without conditional mediation;
- TOTP-only MFA rendering and submission.

## Rollout

1. Deploy the configuration/backend change. Existing frontend builds remain compatible because password login stops
   advertising passkey as MFA, while the discoverable passkey endpoints remain stable.
2. Deploy the Conditional UI frontend.
3. Confirm `APP_URL=https://accounts.aoctech.app` and
   `BASE_URL=https://accountsapi.aoctech.app`. Do not set `WEBAUTHN_RPID` unless shared-subdomain credentials are
   required.

## Cross-project impact

- `ui`: Conditional UI and TOTP-only MFA behavior change.
- `cdk`: optional WebAuthn RP ID SSM loading is added.
- `ctech-dfe` and `ctech-wallet`: new passkey+TOTP sessions correctly expose
  `amr=["webauthn","otp"]`. Consumers must not assume all TOTP sessions used a password. No static reference to the
  removed `["pwd","webauthn"]` combination was found during the original review; owners should still confirm before
  rollout.
- JWT signing, JWKS, issuer, audience and token lifetimes do not change.

## Verification

Automated gates:

```bash
cd api && go test ./... && go build ./...
cd ui && npm test && npx eslint src --ext .ts,.tsx && npm run build
cd cdk && npm run build && npx cdk synth
```

Post-deploy browser smoke test:

1. Register a passkey at `https://accounts.aoctech.app`, log out, focus the email field and select the credential
   offered by Conditional UI; confirm session and OAuth continuation.
2. Repeat with the explicit passkey button.
3. Cancel the explicit prompt and confirm password/Google remain available with cancellation copy.
4. Log in by password on an account that also owns a passkey and has no TOTP; confirm there is no MFA lockout.
5. On an account with passkey + TOTP, confirm the resulting access token reports
   `amr=["webauthn","otp"]` and its activity log records the login.
6. Verify the real production origin, not only staging; this is the only reliable confirmation of the original
   `SecurityError` outage.
