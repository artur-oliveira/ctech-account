# Resource Server Scope Registry

CTech Account is both the Authorization Server and a Resource Server, while
remaining the central policy point. Its own API permissions live in
`api/internal/scopes/account-scope-manifest.json`; DF-e, Wallet, Poker, and
future APIs own equivalent manifests and reconcile them through the internal
registry during their deploy.

## Trust and ownership

- An operator provisions the Resource Server once with an immutable HTTPS
  audience and a dedicated confidential publisher client.
- The publisher receives only `internal:account:scope-registry:write` and its
  OAuth client row is bound by `managed_resource_id` to exactly one resource.
- Both the bearer scope and that binding are checked on `GET/PUT
  /v1.0/internal/resource-servers/{id}/manifest`. A DFe publisher cannot modify
  Wallet or Poker.
- Manifests may declare only concrete scopes in `resource:*` or
  `internal:resource:*`; identity and wildcard scopes are rejected. `account`
  is reserved from operator provisioning and may be reconciled only by the
  Account process itself.
- Publishing updates the catalog and appends active public scopes to an
  existing first-party public OAuth client whose `client_id` equals the
  Resource Server ID (`wallet` → `wallet`). This convention lets the owning UI
  request its full manifest without Account knowing service-specific scope
  names. Other OAuth clients and API keys are never granted automatically.

## Reconciliation semantics

The current item uses `pk=RESOURCE_SERVER`, `sk={id}` in the existing
`{env}_ctech_scopes` table. Every changed publish atomically writes an immutable
history item under `pk=RESOURCE_SERVER_HISTORY#{id}`. Revision and SHA-256 hash
are returned as an ETag.

`PUT` requires the ETag from `GET` in `If-Match`. Equal canonical manifests are
idempotent and do not advance the revision. A stale revision returns 412.
An active scope cannot disappear in one release: publish it as `deprecated`
first, then remove it in a later release. Deprecated scopes remain resolvable
for existing tokens but are excluded from discovery and new grants.

The same publish is also idempotent for the first-party UI grant: it preserves
redirect URIs, audience, identity scopes and all existing grants, appending only
missing active public scopes. API-only resources may omit the same-ID client.
If that ID exists but is not a first-party public client, reconciliation fails
closed so a publisher cannot promote or rewrite an unrelated client.

V2 rows override legacy `pk=SERVICE` rows at read time. This makes migration
non-disruptive. `cmd/seedscopes` remains only for built-in OIDC identity scopes,
the registry root permission, and legacy compatibility data.

## Account's system-owned resource

At API startup, `RegistryService.BootstrapAccount` creates or reconciles
`RESOURCE_SERVER/account` with the runtime `AUDIENCE`, the embedded manifest,
and reserved publisher `system://ctech-account`. This publisher is not an OAuth
client and cannot be created through `cmd/createresource`, avoiding a circular
dependency where Account would need a token from itself before it could boot.
`AUDIENCE` defaults to the stable public `APP_URL` resource identifier
(`https://accounts.aoctech.app` in production), independently of the API's
transport origin.

Startup also creates or reconciles the system-owned public `SELF_CLIENT_ID`
client, `${APP_URL}/login/callback`, and all Account SPA scopes. Every
`/v1.0/account/*` operation requires its exact `account:*` permission; a
delegated client or API key with the correct audience and grant can therefore
use the Resource Server normally. Only the SPA-specific step-up protocol keeps
the `SELF_CLIENT_ID` binding. The registry root scope is duplicated in the small
bootstrap seed so downstream publishers can be provisioned before the Account
v2 row exists; after startup the v2 manifest is authoritative.

## Bootstrap a service

Run once per environment from `ctech-account/api` (use `dev`, `stage`, or
`prod` consistently in table prefix and SSM paths):

Deploy Account and run `cmd/seedscopes` first so the Account-owned publisher
management scope exists in the runtime catalog.

```bash
AWS_REGION=us-east-1 ENVIRONMENT=dev go run ./cmd/createresource \
  -id dfe -name "CTech DF-e" -audience https://dfe-dev.aoctech.app \
  -publisher-client-id scope-publisher-dfe \
  -ssm-path-client /ctech-account/dev/scope-publishers/dfe/client-id \
  -ssm-path-secret /ctech-account/dev/scope-publishers/dfe/client-secret
```

Repeat for `wallet` and `poker` with their audiences. The service CDK creates a
dedicated GitHub OIDC role that can read only Account's URL and that service's
two publisher parameters. Its deploy calls
`.github/workflows/publish-resource-scopes.yml` after infrastructure and before
the API. The workflow calls the direct API `base-url`, but this does not alter
the Account audience. It derives `If-Match` from the authoritative `revision`
and `manifest_hash` fields in the `GET` body, avoiding ETag rewriting by
CloudFront or another intermediary.

For legacy data, run `go run ./cmd/migratescopes` first as a dry run, then with
`-apply`. To recover a bad publish, use `go run ./cmd/restoreresource -id dfe
-revision 3 -expected-revision 7`; restoration creates a new revision rather
than rewriting history.

## Manifest schema

```json
{
  "schema_version": 1,
  "resource_server_id": "example",
  "display_name": "CTech Example",
  "scopes": [
    {
      "name": "example:records:read",
      "descriptions": {
        "en": "Read records.",
        "pt-BR": "Consultar registros."
      },
      "visibility": "public",
      "status": "active"
    }
  ]
}
```

The audience is deliberately absent: it is environment-specific and remains
operator-owned. Resource APIs publish it at
`/.well-known/oauth-protected-resource` according to RFC 9728.
