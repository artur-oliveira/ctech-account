# Platform Companies Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ctech-account` stores a Company — a CNPJ or CPF an organization acts for, with its names and a per-person "may act for" edge — and exposes it to `ctech-dfe` and `ctech-billing`.

**Architecture:** One new DynamoDB table, `{env}_account_companies`, holding three row kinds under `pk = ORG#{organization_id}`: the company (`sk = COMPANY#{company_id}`), a tax-id lock (`sk = TAXID#{digits}`) that makes uniqueness-within-organization a conditional write rather than a read-then-write, and the actor edge (`sk = ACTOR#{company_id}#{user_id}`). A `lookup-index` GSI on `lookup_pk` answers both "which companies may this person act for" (`USER#{id}`) and "have I imported this one" (`SOURCE#{system}#{ref}`), mirroring what the organizations tables already do. Authorization reuses `middleware.RequireOrgRole` unchanged.

**Tech Stack:** Go 1.27, Fiber v3, DynamoDB via `gopkg.aoctech.app/api-commons/dynamo`, AWS CDK (TypeScript), Next 16 static export + React 19 + TanStack Query 5 + shadcn on `@base-ui/react`.

**Spec:** [`docs/specs/2026-08-29-platform-companies.md`](../specs/2026-08-29-platform-companies.md), which amends [ctech-billing ADR 0022](../../../ctech-billing/docs/adr/0022-company-identity-in-account.md).

## Scope: this plan is `ctech-account` only

The spec's `ctech-dfe` consequence — re-keying `organization_nfe_configs` and its siblings from the organization PK to a `company_id`, and a second migration pass — is a production migration in another repo with its own runbook. It is **a separate plan**, and this one deliberately stops at the contract it will consume (Task 6). Nothing here changes `ctech-dfe`.

## Global Constraints

- **Nothing fiscal.** No inscrição estadual, no CRT/regime, no fiscal address, no certificate — not even a boolean saying a certificate exists. Spec: *"identity is a fact about the company that serves everyone; configuration is one product knowing how to issue."*
- **No delete.** Fiscal documents reference a company and must stay referenceable. Same rule organizations already follow.
- **`tax_id` is canonical on write: mask stripped, letters uppercased — NOT digits.** A CNPJ's first twelve positions have been alphanumeric since the Receita Federal's 2026 change; only the two check digits stayed numeric. A CPF stayed numeric throughout. `tax_id_kind` is `cnpj` or `cpf` — never a CNPJ-only field; `ctech-dfe` keys `CPF_{digits}` today (`api/internal/repositories/organizations.go:16`).
- **The two documents do not share weights.** CNPJ weights cycle 2..9 from the right; CPF weights descend from 10. One sequence used for both still validates most inputs by luck.
- **`company_id` is a UUIDv7 and is never the tax id.** Uniqueness of `(organization_id, tax_id)` is a conditional write on a lock row.
- **The same tax id in two different organizations is allowed.** This is the accountant case and it is load-bearing; a test pins it so nobody "fixes" it into a global unique key.
- **The cnpja lookup is a convenience, never a gate.** An outage must not block registration.
- **Organization membership does not imply the actor edge.**
- **Go 1.27 stdlib uuid:** `uuid.NewV7()` returns one value. Import is bare `"uuid"`.
- Commit messages carry **no** `Co-Authored-By` trailer.

---

## File Structure

| File | Responsibility |
|---|---|
| `api/internal/domain/company/model.go` (create) | `Company`, `Actor`, tax-id normalization and check digits |
| `api/internal/domain/company/repository.go` (create) | keys, the three row kinds, the transactional create |
| `api/internal/domain/company/service.go` (create) | the rules: who may register, who may act |
| `api/internal/domain/company/taxid.go` (create) | CNPJ/CPF check digits — pure, no dependencies |
| `api/internal/domain/company/registry/cnpja.go` (create) | the outbound lookup, cached, non-gating |
| `api/internal/handler/company.go` (create) | routes, DTOs, problem mapping |
| `api/cmd/api/main.go` (modify) | wire the repository, service and handler |
| `cdk/lib/dynamodb-stack.ts` (modify) | the `account_companies` table |
| `ui/src/lib/types.ts` (modify) | `Company`, `CompanyActor` |
| `ui/src/lib/queries.ts`, `mutations.ts`, `mock.ts` (modify) | fetchers, mutations, mock routes |
| `ui/src/app/account/organizations/detail/companies-tab.tsx` (create) | the roster of companies inside an organization |

---

### Task 1: Tax id validation

**Files:**
- Create: `api/internal/domain/company/taxid.go`
- Test: `api/internal/domain/company/taxid_test.go`

**Interfaces:**
- Produces: `NormalizeTaxID(raw string) (digits string, kind string, ok bool)` — `kind` is `KindCNPJ` or `KindCPF`; `ok` is false when the digits are the wrong length or the check digits do not verify. Constants `KindCNPJ = "cnpj"`, `KindCPF = "cpf"`.

- [ ] **Step 1: Write the failing test**

```go
package company

import "testing"

func TestNormalizeTaxIDStripsAMaskAndNamesTheKind(t *testing.T) {
	digits, kind, ok := NormalizeTaxID("11.222.333/0001-81")
	if !ok || digits != "11222333000181" || kind != KindCNPJ {
		t.Fatalf("got %q %q %v, want 11222333000181 cnpj true", digits, kind, ok)
	}
	digits, kind, ok = NormalizeTaxID("529.982.247-25")
	if !ok || digits != "52998224725" || kind != KindCPF {
		t.Fatalf("got %q %q %v, want 52998224725 cpf true", digits, kind, ok)
	}
}

// A transposed digit is the typo this catches, and the reason a length check
// alone is not enough: 11222333000181 with two digits swapped is still 14 long.
func TestNormalizeTaxIDRejectsABadCheckDigit(t *testing.T) {
	for _, in := range []string{"11222333000182", "52998224726"} {
		if _, _, ok := NormalizeTaxID(in); ok {
			t.Errorf("%q: accepted, want rejected", in)
		}
	}
}

// Repeated digits pass the check-digit arithmetic and are the classic filler
// value. They must be refused explicitly or 00000000000000 is a valid CNPJ.
func TestNormalizeTaxIDRejectsRepeatedDigits(t *testing.T) {
	for _, in := range []string{"00000000000000", "11111111111"} {
		if _, _, ok := NormalizeTaxID(in); ok {
			t.Errorf("%q: accepted, want rejected", in)
		}
	}
}

func TestNormalizeTaxIDRejectsTheWrongLength(t *testing.T) {
	for _, in := range []string{"", "123", "112223330001812"} {
		if _, _, ok := NormalizeTaxID(in); ok {
			t.Errorf("%q: accepted, want rejected", in)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/domain/company/ -run TestNormalizeTaxID -v`
Expected: FAIL — the package does not compile, `undefined: NormalizeTaxID`.

- [ ] **Step 3: Write the implementation**

```go
// Package company owns who an organization issues documents for: the tax id,
// the names on it, and who may act for it.
//
// It holds nothing about issuing one — no inscrição estadual, no regime, no
// certificate. That is ctech-dfe's, and the split is ctech-billing ADR 0022.
package company

import "strings"

const (
	KindCNPJ = "cnpj"
	KindCPF  = "cpf"
)

// NormalizeTaxID strips a mask, verifies the check digits, and says which kind
// of document it is.
//
// The check digits are verified here rather than at the registry lookup
// because they are arithmetic, not a fact about the world: a valid CNPJ issued
// this morning is unknown to every public API and must still be accepted.
func NormalizeTaxID(raw string) (string, string, bool) {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if allSameDigit(digits) {
		return "", "", false
	}
	switch len(digits) {
	case 11:
		if !validCPF(digits) {
			return "", "", false
		}
		return digits, KindCPF, true
	case 14:
		if !validCNPJ(digits) {
			return "", "", false
		}
		return digits, KindCNPJ, true
	}
	return "", "", false
}

func allSameDigit(s string) bool {
	if s == "" {
		return true
	}
	for i := 1; i < len(s); i++ {
		if s[i] != s[0] {
			return false
		}
	}
	return true
}

// checkDigit computes one modulus-11 digit over the given weights, which is the
// same arithmetic for both documents — only the weights differ.
func checkDigit(digits string, weights []int) byte {
	sum := 0
	for i, w := range weights {
		sum += int(digits[i]-'0') * w
	}
	rem := sum % 11
	if rem < 2 {
		return '0'
	}
	return byte('0' + 11 - rem)
}

func validCPF(d string) bool {
	first := checkDigit(d, []int{10, 9, 8, 7, 6, 5, 4, 3, 2})
	second := checkDigit(d, []int{11, 10, 9, 8, 7, 6, 5, 4, 3, 2})
	return d[9] == first && d[10] == second
}

func validCNPJ(d string) bool {
	first := checkDigit(d, []int{5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2})
	second := checkDigit(d, []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2})
	return d[12] == first && d[13] == second
}
```

- [ ] **Step 4: Run the tests**

Run: `cd api && go test ./internal/domain/company/ -v`
Expected: PASS, four tests.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/company/taxid.go api/internal/domain/company/taxid_test.go
git commit -m "feat(company): tax id normalization with check digits

