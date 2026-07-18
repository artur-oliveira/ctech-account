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
