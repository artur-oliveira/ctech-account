# CLAUDE.md — ctech-account (monorepo root)

OAuth 2.0 + OpenID Connect Identity Provider for the aoctech.app platform.

**Before any task:** Read `README.md` and `PLAN.md`.

---

## Projects

| Path   | Role                                       | Full guidelines |
|--------|--------------------------------------------|-----------------|
| `api/` | Go API — OAuth 2.0 + OIDC backend          | `api/CLAUDE.md` |
| `ui/`  | Next.js 16 frontend — accounts.aoctech.app | `ui/CLAUDE.md`  |
| `cdk/` | AWS CDK infrastructure — TypeScript        | `cdk/CLAUDE.md` |

**Always read the relevant subproject CLAUDE.md before making any change.**

---

## Cross-Project Impact

Changes to JWT signing, JWKS, or OAuth flows affect downstream JWT consumers (`ctech-dfe`,
`ctech-wallet`). State cross-project impact (ui, cdk, ctech-dfe, ctech-wallet) in every plan
that touches auth.

## Secrets

Never commit: RSA private keys (`key.pem`), JWT secrets, AWS credentials, real user data, real passwords.

## CTech Family — Cross-Repo Awareness (IMPORTANT)

This repo is one service in the CTech product family, not an isolated project. All CTech repos live under the same GitHub account and are meant to be treated as one codebase split across repos:

- ctech-cdk (github.com/artur-oliveira/ctech-cdk) — shared CDK constructs (EC2/ASG, DynamoDB, etc.)
- ctech-go-common (github.com/artur-oliveira/ctech-go-common) — shared Go libraries (HTTP client, auth, retries, websocket drain, caching)
- ctech-account, ctech-wallet, ctech-billing, ctech-dfe, ctech-poker — backend services
- ctech-ui (github.com/artur-oliveira/ctech-ui) — shared frontend design system / components (adoption in progress)
- ctech-ws-client (github.com/artur-oliveira/ctech-ws-client) — shared websocket client library
- ctech-oauth-client, ctech-vanity, ctech-lbalancer — supporting infra/clients

Before making a decision here, ask: "does this apply to the whole family, not just this repo?" Treat as cross-repo by default:
- Infra/runtime bugs (clock drift, spot interruption handling, websocket draining, health checks, load balancer behavior) — check ctech-cdk / ctech-lbalancer and sibling services for the same exposure before treating it as local.
- API leaks/perf/cost bugs (DynamoDB read/write amplification, KMS decrypt calls, SQS growth, N+1 requests) — check whether the root cause is shared code (ctech-go-common) or a repeatable pattern other services also have.
- Frontend state/websocket/resilience/UX patterns (reconnect, circuit breaker, error/loading/empty states, 404/500/503 pages, OAuth flow, modals, buttons) — check ctech-ui and ctech-ws-client for the shared version before implementing locally.
- New reusable code (not service-specific business logic) — default to proposing it for a shared package (ctech-cdk, ctech-go-common, ctech-ui, ctech-ws-client) instead of duplicating it here.

A fix scoped to only this repo, for a problem that is actually systemic across the family, is an incomplete fix. This applies to AI agents working in single-repo sessions too.

ctech-account is the auth backbone of the family: it issues and validates the sessions and
tokens every other service trusts, and it owns the shared Valkey dependency those flows need.
A regression here (session/token issues, Valkey outage or latency) cascades to every dependent
service (wallet, billing, poker, dfe), not just this repo — treat any change to auth flow or
session behavior as especially high-blast-radius.

## Mandatory Documentation Policy

**Every code change MUST be documented.**

There are NO exceptions.

Any modification affecting behavior, architecture, APIs, integrations, configuration, deployment, security, business rules, or developer workflow MUST include the corresponding documentation update in the same change.
