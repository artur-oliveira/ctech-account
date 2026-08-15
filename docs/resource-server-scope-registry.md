# Resource Server Scope Registry

CTech Account is the Authorization Server and central policy point, but it no
longer needs a code change for every downstream permission. DF-e, Wallet,
Poker, and future APIs own a versioned `scope-manifest.json` and reconcile it
through the internal registry during their deploy.

## Trust and ownership

- An operator provisions the Resource Server once with an immutable HTTPS
  audience and a dedicated confidential publisher client.
- The publisher receives only `internal:account:scope-registry:write` and its
  OAuth client row is bound by `managed_resource_id` to exactly one resource.
- Both the bearer scope and that binding are checked on `GET/PUT
  /v1.0/internal/resource-servers/{id}/manifest`. A DFe publisher cannot modify
  Wallet or Poker.
- Manifests may declare only concrete scopes in `resource:*` or
  `internal:resource:*`; Account/identity namespaces and wildcards are rejected.
- Publishing changes the catalog only. It never grants the new scope to an
  existing OAuth client or API key.

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

V2 rows override legacy `pk=SERVICE` rows at read time. This makes migration
non-disruptive and leaves `cmd/seedscopes` available for Account's built-in
identity/account scopes.

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
the API.

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