A length check is not enough: a transposed digit keeps the length. The
arithmetic is verified locally because a CNPJ issued this morning is
unknown to every public registry and must still be accepted."
```

---

### Task 2: The model

**Files:**
- Create: `api/internal/domain/company/model.go`
- Test: `api/internal/domain/company/model_test.go`

**Interfaces:**
- Consumes: `KindCNPJ`, `KindCPF` from Task 1.
- Produces: `Company{OrganizationID, ID, TaxID, TaxIDKind, LegalName, TradeName, SourceSystem, SourceRef, CreatedAt, UpdatedAt}`, `Actor{CompanyID, UserID, Name, GrantedBy, CreatedAt}`, `const maxCompanyName = 200`, `ValidateNames(legal, trade string) (string, string, bool)`.

- [ ] **Step 1: Write the failing test**

```go
package company

import "testing"

func TestValidateNamesTrimsAndRequiresALegalName(t *testing.T) {
	legal, trade, ok := ValidateNames("  Acme LTDA  ", "  Acme  ")
	if !ok || legal != "Acme LTDA" || trade != "Acme" {
		t.Fatalf("got %q %q %v", legal, trade, ok)
	}
	if _, _, ok := ValidateNames("   ", "Acme"); ok {
		t.Error("accepted an empty legal name")
	}
}

// The trade name is optional: most companies do not have one, and requiring it
// would make people type the razão social twice.
func TestValidateNamesAllowsAnAbsentTradeName(t *testing.T) {
	legal, trade, ok := ValidateNames("Acme LTDA", "")
	if !ok || legal != "Acme LTDA" || trade != "" {
		t.Fatalf("got %q %q %v", legal, trade, ok)
	}
}

