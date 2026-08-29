# Runbook — migrating ctech-dfe organizations into the platform model

**Tool:** `api/cmd/migrate-dfe-orgs`
**Plan:** [2026-08-29-platform-organizations.md](2026-08-29-platform-organizations.md) (Task 9)
**Decision:** [ctech-billing ADR 0021](../../../ctech-billing/docs/adr/0021-platform-organizations-and-companies.md)

## What it does

Reads `{dfe-prefix}_organizations` and `{dfe-prefix}_organization_users`, writes
`{prefix}_account_organizations` and `{prefix}_account_memberships`.

**It deletes nothing.** ctech-dfe keeps every row. The rollback is deleting the
rows this wrote — nothing has to be restored.

**It is dry-run by default.** Without `-apply` it reads, decides and prints, and
writes nothing.

**It is safe to run again.** Each imported organization carries
`source_system=dfe` and `source_ref={dfe pk}`, found through the sparse
`lookup-index`. A second run recognizes what it already imported. It also
*completes* a partial import: a run that died between the organization and its
third member is finished by the next one, rather than skipped as "already
migrated".

## What does not come along

The CNPJ. A dfe organization is keyed `CNPJ_…`, and that tax entity stays in dfe
as a Company. Reusing it as the platform id would put a tax id in the partition
key of every ctech-billing row forever (ADR 0021). The new id is a fresh UUIDv7;
the dfe key survives as `source_ref`.

Also left behind: the fiscal person, addresses, certificates, pickup locations,
authorized XML viewers. None of it is tenant identity.

## The five things it refuses to decide

Any of these puts the organization in the **NEEDS A HUMAN** bucket and it is not
migrated:

1. **`owner_user_id` is empty.** dfe's own repair path derives it from the
   oldest OWNER row (`services/billing.go:990`). This does not: inferring who
   owns a company is not a default.
2. **The OWNER row disagrees with `owner_user_id`.** dfe contradicting itself is
   a question.
3. **A member does not exist in the account store.** Never written — a
   membership pointing at nobody is an access grant that cannot be audited.
4. **A member's dfe role has no equivalent here.** `OWNER/ADMIN/USER/VIEWER` map
   cleanly (`USER → member` is the only rename); anything else is reported.
5. **A member carries extra dfe `permissions`.** This model has no permissions,
   deliberately. Importing one silently deletes access somebody was explicitly
   given, and they find out when a screen is gone.

One dfe organization becomes one platform organization — never one per owner.
Two CNPJs owned by the same person stay two workspaces until a human merges
them, because merging is irreversible and splitting is not.

## Order of operations

**Deploy the DynamoDB stack before running anything.** The three tables and
their `lookup-index` must exist first — the same ordering the `kyc-level-index`
work required.

```bash
cd api

# 1. Rehearse against dev. Read every line of the report.
go run ./cmd/migrate-dfe-orgs -dfe-table-prefix dev_dfe -table-prefix dev

# 2. One organization, for real, in dev.
go run ./cmd/migrate-dfe-orgs -dfe-table-prefix dev_dfe -table-prefix dev \
    -org CNPJ_11111111000191 -apply

# 3. The rest of dev.
go run ./cmd/migrate-dfe-orgs -dfe-table-prefix dev_dfe -table-prefix dev -apply

# 4. Run it again. Everything must say "already imported; nothing to do".
go run ./cmd/migrate-dfe-orgs -dfe-table-prefix dev_dfe -table-prefix dev -apply
```

Then production, same four steps with `-dfe-table-prefix prod_dfe -table-prefix prod`.

## Reading the report

```
── CNPJ_11111111000191  "Contabilidade Silva"
   owner usr_01J…, 2 further member(s)
   created 019… with 3 membership(s)

── CNPJ_22222222000191  "Transportes Souza"
   NEEDS A HUMAN: member usr_abc carries extra dfe permissions [nfe.emit] that this model cannot express
   not migrated

1 migrated, 0 already there, 1 need a human
```

The exit code is non-zero whenever anything landed in the third bucket, so no
pipeline reports success over a partial migration.

**Resolving a "needs a human"** means fixing it *in dfe* — set the missing
`owner_user_id`, reconcile the OWNER row, decide what an extra permission
becomes here (usually: promote the member to `admin`, or accept the loss) — and
then running the tool again. Nothing is edited here to work around a question
there.

## After it is done

Nothing reads these rows yet. Phase 2 is what makes ctech-billing and ctech-dfe
resolve the tenant from here, and it is a separate change — which is why this
migration is safe to run early and to run repeatedly.
