# Keystore rotator: every tick has a deadline

**Incident (prod, 2026-11-13):** the rotator on `app2` rotated the signing key at 03:15Z (SSM `jwk/active` v4,
`eb645987`). The sibling process `app` (same host, behind nginx) never reloaded: for ~21h
`/.well-known/jwks.json` alternated between `[eb645987, ce2d986c]` and the stale `[ce2d986c, ea5bf76a]`.
Verifiers that cached the stale set (ctech-wallet, ctech-poker) answered 401 for tokens signed with `eb645987`,
so poker's M2M wallet debit/credit failed in bursts. No `rotation tick failed` line was ever logged.

**Cause (inferred, evidence lost to the restart):** `RunRotator` runs ticks serially under a process-lifetime
context. An SSM/Valkey call that never returns (no per-call deadline) blocks the loop forever, silently.

**Fix:** `tickWithDeadline` runs each tick under `tickTimeout` (30s); a hung call becomes a logged
`keystore: rotation tick failed ... context deadline exceeded` and the next interval retries.
Test: `TestTickDeadlineUnblocksHungSSM`.

**Mitigation applied:** `rc-service app restart` on the account host; JWKS converged to one key set.
