# Spec: CTech Wallet → CTech Ledger display rename

- **Status:** Implemented (2026-09-08)
- **Project:** `ui/` (Next.js legal/console frontend)

## 1. Background

Our BaaS provider (Asaas) forbids using the word "Wallet" in how the product presents
itself to end users — we are a technical integrator on their regulated payment rails, not
a licensed financial institution, and "Wallet" reads as a claim to that status. The wallet
product's **display name** changes from "CTech Wallet" to "CTech Ledger". CTech Account
keeps its name; only the wallet product's public-facing branding changes.

## 2. What changed

- Non-versioned UI chrome and current legal content now say "CTech Ledger":
  `ui/src/app/products/wallet/page.tsx` (page `<title>`), `ui/src/lib/legal-documents.ts`
  (`legalGroups` product listing label, and the `wallet` document's title/intro), and the
  two current top-level documents that mention the product in passing
  (`ui/src/app/terms/page.tsx`, `ui/src/app/privacy/page.tsx`).
- A new legal version, **v3**, was published for the Wallet terms addendum
  (`ui/src/app/products/wallet/v3/page.tsx`, `legalDocuments['wallet-v3']`, version `3.0`,
  dated 2026-09-08). It carries the same substantive terms as the version it supersedes,
  with "CTech Wallet" replaced by "CTech Ledger" throughout. This is the new current
  version and users who accepted the prior version are re-gated on next acceptance flow
  (existing acceptance-tracking behavior, versioned by exact string match — see
  `api/internal/legal/version.go` for the equivalent pattern on ToS/Privacy).
- The previously-current content (version `2.2`, "CTech Wallet") was frozen verbatim as a
  new historical entry, also named `wallet-v3` at route `/products/wallet/v3` — mirroring
  the existing v1→v2 pattern, where the prior "current" content is frozen into the next
  sequential versioned route and the non-versioned route/`legalDocuments.wallet` entry is
  updated in place to the new text.
- `ui/src/components/legal-page-layout.tsx`'s `WALLET_VERSION_HISTORY` gained the `3.0`
  entry (`href: /products/wallet`) and the frozen `2.2` entry now points at
  `/products/wallet/v3` instead of `/products/wallet`.

## 3. What did NOT change

- Historical legal versions `/products/wallet/v1`, `/products/wallet/v2`,
  `/terms/v1`, `/terms/v2`, `/privacy/v1` — these still say "CTech Wallet" verbatim, as a
  historical record of what users actually accepted at the time.
- "CTech Account" and "CTech DFe" occurrences — unaffected by this rename.
- Internal/engineering identifiers that are not end-user-facing display text, e.g. the Go
  OAuth scope catalog's `internal:wallet` service name (`api/internal/scopes/catalog.go`)
  and the wallet API/service names generally — out of scope for a display-name-only change.