func TestValidateNamesRejectsAnOverlongName(t *testing.T) {
	long := make([]byte, maxCompanyName+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, _, ok := ValidateNames(string(long), ""); ok {
		t.Error("accepted a name past the storage bound")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/domain/company/ -run TestValidateNames -v`
Expected: FAIL, `undefined: ValidateNames`.

- [ ] **Step 3: Write the implementation**

Append to `api/internal/domain/company/model.go`:

```go
package company

import (
	"strings"
	"time"
)

// maxCompanyName is a storage bound, not a product rule. A razão social is
// long; free storage is longer.
const maxCompanyName = 200

// Company is a tax id an organization acts for.
//
// It is scoped to its organization, and the same tax id may appear in two of
// them. That is the accountant holding a client's CNPJ while the client's own
// staff also issues — the ordinary shape of this market, and unrepresentable
// under a global unique key (ctech-billing ADR 0022).
type Company struct {
	OrganizationID string `dynamodbav:"-"`
	ID             string `dynamodbav:"-"`
	TaxID          string `dynamodbav:"tax_id"`
	TaxIDKind      string `dynamodbav:"tax_id_kind"`
	LegalName      string `dynamodbav:"legal_name"`
	TradeName      string `dynamodbav:"trade_name,omitempty"`
	// SourceSystem/SourceRef record where an imported company came from. Set
	// only by a migration, empty for anything created through the product.
	SourceSystem string    `dynamodbav:"source_system,omitempty"`
	SourceRef    string    `dynamodbav:"source_ref,omitempty"`
	CreatedAt    time.Time `dynamodbav:"created_at"`
	UpdatedAt    time.Time `dynamodbav:"updated_at"`
}

// Actor is one person's permission to act for one company.
//
// Separate from organization membership on purpose: an accountant's
// organization holds forty CNPJs and a junior handles five of them. Membership
// is what gets you into the workspace; this is what lets you act for a company
// inside it.
type Actor struct {
	OrganizationID string `dynamodbav:"-"`
	CompanyID      string `dynamodbav:"-"`
	UserID         string `dynamodbav:"-"`
	// Name is a cache of the person's display name, same rationale as
	// Membership.Name: a roster is one query instead of one plus a lookup per
	// row.
	Name      string    `dynamodbav:"name,omitempty"`
	GrantedBy string    `dynamodbav:"granted_by,omitempty"`
	CreatedAt time.Time `dynamodbav:"created_at"`
}

// ValidateNames trims both names and enforces the storage bound. The legal name
// is required; the trade name is not, because most companies do not have one.
func ValidateNames(legal, trade string) (string, string, bool) {
	legal = strings.TrimSpace(legal)
	trade = strings.TrimSpace(trade)
	if legal == "" || len(legal) > maxCompanyName || len(trade) > maxCompanyName {
		return "", "", false
	}
	return legal, trade, true
}
```

- [ ] **Step 4: Run the tests**

Run: `cd api && go test ./internal/domain/company/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/company/model.go api/internal/domain/company/model_test.go
git commit -m "feat(company): the model, scoped to an organization

The same tax id may appear in two organizations. That is the accountant
holding a client's CNPJ while the client's staff also issues, and it is
unrepresentable under a global unique key."
```

---

### Task 3: The table

**Files:**
- Modify: `cdk/lib/dynamodb-stack.ts` (after the invitations table, ~line 355)

**Interfaces:**
- Produces: table `{environment}_account_companies`, registered as `this.tables.set('account_companies', companiesTable)` so `iam-stack.ts` picks it up from `[...dynamoDBTables.values()]` with no change there.

- [ ] **Step 1: Add the table**

Insert after the `this.tables.set('account_invitations', invitationsTable);` line:

```ts
    // Three row kinds under pk=ORG#{organization_id}:
    //   sk=COMPANY#{company_id}          the company
    //   sk=TAXID#{digits}                the uniqueness lock, written in the
    //                                    same transaction as the company so
    //                                    two concurrent registrations of one
    //                                    CNPJ cannot both win
    //   sk=ACTOR#{company_id}#{user_id}  who may act for it
    //
    // lookup-index answers two questions with one GSI, exactly as the
    // organizations tables do: lookup_pk=USER#{id} for "which companies may
    // this person act for", lookup_pk=SOURCE#{system}#{ref} for "have I
    // already imported this one". Sparse — a company row carries lookup_pk
    // only when it was imported.
    const companiesTable = new dynamodb.TableV2(this, 'CompaniesTableV2', {
      tableName: `${environment}_account_companies`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      sortKey: {name: 'sk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand({
        maxReadRequestUnits: 1000,
        maxWriteRequestUnits: 1000,
      }),
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
      globalSecondaryIndexes: [
        {
          indexName: 'lookup-index',
          partitionKey: {name: 'lookup_pk', type: dynamodb.AttributeType.STRING},
          projectionType: dynamodb.ProjectionType.ALL,
          warmThroughput: undefined,
          maxReadRequestUnits: 1000,
          maxWriteRequestUnits: 1000,
        },
      ],
    });
    this.tables.set('account_companies', companiesTable);
```

- [ ] **Step 2: Verify it synthesizes**

Run: `cd cdk && npm run build && npx cdk synth --quiet 2>&1 | tail -5`
Expected: no error. If `cdk synth` needs an environment argument in this repo, use the same invocation the organizations tables were added with.

- [ ] **Step 3: Commit**

```bash
git add cdk/lib/dynamodb-stack.ts
git commit -m "feat(cdk): the account_companies table

One table, three row kinds. The tax-id lock shares the partition with the
company so uniqueness within an organization is a conditional write in one
transaction, not a read followed by a hopeful put."
```

---

### Task 4: The repository

**Files:**
- Create: `api/internal/domain/company/repository.go`
- Test: `api/internal/domain/company/repository_test.go`

**Interfaces:**
- Consumes: `Company`, `Actor` from Task 2; `database.Base`, `database.NewBase`, `database.TableName`, `database.ConditionalUpdate`, `database.IsConditionFailed`.
- Produces:

```go
type Repository interface {
	Create(ctx context.Context, c *Company, firstActor *Actor) error
	Get(ctx context.Context, orgID, companyID string) (*Company, error)
	List(ctx context.Context, orgID string) ([]*Company, error)
	GetBySourceRef(ctx context.Context, system, ref string) (*Company, error)
	UpdateNames(ctx context.Context, orgID, companyID, legal, trade string, now time.Time) error
	PutActor(ctx context.Context, a *Actor) error
	GetActor(ctx context.Context, orgID, companyID, userID string) (*Actor, error)
	ListActors(ctx context.Context, orgID, companyID string) ([]*Actor, error)
	RemoveActor(ctx context.Context, orgID, companyID, userID string) error
}

func NewRepository(db *dynamodb.Client, tablePrefix string) Repository
```
plus `var ErrNotFound`, `var ErrTaxIDTaken`.

- [ ] **Step 1: Write the failing test**

The repository's key layout is what the test pins — the DynamoDB calls themselves are covered by the service tests through the fake. `repository_test.go`:

```go
package company

import "testing"

func TestKeysNamespaceTheThreeRowKinds(t *testing.T) {
	if got := orgPK("org_1"); got != "ORG#org_1" {
		t.Errorf("orgPK = %q", got)
	}
	if got := companySK("cmp_1"); got != "COMPANY#cmp_1" {
		t.Errorf("companySK = %q", got)
	}
	if got := taxIDSK("11222333000181"); got != "TAXID#11222333000181" {
		t.Errorf("taxIDSK = %q", got)
	}
	if got := actorSK("cmp_1", "usr_1"); got != "ACTOR#cmp_1#usr_1" {
		t.Errorf("actorSK = %q", got)
	}
}

// The prefixes must not be prefixes of one another, or a Query for companies
// would also return actors. COMPANY# and ACTOR# are safe; this test is what
// stops a future rename from making them unsafe.
func TestRowKindPrefixesDoNotOverlap(t *testing.T) {
	prefixes := []string{companySKPrefix, taxIDSKPrefix, actorSKPrefix}
	for i, a := range prefixes {
		for j, b := range prefixes {
			if i != j && (len(a) <= len(b) && b[:len(a)] == a) {
				t.Errorf("%q is a prefix of %q", a, b)
			}
		}
	}
}

func TestActorLookupIsNamespacedByUser(t *testing.T) {
	if got := lookupUserPK("usr_1"); got != "USER#usr_1" {
		t.Errorf("lookupUserPK = %q", got)
	}
	if got := lookupSourcePK("dfe", "CNPJ_1"); got != "SOURCE#dfe#CNPJ_1" {
		t.Errorf("lookupSourcePK = %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/domain/company/ -run TestKeys -v`
Expected: FAIL, `undefined: orgPK`.

- [ ] **Step 3: Write the implementation**

```go
package company

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/account/api/internal/database"
)

var (
	// ErrNotFound is a company or an actor edge that is not there.
	ErrNotFound = errors.New("not found")
	// ErrTaxIDTaken is the lock row losing its conditional write: this
	// organization already holds this tax id. Never a read-then-write check —
	// two people registering the same CNPJ at once must not both succeed.
	ErrTaxIDTaken = errors.New("this organization already has this tax id")
)

const (
	companiesTable = "account_companies"
	lookupIndex    = "lookup-index"

	orgPKPrefix     = "ORG#"
	companySKPrefix = "COMPANY#"
	taxIDSKPrefix   = "TAXID#"
	actorSKPrefix   = "ACTOR#"
)

func orgPK(orgID string) string        { return orgPKPrefix + orgID }
func companySK(companyID string) string { return companySKPrefix + companyID }
func taxIDSK(digits string) string      { return taxIDSKPrefix + digits }

// actorSK nests the company id inside the sort key so one Query with the
// prefix ACTOR#{company}# lists a company's actors, and the table needs no
// second index for it.
func actorSK(companyID, userID string) string {
	return actorSKPrefix + companyID + "#" + userID
}

func lookupUserPK(userID string) string        { return "USER#" + userID }
func lookupSourcePK(system, ref string) string { return "SOURCE#" + system + "#" + ref }

func companyIDFromSK(sk string) string { return strings.TrimPrefix(sk, companySKPrefix) }

func actorIDsFromSK(sk string) (companyID, userID string) {
	rest := strings.TrimPrefix(sk, actorSKPrefix)
	companyID, userID, _ = strings.Cut(rest, "#")
	return companyID, userID
}

type Repository interface {
	Create(ctx context.Context, c *Company, firstActor *Actor) error
	Get(ctx context.Context, orgID, companyID string) (*Company, error)
	List(ctx context.Context, orgID string) ([]*Company, error)
	GetBySourceRef(ctx context.Context, system, ref string) (*Company, error)
	UpdateNames(ctx context.Context, orgID, companyID, legal, trade string, now time.Time) error
	PutActor(ctx context.Context, a *Actor) error
	GetActor(ctx context.Context, orgID, companyID, userID string) (*Actor, error)
	ListActors(ctx context.Context, orgID, companyID string) ([]*Actor, error)
	RemoveActor(ctx context.Context, orgID, companyID, userID string) error
}

type repo struct {
	db        *dynamodb.Client
	companies database.Base
	tableName string
}

func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &repo{
		db:        db,
		companies: database.NewBase(db, tablePrefix, companiesTable),
		tableName: database.TableName(tablePrefix, companiesTable),
	}
}

// Create writes the company, its tax-id lock and its first actor in one
// transaction.
//
// The lock is what makes "one tax id per organization" a database invariant
// rather than a hope: a read-then-write would let two concurrent registrations
// of the same CNPJ both find nothing and both write.
func (r *repo) Create(ctx context.Context, c *Company, firstActor *Actor) error {
	item, err := attributevalue.MarshalMap(c)
	if err != nil {
		return fmt.Errorf("marshaling company: %w", err)
	}
	item["pk"] = &types.AttributeValueMemberS{Value: orgPK(c.OrganizationID)}
	item["sk"] = &types.AttributeValueMemberS{Value: companySK(c.ID)}
	// Sparse: only an imported company is findable by source ref.
	if c.SourceSystem != "" && c.SourceRef != "" {
		item["lookup_pk"] = &types.AttributeValueMemberS{Value: lookupSourcePK(c.SourceSystem, c.SourceRef)}
	}

	lock := map[string]types.AttributeValue{
		"pk":         &types.AttributeValueMemberS{Value: orgPK(c.OrganizationID)},
		"sk":         &types.AttributeValueMemberS{Value: taxIDSK(c.TaxID)},
		"company_id": &types.AttributeValueMemberS{Value: c.ID},
	}

	writes := []types.TransactWriteItem{
		r.companies.BuildPutTxItem(item),
		r.companies.BuildPutTxItemIfAbsent(lock),
	}
	if firstActor != nil {
		actorItem, err := r.actorItem(firstActor)
		if err != nil {
			return err
		}
		writes = append(writes, r.companies.BuildPutTxItemIfAbsent(actorItem))
	}

	if err := r.companies.TransactWrite(ctx, writes); err != nil {
		if database.IsConditionFailed(err) {
			return ErrTaxIDTaken
		}
		return fmt.Errorf("creating company: %w", err)
	}
	return nil
}

func (r *repo) actorItem(a *Actor) (map[string]types.AttributeValue, error) {
	item, err := attributevalue.MarshalMap(a)
	if err != nil {
		return nil, fmt.Errorf("marshaling actor: %w", err)
	}
	item["pk"] = &types.AttributeValueMemberS{Value: orgPK(a.OrganizationID)}
	item["sk"] = &types.AttributeValueMemberS{Value: actorSK(a.CompanyID, a.UserID)}
	item["lookup_pk"] = &types.AttributeValueMemberS{Value: lookupUserPK(a.UserID)}
	return item, nil
}

func (r *repo) Get(ctx context.Context, orgID, companyID string) (*Company, error) {
	item, err := r.companies.GetItem(ctx, orgPK(orgID), companySK(companyID))
	if err != nil {
		return nil, fmt.Errorf("reading company: %w", err)
	}
	if item == nil {
		return nil, ErrNotFound
	}
	return unmarshalCompany(item)
}

func unmarshalCompany(item map[string]types.AttributeValue) (*Company, error) {
	var c Company
	if err := attributevalue.UnmarshalMap(item, &c); err != nil {
		return nil, fmt.Errorf("unmarshaling company: %w", err)
	}
	// The ids live in the key, so they are recovered here or every company
	// comes back not knowing who or where it is.
	if pk, ok := item["pk"].(*types.AttributeValueMemberS); ok {
		c.OrganizationID = strings.TrimPrefix(pk.Value, orgPKPrefix)
	}
	if sk, ok := item["sk"].(*types.AttributeValueMemberS); ok {
		c.ID = companyIDFromSK(sk.Value)
	}
	return &c, nil
}

func (r *repo) List(ctx context.Context, orgID string) ([]*Company, error) {
	res, err := r.companies.Query(ctx, database.QueryOpts{
		PK: orgPK(orgID), SKPrefix: companySKPrefix, Limit: 500,
	})
	if err != nil {
		return nil, fmt.Errorf("listing companies: %w", err)
	}
	out := make([]*Company, 0, len(res.Items))
	for _, item := range res.Items {
		c, err := unmarshalCompany(item)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// GetBySourceRef answers "have I already imported this one". It reads a GSI, so
// it is eventually consistent — acceptable only because Create's lock row is
// conditional, making the worst case a rejected duplicate rather than two rows.
func (r *repo) GetBySourceRef(ctx context.Context, system, ref string) (*Company, error) {
	res, err := r.companies.QueryGSI(ctx, lookupIndex, "lookup_pk", lookupSourcePK(system, ref), 1, nil)
	if err != nil {
		return nil, fmt.Errorf("reading company by source ref: %w", err)
	}
	if len(res.Items) == 0 {
		return nil, ErrNotFound
	}
	return unmarshalCompany(res.Items[0])
}

func (r *repo) UpdateNames(ctx context.Context, orgID, companyID, legal, trade string, now time.Time) error {
	ok, err := database.ConditionalUpdate(ctx, r.db, r.tableName,
		orgPK(orgID), aws.String(companySK(companyID)),
		map[string]any{"legal_name": legal, "trade_name": trade, "updated_at": now.UTC()},
		"attribute_exists(pk)", nil, nil)
	if err != nil {
		return fmt.Errorf("renaming company: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func (r *repo) PutActor(ctx context.Context, a *Actor) error {
	item, err := r.actorItem(a)
	if err != nil {
		return err
	}
	if err := r.companies.PutItem(ctx, item); err != nil {
		return fmt.Errorf("granting actor: %w", err)
	}
	return nil
}

func (r *repo) GetActor(ctx context.Context, orgID, companyID, userID string) (*Actor, error) {
	item, err := r.companies.GetItem(ctx, orgPK(orgID), actorSK(companyID, userID))
	if err != nil {
		return nil, fmt.Errorf("reading actor: %w", err)
	}
	if item == nil {
		return nil, ErrNotFound
	}
	return unmarshalActor(item)
}

func unmarshalActor(item map[string]types.AttributeValue) (*Actor, error) {
	var a Actor
	if err := attributevalue.UnmarshalMap(item, &a); err != nil {
		return nil, fmt.Errorf("unmarshaling actor: %w", err)
	}
	if pk, ok := item["pk"].(*types.AttributeValueMemberS); ok {
		a.OrganizationID = strings.TrimPrefix(pk.Value, orgPKPrefix)
	}
	if sk, ok := item["sk"].(*types.AttributeValueMemberS); ok {
		a.CompanyID, a.UserID = actorIDsFromSK(sk.Value)
	}
	return &a, nil
}

func (r *repo) ListActors(ctx context.Context, orgID, companyID string) ([]*Actor, error) {
	res, err := r.companies.Query(ctx, database.QueryOpts{
		PK: orgPK(orgID), SKPrefix: actorSKPrefix + companyID + "#", Limit: 500,
	})
	if err != nil {
		return nil, fmt.Errorf("listing actors: %w", err)
	}
	out := make([]*Actor, 0, len(res.Items))
	for _, item := range res.Items {
		a, err := unmarshalActor(item)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (r *repo) RemoveActor(ctx context.Context, orgID, companyID, userID string) error {
	ok, err := r.companies.DeleteItem(ctx, orgPK(orgID), actorSK(companyID, userID))
	if err != nil {
		return fmt.Errorf("revoking actor: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: Run the tests**

Run: `cd api && go test ./internal/domain/company/ -v && go vet ./internal/domain/company/`
Expected: PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/company/repository.go api/internal/domain/company/repository_test.go
git commit -m "feat(company): repository, with the tax id as a lock row

Uniqueness within an organization is a conditional write in the same
transaction as the company, not a read followed by a hopeful put: two
people registering one CNPJ at once must not both find nothing."
```

---

### Task 5: The service

**Files:**
- Create: `api/internal/domain/company/service.go`
- Test: `api/internal/domain/company/service_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1, 2, 4; `organization.Service` for the caller's role (via a narrow interface, so this package does not import the whole organization service and create a cycle risk).
- Produces:

```go
type Service struct{ /* unexported */ }
func NewService(repo Repository, now func() time.Time) *Service
func (s *Service) Register(ctx context.Context, orgID, actorUserID, actorName, rawTaxID, legalName, tradeName string) (*Company, error)
func (s *Service) List(ctx context.Context, orgID string) ([]*Company, error)
func (s *Service) Get(ctx context.Context, orgID, companyID string) (*Company, error)
func (s *Service) Rename(ctx context.Context, orgID, companyID, legalName, tradeName string) error
func (s *Service) GrantActor(ctx context.Context, orgID, companyID, targetUserID, targetName, grantedBy string) error
func (s *Service) RevokeActor(ctx context.Context, orgID, companyID, targetUserID string) error
func (s *Service) ListActors(ctx context.Context, orgID, companyID string) ([]*Actor, error)
func (s *Service) MayAct(ctx context.Context, orgID, companyID, userID string) (bool, error)
```
plus `var ErrInvalidTaxID`, `var ErrInvalidName`.

Authorization by organization role is **not** repeated here: `middleware.RequireOrgRole` already resolves and enforces it per request, and a second check reading the same row is a second thing to keep in agreement. The service enforces only what the middleware cannot see — the tax id, the names, and the actor edge.

- [ ] **Step 1: Write the failing test**

```go
package company

import (
	"context"
	"testing"
	"time"
)

func fixedClock() time.Time { return time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC) }

// fakeRepo reproduces the conditions the real repository enforces in DynamoDB —
// notably the tax-id lock — because a fake that accepts what the database
// refuses makes these tests agree with nothing that runs in production.
type fakeRepo struct {
	companies map[string]map[string]*Company // orgID -> companyID
	taxIDs    map[string]map[string]string   // orgID -> taxID -> companyID
	actors    map[string]map[string]*Actor   // orgID+companyID -> userID
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		companies: map[string]map[string]*Company{},
		taxIDs:    map[string]map[string]string{},
		actors:    map[string]map[string]*Actor{},
	}
}

func actorKey(orgID, companyID string) string { return orgID + "|" + companyID }

func (f *fakeRepo) Create(_ context.Context, c *Company, first *Actor) error {
	if f.taxIDs[c.OrganizationID] == nil {
		f.taxIDs[c.OrganizationID] = map[string]string{}
	}
	if _, taken := f.taxIDs[c.OrganizationID][c.TaxID]; taken {
		return ErrTaxIDTaken
	}
	f.taxIDs[c.OrganizationID][c.TaxID] = c.ID
	if f.companies[c.OrganizationID] == nil {
		f.companies[c.OrganizationID] = map[string]*Company{}
	}
	copied := *c
	f.companies[c.OrganizationID][c.ID] = &copied
	if first != nil {
		return f.PutActor(context.Background(), first)
	}
	return nil
}

func (f *fakeRepo) Get(_ context.Context, orgID, companyID string) (*Company, error) {
	c, ok := f.companies[orgID][companyID]
	if !ok {
		return nil, ErrNotFound
	}
	copied := *c
	return &copied, nil
}

func (f *fakeRepo) List(_ context.Context, orgID string) ([]*Company, error) {
	out := make([]*Company, 0, len(f.companies[orgID]))
	for _, c := range f.companies[orgID] {
		copied := *c
		out = append(out, &copied)
	}
	return out, nil
}

func (f *fakeRepo) GetBySourceRef(_ context.Context, system, ref string) (*Company, error) {
	for _, byID := range f.companies {
		for _, c := range byID {
			if c.SourceSystem == system && c.SourceRef == ref && ref != "" {
				copied := *c
				return &copied, nil
			}
		}
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) UpdateNames(_ context.Context, orgID, companyID, legal, trade string, now time.Time) error {
	c, ok := f.companies[orgID][companyID]
	if !ok {
		return ErrNotFound
	}
	c.LegalName, c.TradeName, c.UpdatedAt = legal, trade, now
	return nil
}

func (f *fakeRepo) PutActor(_ context.Context, a *Actor) error {
	k := actorKey(a.OrganizationID, a.CompanyID)
	if f.actors[k] == nil {
		f.actors[k] = map[string]*Actor{}
	}
	copied := *a
	f.actors[k][a.UserID] = &copied
	return nil
}

func (f *fakeRepo) GetActor(_ context.Context, orgID, companyID, userID string) (*Actor, error) {
	a, ok := f.actors[actorKey(orgID, companyID)][userID]
	if !ok {
		return nil, ErrNotFound
	}
	copied := *a
	return &copied, nil
}

func (f *fakeRepo) ListActors(_ context.Context, orgID, companyID string) ([]*Actor, error) {
	byUser := f.actors[actorKey(orgID, companyID)]
	out := make([]*Actor, 0, len(byUser))
	for _, a := range byUser {
		copied := *a
		out = append(out, &copied)
	}
	return out, nil
}

func (f *fakeRepo) RemoveActor(_ context.Context, orgID, companyID, userID string) error {
	k := actorKey(orgID, companyID)
	if _, ok := f.actors[k][userID]; !ok {
		return ErrNotFound
	}
	delete(f.actors[k], userID)
	return nil
}

func newService() (*Service, *fakeRepo) {
	repo := newFakeRepo()
	return NewService(repo, fixedClock), repo
}

func TestRegisterNormalizesTheTaxIDAndNamesTheKind(t *testing.T) {
	svc, _ := newService()
	c, err := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11.222.333/0001-81", " Acme LTDA ", "Acme")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if c.TaxID != "11222333000181" || c.TaxIDKind != KindCNPJ {
		t.Errorf("tax id = %q %q", c.TaxID, c.TaxIDKind)
	}
	if c.LegalName != "Acme LTDA" {
		t.Errorf("legal name = %q", c.LegalName)
	}
	if c.ID == "" {
		t.Error("company id is empty")
	}
}

// The person who registers a company can act for it immediately. Anything else
// means registering one and then being unable to use it.
func TestRegisteringGrantsTheRegistrantTheActorEdge(t *testing.T) {
	svc, _ := newService()
	c, _ := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000181", "Acme LTDA", "")
	ok, err := svc.MayAct(context.Background(), "org_1", c.ID, "usr_1")
	if err != nil || !ok {
		t.Fatalf("MayAct = %v %v, want true", ok, err)
	}
}

// The load-bearing case: an accountant and their client each hold the same
// CNPJ. Pinned so nobody "fixes" the scoping into a global unique key.
func TestTheSameTaxIDInTwoOrganizationsIsAllowed(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Register(context.Background(), "org_client", "usr_1", "Ana", "11222333000181", "Acme LTDA", ""); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := svc.Register(context.Background(), "org_accountant", "usr_2", "Bruno", "11222333000181", "Acme LTDA", ""); err != nil {
		t.Fatalf("second organization refused the same tax id: %v", err)
	}
}

func TestTheSameTaxIDTwiceInOneOrganizationIsRefused(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000181", "Acme LTDA", ""); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Masked differently on purpose: normalization must happen before the
	// lock is consulted, or the same document registers twice.
	_, err := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11.222.333/0001-81", "Acme LTDA", "")
	if err != ErrTaxIDTaken {
		t.Fatalf("err = %v, want ErrTaxIDTaken", err)
	}
}

func TestRegisterRefusesABadTaxID(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000182", "Acme LTDA", ""); err != ErrInvalidTaxID {
		t.Fatalf("err = %v, want ErrInvalidTaxID", err)
	}
}

func TestRegisterRefusesAnEmptyLegalName(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000181", "  ", ""); err != ErrInvalidName {
		t.Fatalf("err = %v, want ErrInvalidName", err)
	}
}

// Membership is what gets you into the workspace; the edge is what lets you act
// for a company inside it. A member with no edge acts for nothing.
func TestBeingInTheOrganizationDoesNotGrantTheActorEdge(t *testing.T) {
	svc, _ := newService()
	c, _ := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000181", "Acme LTDA", "")
	ok, err := svc.MayAct(context.Background(), "org_1", c.ID, "usr_colleague")
	if err != nil {
		t.Fatalf("MayAct: %v", err)
	}
	if ok {
		t.Error("a colleague with no edge may act for the company")
	}
}

func TestGrantingAndRevokingTheEdge(t *testing.T) {
	svc, _ := newService()
	c, _ := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000181", "Acme LTDA", "")
	if err := svc.GrantActor(context.Background(), "org_1", c.ID, "usr_2", "Bruno", "usr_1"); err != nil {
		t.Fatalf("GrantActor: %v", err)
	}
	if ok, _ := svc.MayAct(context.Background(), "org_1", c.ID, "usr_2"); !ok {
		t.Fatal("grant did not take effect")
	}
	if err := svc.RevokeActor(context.Background(), "org_1", c.ID, "usr_2"); err != nil {
		t.Fatalf("RevokeActor: %v", err)
	}
	if ok, _ := svc.MayAct(context.Background(), "org_1", c.ID, "usr_2"); ok {
		t.Error("revoke did not take effect")
	}
}

// Granting an edge on a company that is not in this organization must not
// silently write a row nothing can reach.
func TestGrantingOnAnUnknownCompanyIsRefused(t *testing.T) {
	svc, _ := newService()
	if err := svc.GrantActor(context.Background(), "org_1", "cmp_nope", "usr_2", "Bruno", "usr_1"); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/domain/company/ -run TestRegister -v`
Expected: FAIL, `undefined: NewService`.

- [ ] **Step 3: Write the implementation**

```go
package company

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"uuid"
)

var (
	// ErrInvalidTaxID is a tax id that is not a well-formed CNPJ or CPF. It
	// says nothing about whether the document exists — that is not knowable
	// here, and pretending otherwise would reject a CNPJ issued this morning.
	ErrInvalidTaxID = errors.New("tax id is not a valid CNPJ or CPF")
	// ErrInvalidName is an empty or overlong legal name.
	ErrInvalidName = errors.New("legal name is required")
)

// Service holds the rules a conditional write cannot express on its own.
//
// It does not re-check the caller's organization role: RequireOrgRole already
// resolved it for this request, and a second read of the same row is a second
// thing to keep in agreement with the first.
type Service struct {
	repo Repository
	now  func() time.Time
}

func NewService(repo Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, now: now}
}

// Register adds a company to an organization and makes the registrant its first
// actor.
//
// The edge is granted in the same transaction because a company nobody may act
// for is a company that had to be fixed by a second request — and if that
// request fails, by a support ticket.
func (s *Service) Register(ctx context.Context, orgID, actorUserID, actorName, rawTaxID, legalName, tradeName string) (*Company, error) {
	digits, kind, ok := NormalizeTaxID(rawTaxID)
	if !ok {
		return nil, ErrInvalidTaxID
	}
	legal, trade, ok := ValidateNames(legalName, tradeName)
	if !ok {
		return nil, ErrInvalidName
	}
	now := s.now().UTC()
	c := &Company{
		OrganizationID: orgID,
		ID:             uuid.NewV7().String(),
		TaxID:          digits,
		TaxIDKind:      kind,
		LegalName:      legal,
		TradeName:      trade,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	first := &Actor{
		OrganizationID: orgID,
		CompanyID:      c.ID,
		UserID:         actorUserID,
		Name:           strings.TrimSpace(actorName),
		GrantedBy:      actorUserID,
		CreatedAt:      now,
	}
	if err := s.repo.Create(ctx, c, first); err != nil {
		return nil, err
	}
	return c, nil
}

// List returns an organization's companies, ordered by the name people read.
func (s *Service) List(ctx context.Context, orgID string) ([]*Company, error) {
	companies, err := s.repo.List(ctx, orgID)
	if err != nil {
		return nil, err
	}
	sort.Slice(companies, func(i, j int) bool {
		return strings.ToLower(companies[i].LegalName) < strings.ToLower(companies[j].LegalName)
	})
	return companies, nil
}

func (s *Service) Get(ctx context.Context, orgID, companyID string) (*Company, error) {
	return s.repo.Get(ctx, orgID, companyID)
}

// Rename corrects the names. The tax id is not renameable: correcting it would
// mean releasing one lock row and taking another, and a company whose tax id
// was wrong is a different company — register it and stop using the first.
func (s *Service) Rename(ctx context.Context, orgID, companyID, legalName, tradeName string) error {
	legal, trade, ok := ValidateNames(legalName, tradeName)
	if !ok {
		return ErrInvalidName
	}
	return s.repo.UpdateNames(ctx, orgID, companyID, legal, trade, s.now().UTC())
}

// GrantActor lets somebody act for a company.
//
// The company is read first so a grant on an id from another organization —
// or on nothing at all — fails instead of writing a row nothing can reach.
func (s *Service) GrantActor(ctx context.Context, orgID, companyID, targetUserID, targetName, grantedBy string) error {
	if _, err := s.repo.Get(ctx, orgID, companyID); err != nil {
		return err
	}
	return s.repo.PutActor(ctx, &Actor{
		OrganizationID: orgID,
		CompanyID:      companyID,
		UserID:         targetUserID,
		Name:           strings.TrimSpace(targetName),
		GrantedBy:      grantedBy,
		CreatedAt:      s.now().UTC(),
	})
}

func (s *Service) RevokeActor(ctx context.Context, orgID, companyID, targetUserID string) error {
	return s.repo.RemoveActor(ctx, orgID, companyID, targetUserID)
}

func (s *Service) ListActors(ctx context.Context, orgID, companyID string) ([]*Actor, error) {
	actors, err := s.repo.ListActors(ctx, orgID, companyID)
	if err != nil {
		return nil, err
	}
	sort.Slice(actors, func(i, j int) bool {
		return strings.ToLower(actors[i].Name) < strings.ToLower(actors[j].Name)
	})
	return actors, nil
}

// MayAct is the question ctech-dfe asks before it lets somebody issue.
//
// A missing edge is false with no error: "not permitted" is the answer, not a
// failure, and a caller that had to distinguish them would treat one as the
// other on the first refactor.
func (s *Service) MayAct(ctx context.Context, orgID, companyID, userID string) (bool, error) {
	_, err := s.repo.GetActor(ctx, orgID, companyID, userID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `cd api && go test ./internal/domain/company/ -v && go vet ./internal/domain/company/`
Expected: PASS, all tests.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/company/service.go api/internal/domain/company/service_test.go
git commit -m "feat(company): the rules, and the actor edge

Registering grants the registrant the edge in the same write: a company
nobody may act for needs a second request to fix, and a support ticket
when that request fails. Organization membership grants nothing here."
```

---

### Task 6: Routes

**Files:**
- Create: `api/internal/handler/company.go`
- Modify: `api/cmd/api/main.go` (near `organizationH` construction, ~line 253–260, and its `Register` call)
- Test: `api/internal/handler/company_test.go`

**Interfaces:**
- Consumes: `company.Service` from Task 5; `middleware.RequireOrgRole`, `middleware.GetOrgID`, `middleware.GetUserID`; `apierror`.
- Produces: routes mounted on the existing `orgs` group, all already behind `RequireAuth` + `RequireClientID(SelfClientID)`:

| Method | Path | Floor |
|---|---|---|
| `GET` | `/organizations/:id/companies` | viewer |
| `POST` | `/organizations/:id/companies` | admin |
| `GET` | `/organizations/:id/companies/:company_id` | viewer |
| `PATCH` | `/organizations/:id/companies/:company_id` | admin |
| `GET` | `/organizations/:id/companies/:company_id/actors` | viewer |
| `PUT` | `/organizations/:id/companies/:company_id/actors/:user_id` | admin |
| `DELETE` | `/organizations/:id/companies/:company_id/actors/:user_id` | admin |

- [ ] **Step 1: Write the failing test**

```go
package handler

import (
	"net/http"
	"testing"
)

// The refusal must be the organization one, not a company-shaped 404: telling
// a stranger that a company id is real is the same disclosure the
// organization routes already refuse to make.
func TestANonMemberIsRefusedTheCompanyList(t *testing.T) {
	env := newCompanyTestEnv(t)
	resp := env.do(t, http.MethodGet, "/v1.0/organizations/org_other/companies", nil, env.strangerToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if got := problemOf(t, resp).Detail; got != "You do not have access to this organization." {
		t.Errorf("detail = %q", got)
	}
}

func TestAViewerMayListButNotRegister(t *testing.T) {
	env := newCompanyTestEnv(t)
	if resp := env.do(t, http.MethodGet, "/v1.0/organizations/org_1/companies", nil, env.viewerToken); resp.StatusCode != http.StatusOK {
		t.Errorf("list status = %d, want 200", resp.StatusCode)
	}
	body := `{"tax_id":"11222333000181","legal_name":"Acme LTDA"}`
	if resp := env.do(t, http.MethodPost, "/v1.0/organizations/org_1/companies", []byte(body), env.viewerToken); resp.StatusCode != http.StatusForbidden {
		t.Errorf("register status = %d, want 403", resp.StatusCode)
	}
}

func TestRegisteringACompanyReturnsItNormalized(t *testing.T) {
	env := newCompanyTestEnv(t)
	body := `{"tax_id":"11.222.333/0001-81","legal_name":"Acme LTDA","trade_name":"Acme"}`
	resp := env.do(t, http.MethodPost, "/v1.0/organizations/org_1/companies", []byte(body), env.adminToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var got companyDTO
	decodeJSON(t, resp, &got)
	if got.TaxID != "11222333000181" || got.TaxIDKind != "cnpj" {
		t.Errorf("tax id = %q %q", got.TaxID, got.TaxIDKind)
	}
}

func TestABadTaxIDIsAValidationProblemNotAServerError(t *testing.T) {
	env := newCompanyTestEnv(t)
	body := `{"tax_id":"11222333000182","legal_name":"Acme LTDA"}`
	resp := env.do(t, http.MethodPost, "/v1.0/organizations/org_1/companies", []byte(body), env.adminToken)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

// The second registration of one tax id is a conflict, not a validation
// failure: the request was well formed and the state refused it.
func TestTheSameTaxIDTwiceIsAConflict(t *testing.T) {
	env := newCompanyTestEnv(t)
	body := `{"tax_id":"11222333000181","legal_name":"Acme LTDA"}`
	if resp := env.do(t, http.MethodPost, "/v1.0/organizations/org_1/companies", []byte(body), env.adminToken); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first registration failed: %d", resp.StatusCode)
	}
	resp := env.do(t, http.MethodPost, "/v1.0/organizations/org_1/companies", []byte(body), env.adminToken)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}
```

`newCompanyTestEnv`, `env.do`, `decodeJSON` and `problemOf` follow the shape the existing organization handler tests use; reuse those helpers rather than writing new ones, and note that `problemOf` reads the body exactly once (two helpers each reading `resp.Body` is a bug this suite has already hit twice).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/handler/ -run TestRegisteringACompany -v`
Expected: FAIL — `undefined: newCompanyTestEnv`, `undefined: companyDTO`.

- [ ] **Step 3: Write the handler**

```go
package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/company"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/middleware"
)

// CompanyHandler exposes the companies inside an organization.
//
// It holds the organization service only to build the RequireOrgRole guard —
// the authorization is that middleware's, not this handler's.
type CompanyHandler struct {
	svc   *company.Service
	orgs  *organization.Service
	users *user.Service
}

func NewCompanyHandler(svc *company.Service, orgs *organization.Service, users *user.Service) *CompanyHandler {
	return &CompanyHandler{svc: svc, orgs: orgs, users: users}
}

func (h *CompanyHandler) Register(orgs fiber.Router) {
	scoped := func(floor string) fiber.Handler { return middleware.RequireOrgRole(h.orgs, floor) }
	orgs.Get("/:id/companies", scoped(organization.RoleViewer), h.list)
	orgs.Post("/:id/companies", scoped(organization.RoleAdmin), h.register)
	orgs.Get("/:id/companies/:company_id", scoped(organization.RoleViewer), h.get)
	orgs.Patch("/:id/companies/:company_id", scoped(organization.RoleAdmin), h.rename)
	orgs.Get("/:id/companies/:company_id/actors", scoped(organization.RoleViewer), h.listActors)
	orgs.Put("/:id/companies/:company_id/actors/:user_id", scoped(organization.RoleAdmin), h.grantActor)
	orgs.Delete("/:id/companies/:company_id/actors/:user_id", scoped(organization.RoleAdmin), h.revokeActor)
}

type registerCompanyRequest struct {
	TaxID     string `json:"tax_id" validate:"required,max=20"`
	LegalName string `json:"legal_name" validate:"required,max=200"`
	TradeName string `json:"trade_name" validate:"max=200"`
}

type renameCompanyRequest struct {
	LegalName string `json:"legal_name" validate:"required,max=200"`
	TradeName string `json:"trade_name" validate:"max=200"`
}

type companyDTO struct {
	ID        string `json:"id"`
	TaxID     string `json:"tax_id"`
	TaxIDKind string `json:"tax_id_kind"`
	LegalName string `json:"legal_name"`
	TradeName string `json:"trade_name,omitempty"`
	CreatedAt string `json:"created_at"`
}

type actorDTO struct {
	UserID    string `json:"user_id"`
	Name      string `json:"name,omitempty"`
	GrantedBy string `json:"granted_by,omitempty"`
	CreatedAt string `json:"created_at"`
}

func toCompanyDTO(c *company.Company) companyDTO {
	return companyDTO{
		ID: c.ID, TaxID: c.TaxID, TaxIDKind: c.TaxIDKind,
		LegalName: c.LegalName, TradeName: c.TradeName,
		CreatedAt: c.CreatedAt.Format(time.RFC3339),
	}
}

func (h *CompanyHandler) list(c fiber.Ctx) error {
	companies, err := h.svc.List(c.Context(), middleware.GetOrgID(c))
	if err != nil {
		return companyProblem(c, err)
	}
	out := make([]companyDTO, 0, len(companies))
	for _, item := range companies {
		out = append(out, toCompanyDTO(item))
	}
	return c.JSON(fiber.Map{"companies": out})
}

func (h *CompanyHandler) register(c fiber.Ctx) error {
	var req registerCompanyRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	created, err := h.svc.Register(c.Context(), middleware.GetOrgID(c), middleware.GetUserID(c),
		h.callerName(c), req.TaxID, req.LegalName, req.TradeName)
	if err != nil {
		return companyProblem(c, err)
	}
	return c.Status(http.StatusCreated).JSON(toCompanyDTO(created))
}

func (h *CompanyHandler) get(c fiber.Ctx) error {
	found, err := h.svc.Get(c.Context(), middleware.GetOrgID(c), c.Params("company_id"))
	if err != nil {
		return companyProblem(c, err)
	}
	return c.JSON(toCompanyDTO(found))
}

func (h *CompanyHandler) rename(c fiber.Ctx) error {
	var req renameCompanyRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	err := h.svc.Rename(c.Context(), middleware.GetOrgID(c), c.Params("company_id"), req.LegalName, req.TradeName)
	if err != nil {
		return companyProblem(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *CompanyHandler) listActors(c fiber.Ctx) error {
	actors, err := h.svc.ListActors(c.Context(), middleware.GetOrgID(c), c.Params("company_id"))
	if err != nil {
		return companyProblem(c, err)
	}
	out := make([]actorDTO, 0, len(actors))
	for _, a := range actors {
		out = append(out, actorDTO{
			UserID: a.UserID, Name: a.Name, GrantedBy: a.GrantedBy,
			CreatedAt: a.CreatedAt.Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"actors": out})
}

// grantActor is idempotent by design — PUT, not POST. Granting an edge
// somebody already has is not an error worth an alert.
func (h *CompanyHandler) grantActor(c fiber.Ctx) error {
	targetUserID := c.Params("user_id")
	err := h.svc.GrantActor(c.Context(), middleware.GetOrgID(c), c.Params("company_id"),
		targetUserID, h.nameOf(c, targetUserID), middleware.GetUserID(c))
	if err != nil {
		return companyProblem(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *CompanyHandler) revokeActor(c fiber.Ctx) error {
	err := h.svc.RevokeActor(c.Context(), middleware.GetOrgID(c), c.Params("company_id"), c.Params("user_id"))
	if err != nil {
		return companyProblem(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *CompanyHandler) callerName(c fiber.Ctx) string {
	return h.nameOf(c, middleware.GetUserID(c))
}

// nameOf resolves a display name for the row about to be written. A failure is
// not worth refusing the write over: a row with no name renders as the user id.
func (h *CompanyHandler) nameOf(c fiber.Ctx, userID string) string {
	if h.users == nil {
		return ""
	}
	u, err := h.users.GetByID(c.Context(), userID)
	if err != nil {
		return ""
	}
	return u.DisplayOrFullName()
}

func companyProblem(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, company.ErrInvalidTaxID):
		return apierror.ValidationFailed("That is not a valid CNPJ or CPF.", c.Path()).Send(c)
	case errors.Is(err, company.ErrInvalidName):
		return apierror.ValidationFailed("The legal name is required and must be at most 200 characters.", c.Path()).Send(c)
	case errors.Is(err, company.ErrTaxIDTaken):
		// A conflict, not a validation failure: the request was well formed
		// and the state refused it.
		return apierror.Conflict("This organization already has a company with that CNPJ or CPF.", c.Path()).Send(c)
	case errors.Is(err, company.ErrNotFound):
		// The same answer the organization routes give, for the same reason:
		// confirming a company id is real is a disclosure.
		return apierror.Forbidden("You do not have access to this organization.", c.Path()).Send(c)
	default:
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
}
```

- [ ] **Step 4: Wire it in `main.go`**

Next to the existing organization wiring (`companyRepo` beside `orgRepo`, `companySvc` beside `orgSvc`, `companyH` beside `organizationH`):

```go
	companyRepo := companyDomain.NewRepository(db, cfg.TablePrefix)
	companySvc := companyDomain.NewService(companyRepo, nil)
	companyH := handler.NewCompanyHandler(companySvc, orgSvc, userSvc)
```

and beside `organizationH.Register(orgs, invitations)`:

```go
	companyH.Register(orgs)
```

with the import `companyDomain "gopkg.aoctech.app/account/api/internal/domain/company"`.

- [ ] **Step 5: Run the tests**

Run: `cd api && go build ./... && go test ./internal/handler/ ./internal/domain/company/ -v 2>&1 | tail -30`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add api/internal/handler/company.go api/internal/handler/company_test.go api/cmd/api/main.go
git commit -m "feat(company): the routes, behind the organization role guard

Authorization is RequireOrgRole's, unchanged: a second check reading the
same membership row is a second thing to keep in agreement. A tax id
already held answers 409, not 422 — the request was well formed."
```

---

### Task 7: The registry lookup

**Files:**
- Create: `api/internal/domain/company/registry/cnpja.go`
- Test: `api/internal/domain/company/registry/cnpja_test.go`
- Modify: `api/internal/handler/company.go` (add `GET /organizations/:id/companies/lookup`)

**Interfaces:**
- Produces:

```go
package registry

type Names struct {
	LegalName string
	TradeName string
}

type Lookup interface {
	Names(ctx context.Context, cnpj string) (Names, bool)
}

func NewCNPJA(client *http.Client) Lookup
```
`Names` returns `(zero, false)` for anything other than a confident answer — an outage, a 404, a timeout, a malformed body. There is no error return, because every caller's response to every failure is the same: let the person type the names.

- [ ] **Step 1: Write the failing test**

```go
package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serving(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNamesReadsTheCompanyAndItsAlias(t *testing.T) {
	srv := serving(t, 200, `{"company":{"name":"ACME COMERCIO LTDA"},"alias":"Acme"}`)
	l := &cnpja{client: srv.Client(), endpoint: srv.URL + "/"}
	names, ok := l.Names(context.Background(), "11222333000181")
	if !ok || names.LegalName != "ACME COMERCIO LTDA" || names.TradeName != "Acme" {
		t.Fatalf("got %+v %v", names, ok)
	}
}

// Every failure is the same failure to the caller: the person types the names.
// Distinguishing them would only tempt a caller into treating one as fatal.
func TestNamesIsQuietOnEveryFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"not found", 404, `{"message":"not found"}`},
		{"rate limited", 429, ``},
		{"upstream error", 500, ``},
		{"malformed body", 200, `<html>`},
		{"empty name", 200, `{"company":{"name":""}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := serving(t, tc.status, tc.body)
			l := &cnpja{client: srv.Client(), endpoint: srv.URL + "/"}
			if names, ok := l.Names(context.Background(), "11222333000181"); ok {
				t.Errorf("got %+v true, want quiet", names)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/domain/company/registry/ -v`
Expected: FAIL, the package does not exist.

- [ ] **Step 3: Write the implementation**

```go
// Package registry looks a CNPJ up in a public register so a person does not
// retype the Receita Federal.
//
// It is a convenience and never a gate: every failure returns "no answer", and
// the caller lets the person type the names. A lookup that could block a
// registration would make a third party's uptime our own.
package registry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// cnpjaEndpoint is the open, unauthenticated tier. The full CNPJ is appended.
const cnpjaEndpoint = "https://open.cnpja.com/office/"

// maxBody caps what is read from a third party we do not control.
const maxBody = 64 << 10

type Names struct {
	LegalName string
	TradeName string
}

type Lookup interface {
	Names(ctx context.Context, cnpj string) (Names, bool)
}

type cnpja struct {
	client   *http.Client
	endpoint string
}

// NewCNPJA builds the lookup. The call is made from the API, never the browser:
// a static export calling a third party directly would hand that party the
// customer's IP and leave no audit trail.
func NewCNPJA(client *http.Client) Lookup {
	if client == nil {
		client = &http.Client{Timeout: 6 * time.Second}
	}
	return &cnpja{client: client, endpoint: cnpjaEndpoint}
}

type cnpjaResponse struct {
	Company struct {
		Name string `json:"name"`
	} `json:"company"`
	Alias string `json:"alias"`
}

func (l *cnpja) Names(ctx context.Context, cnpj string) (Names, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.endpoint+cnpj, nil)
	if err != nil {
		return Names{}, false
	}
	req.Header.Set("Accept", "application/json")
	resp, err := l.client.Do(req)
	if err != nil {
		return Names{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Names{}, false
	}
	var body cnpjaResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&body); err != nil {
		return Names{}, false
	}
	legal := strings.TrimSpace(body.Company.Name)
	if legal == "" {
		return Names{}, false
	}
	return Names{LegalName: legal, TradeName: strings.TrimSpace(body.Alias)}, true
}
```

- [ ] **Step 4: Add the route**

In `api/internal/handler/company.go`, add the field `lookup registry.Lookup` to `CompanyHandler`, accept it in `NewCompanyHandler`, register the route **before** the `:company_id` routes so `lookup` is not captured as an id:

```go
	// Before /:company_id — otherwise "lookup" is captured as a company id.
	orgs.Get("/:id/companies/lookup", scoped(organization.RoleAdmin), h.lookupTaxID)
```

```go
// lookupTaxID fills the names for a CNPJ. A miss is 200 with nothing found,
// never an error: the register not knowing a company says nothing about whether
// the company exists, and a 404 here would read as "invalid CNPJ".
func (h *CompanyHandler) lookupTaxID(c fiber.Ctx) error {
	digits, kind, ok := company.NormalizeTaxID(c.Query("tax_id"))
	if !ok {
		return apierror.ValidationFailed("That is not a valid CNPJ or CPF.", c.Path()).Send(c)
	}
	// No lookup for a CPF, deliberately: a person's name is not ours to fetch
	// from a public register.
	if kind != company.KindCNPJ || h.lookup == nil {
		return c.JSON(fiber.Map{"found": false})
	}
	names, found := h.lookup.Names(c.Context(), digits)
	if !found {
		return c.JSON(fiber.Map{"found": false})
	}
	return c.JSON(fiber.Map{
		"found": true, "legal_name": names.LegalName, "trade_name": names.TradeName,
	})
}
```

Wire it in `main.go`: `companyH := handler.NewCompanyHandler(companySvc, orgSvc, userSvc, registry.NewCNPJA(nil))`.

- [ ] **Step 5: Run the tests**

Run: `cd api && go build ./... && go test ./internal/domain/company/... ./internal/handler/ 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add api/internal/domain/company/registry api/internal/handler/company.go api/cmd/api/main.go
git commit -m "feat(company): fill the names from cnpja, never gate on it

Every failure is the same answer — no names, type them — because a
lookup that could block a registration makes a third party's uptime our
own. Called from the API: a static export calling cnpja directly would
hand it the customer's IP. No lookup for a CPF."
```

---

### Task 8: The screen

**Files:**
- Create: `ui/src/app/account/organizations/detail/companies-tab.tsx`
- Create: `ui/src/app/account/organizations/detail/companies-tab.test.tsx`
- Modify: `ui/src/lib/types.ts`, `ui/src/lib/queries.ts`, `ui/src/lib/mutations.ts`, `ui/src/lib/mock.ts`
- Modify: `ui/src/app/account/organizations/detail/page.tsx` (a fourth tab)
- Modify: `ui/src/locales/en.json`, `ui/src/locales/pt-BR.json`

**Interfaces:**
- Consumes: the routes from Tasks 6 and 7.
- Produces (types.ts):

```ts
export type TaxIDKind = 'cnpj' | 'cpf'

export interface Company {
  id: string
  tax_id: string
  tax_id_kind: TaxIDKind
  legal_name: string
  trade_name?: string
  created_at: string
}

export interface CompanyActor {
  user_id: string
  name?: string
  granted_by?: string
  created_at: string
}

/** Masks a stored digits-only tax id for display. */
export function formatTaxID(taxID: string, kind: TaxIDKind): string
```

- [ ] **Step 1: Write the failing test**

```tsx
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { CompaniesTab } from './companies-tab'
import { fetchCompanies } from '@/lib/queries'
import type { Organization, OrganizationRole } from '@/lib/types'

vi.mock('@/lib/queries', () => ({ fetchCompanies: vi.fn(), lookupTaxID: vi.fn() }))
vi.mock('@/lib/mutations', () => ({ registerCompanyAPI: vi.fn() }))

afterEach(cleanup)

function organization(role: OrganizationRole): Organization {
  return {
    id: 'org_1', display_name: 'CTech', owner_user_id: 'usr_owner',
    role, joined_at: new Date().toISOString(),
  }
}

function renderTab(role: OrganizationRole) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <CompaniesTab organization={organization(role)} />
    </QueryClientProvider>,
  )
}

describe('companies tab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchCompanies).mockResolvedValue([
      {
        id: 'cmp_1', tax_id: '11222333000181', tax_id_kind: 'cnpj',
        legal_name: 'Acme LTDA', trade_name: 'Acme',
        created_at: new Date().toISOString(),
      },
    ])
  })

  // The stored value is digits; a person reads a mask. Showing the raw digits
  // makes them count characters to check they typed the right company.
  it('shows the tax id masked', async () => {
    renderTab('admin')
    expect(await screen.findByText('11.222.333/0001-81')).toBeInTheDocument()
  })

  // The control is absent, not disabled: the server refuses a viewer's write,
  // and a dead control invites the attempt.
  it('gives a viewer no way to register a company', async () => {
    renderTab('viewer')
    await screen.findByText('Acme LTDA')
    expect(screen.queryByRole('button', { name: /add company/i })).toBeNull()
  })

  it('offers an admin the register control', async () => {
    renderTab('admin')
    await screen.findByText('Acme LTDA')
    expect(screen.getByRole('button', { name: /add company/i })).toBeInTheDocument()
  })
})
```

Note: `ResponsiveDataList` renders mobile cards **and** the desktop table at once, so any count-based assertion must be scoped with `within(screen.getByRole('table'))` — this suite has already been bitten by unscoped counts doubling.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/app/account/organizations/detail/companies-tab.test.tsx`
Expected: FAIL — cannot resolve `./companies-tab`.

- [ ] **Step 3: Add the types and the mask**

In `ui/src/lib/types.ts`:

```ts
export type TaxIDKind = 'cnpj' | 'cpf'

export interface Company {
  id: string
  tax_id: string
  tax_id_kind: TaxIDKind
  legal_name: string
  trade_name?: string
  created_at: string
}

export interface CompanyActor {
  user_id: string
  name?: string
  granted_by?: string
  created_at: string
}

/**
 * Masks a stored digits-only tax id. The server stores digits so two spellings
 * of one document cannot both be registered; people read the mask.
 */
export function formatTaxID(taxID: string, kind: TaxIDKind): string {
  if (kind === 'cnpj' && taxID.length === 14) {
    return `${taxID.slice(0, 2)}.${taxID.slice(2, 5)}.${taxID.slice(5, 8)}/${taxID.slice(8, 12)}-${taxID.slice(12)}`
  }
  if (kind === 'cpf' && taxID.length === 11) {
    return `${taxID.slice(0, 3)}.${taxID.slice(3, 6)}.${taxID.slice(6, 9)}-${taxID.slice(9)}`
  }
  return taxID
}
```

- [ ] **Step 4: Add the fetchers and mutations**

`queries.ts`:

```ts
export async function fetchCompanies(organizationID: string): Promise<Company[]> {
  const { data } = await api.get(`/organizations/${organizationID}/companies`)
  return data.companies ?? []
}

export async function fetchCompanyActors(organizationID: string, companyID: string): Promise<CompanyActor[]> {
  const { data } = await api.get(`/organizations/${organizationID}/companies/${companyID}/actors`)
  return data.actors ?? []
}

/** Never throws on a miss: the caller lets the person type the names. */
export async function lookupTaxID(
  organizationID: string,
  taxID: string,
): Promise<{ legal_name: string; trade_name: string } | null> {
  try {
    const { data } = await api.get(`/organizations/${organizationID}/companies/lookup`, { params: { tax_id: taxID } })
    return data.found ? { legal_name: data.legal_name, trade_name: data.trade_name ?? '' } : null
  } catch {
    return null
  }
}
```

`mutations.ts`:

```ts
export async function registerCompanyAPI(
  organizationID: string,
  body: { tax_id: string; legal_name: string; trade_name?: string },
): Promise<Company> {
  const { data } = await api.post(`/organizations/${organizationID}/companies`, body)
  return data
}

export async function renameCompanyAPI(
  organizationID: string,
  companyID: string,
  body: { legal_name: string; trade_name?: string },
): Promise<void> {
  await api.patch(`/organizations/${organizationID}/companies/${companyID}`, body)
}

export async function grantCompanyActorAPI(organizationID: string, companyID: string, userID: string): Promise<void> {
  await api.put(`/organizations/${organizationID}/companies/${companyID}/actors/${userID}`)
}

export async function revokeCompanyActorAPI(organizationID: string, companyID: string, userID: string): Promise<void> {
  await api.delete(`/organizations/${organizationID}/companies/${companyID}/actors/${userID}`)
}
```

- [ ] **Step 5: Write the tab**

Follow `members-tab.tsx` exactly for structure: `useQuery` with the key `['companies', organization.id]`, `ResponsiveDataList`, a `ConfirmDialog`-shaped register dialog, `toast` on success, `QueryError` on failure. The rules specific to this screen:

- The register control renders only when `organization.role` is admin or owner — `assignableRoles(organization.role).length > 0` is the existing predicate for "may manage", and it is the one to reuse rather than a new role comparison.
- The tax-id field triggers `lookupTaxID` on blur once the field holds a complete, well-formed CNPJ; the names it returns pre-fill the two name fields and **stay editable**. A null result changes nothing and shows no error.
- Legal name leads each row, the masked tax id sits beneath it in `text-muted-foreground` — the same hierarchy `members-tab.tsx` uses for name over user id.
- The empty state teaches: *"No companies yet. Add the CNPJ you issue documents for."* Not "nothing here".

- [ ] **Step 6: Add the mock routes**

In `mock.ts`, beside the organization routes: `GET/POST /organizations/:id/companies`, `GET/PATCH /organizations/:id/companies/:companyID`, the actors routes, and `GET /organizations/:id/companies/lookup` returning `{found: true, legal_name: 'ACME COMERCIO LTDA', trade_name: 'Acme'}` for one seeded CNPJ and `{found: false}` otherwise — so the "type them yourself" path is reachable in mock mode without an outage.

- [ ] **Step 7: Add the tab to the detail page and the strings**

A fourth tab in `detail/page.tsx`, after Members, labelled from `t('organizations.companies')`. Add every new key to **both** `en.json` and `pt-BR.json`.

- [ ] **Step 8: Run everything**

Run: `cd ui && npx vitest run && npx tsc --noEmit && npm run lint && npm run build 2>&1 | tail -20`
Expected: all tests pass, tsc clean, lint 0 errors (5 pre-existing warnings), the static export emits the organizations routes.

- [ ] **Step 9: Commit**

```bash
git add ui/src
git commit -m "feat(ui): companies inside an organization

The stored tax id is digits so two spellings cannot both register; the
screen shows the mask. The cnpja lookup pre-fills the names and they stay
editable — a miss changes nothing and says nothing."
```

---

### Task 9: Amend the handoff spec

**Files:**
- Modify: `docs/specs/2026-08-29-organization-handoff.md`

The handoff currently returns an empty organization and records a wart: *"creating an organization is not creating a company — the DF-e still has its own step"*. With Company shipped, that stops being true and the spec must stop saying it.

- [ ] **Step 1: Amend the flow**

Replace the wart paragraph and update the flow so `/account/organizations/new` collects the company in the same pass and the return carries both ids:

```
return_to?organization_id=org_xxx&company_id=cmp_xxx&state=<echoed>
```

Add to the parameters table: `company_id` — out, on success only, present whenever the handoff created a company.

- [ ] **Step 2: Record what did not change**

State explicitly that the three security rules are untouched: `return_to` validated against the client's registered origins, first-party clients only, and no token on the redirect. Adding a parameter to a redirect is exactly the change that quietly relaxes the validation, and the spec should say it did not.

- [ ] **Step 3: Commit**

```bash
git add docs/specs/2026-08-29-organization-handoff.md
git commit -m "docs(spec): the handoff returns a company as well

With Company in accounts, the person types the CNPJ once and the DF-e
asks only for what is its own. The three security rules are unchanged,
which is worth saying: adding a redirect parameter is how validation
quietly relaxes."
```

---

## Self-Review

**Spec coverage.** Outcome → Tasks 1–8. The identity/issuance table → Tasks 2 and 4 (nothing fiscal in the model). `tax_id_kind` → Task 1. Opaque id + lock row → Tasks 2 and 4. Same tax id in two organizations → pinned in Task 5. Série hazard → **out of scope here by design**: it is enforced in `ctech-dfe` at enablement, and this plan's scope note says so. The `User ↔ Company` edge → Tasks 2, 4, 5, 6. Verification flow → the spec puts it out of scope; no task, correctly. cnpja rules (never a gate, server-side, a miss is not an invalid CNPJ) → Task 7, all three. One organization/one company reads as one thing → Task 8's empty state and single-name hierarchy. Handoff consequence → Task 9. Billing's designated company → the spec assigns it to billing's own spec; no task, correctly.

**Placeholder scan.** No TBDs. Task 8 Step 5 describes the component in prose rather than a full listing — deliberate: it follows `members-tab.tsx`, which the implementer must read anyway, and transcribing a near-copy here would let the two drift. The three rules that are *not* copyable from that file are stated explicitly.

**Type consistency.** `NormalizeTaxID` returns `(string, string, bool)` in Tasks 1, 5 and 7. `Company`/`Actor` field names match across model, repository, service, handler DTO and TS interface. `Register` takes `(ctx, orgID, actorUserID, actorName, rawTaxID, legalName, tradeName)` in Tasks 5 and 6. `ErrTaxIDTaken` is raised in Task 4 and mapped in Task 6. `Repository` in Task 4 matches `fakeRepo` in Task 5 method for method.

One gap found and closed while reviewing: Task 7's route had to be registered **before** `/:company_id` or Fiber captures `lookup` as a company id — now stated in the step.


---

## Execution record (2026-08-29)

Implemented inline. Deviations from the plan as written, and why:

**Task 1 was rewritten for the alphanumeric CNPJ.** The plan assumed digits and a single
shared weight table. Both were wrong: canonical is mask-stripped uppercase alphanumeric,
and the CNPJ and CPF weight sequences genuinely differ. `NormalizeTaxID` now takes
`charValue` (ASCII − 48, the Receita's rule) plus a per-document weight function, and
`canonicalize` refuses stray characters rather than dropping them — silently dropping one
can turn a typo into a different valid document.

**`main.go` hoists the organizations route group.** The plan had the company handler mount
on a group the organization handler created inline. Both now share one `orgsGroup`, so one
`RequireAuth` + `RequireClientID` covers both rather than two groups that could drift.

**`Create` uses `BuildPutTxItemIfAbsent` for the company row too**, not `BuildPutTxItem`.
A UUIDv7 collision is not the concern; a retry replaying the same transaction is.

**`ListForUser` was added to the repository**, which the plan omitted. It is the query
`ctech-dfe` needs — "which companies may this person act for" — and the GSI it reads
already existed for the source-ref lookup.

**One test-suite bug repeated.** `ResponsiveDataList` renders the mobile cards and the
desktop table simultaneously, so five UI tests failed on "Found multiple elements". The
plan warned about exactly this and the tests were written unscoped anyway. Fixed with a
`seeded()` helper that scopes to `getByRole('table')`.

Task 9 amended the handoff spec, ADR 0022 and this plan's own constraints for the
alphanumeric CNPJ.

**Still open:** the `ctech-dfe` re-key (out of scope here, its own plan), and the
`/account/organizations/new` handoff screen itself — the spec is amended, the screen is
not built.
