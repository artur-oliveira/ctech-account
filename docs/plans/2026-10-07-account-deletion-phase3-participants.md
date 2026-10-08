# Account Deletion — Phase 3 (participants: eligibility, acks, reconciler, admin) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make ctech-account the orchestrator of the saga with the other services:
- ask every participant for blockers (at request and at grace end);
- track one status row per participant;
- accept their acks;
- re-publish `user.erase` with backoff until each acks;
- purge the account only when all have acked;
- stop in `stalled` if a participant reports a blocker after the point of no return;
- give support the tools: legal hold, cancel on the user's behalf, redrive.

**Architecture:**
- Participants are configured as JSON (`ERASURE_PARTICIPANTS`): service name, base URL, audience and OAuth client id.
- For eligibility, ctech-account mints its own short-lived service token (it is the issuer) and calls `GET {url}/internal/erasure/eligibility/{sub}` on each participant. A `CompositeOwnership` merges the participants' blockers with the local ones.
- Status rows `SVC#{service}` sit under the request partition in `{env}_account_deletion_requests`.
- Participants ack on `POST /v1.0/internal/erasure/ack` with a client-credentials token carrying `internal:account:erasure-ack`.
- The `purging` step reconciles, purges and finishes.
- SNS publishes carry a `services` message attribute, so participants filter on attributes.

**Tech Stack:** Go 1.27, Fiber v3, DynamoDB, SNS, net/http.

**Spec:** `docs/specs/2026-10-06-account-deletion-saga-protocol.md` §3–§8 and `docs/specs/2026-10-06-account-deletion-ctech-account.md` §4, §6.

**Builds on:** Phases 1, 2a, 2b.

**Out of scope:**
- The participants' own consumers, purges and eligibility endpoints: one plan per service repo.
- Single-service unlink (scope `service`): it needs a per-service lock instead of the account-wide one, and gets its own plan.
- The account-deletion UI (Phase 4).

## Global Constraints

- Branch `feat/account-deletion-phase3`, from `main` after Phase 2 merges. Conventional Commits, **no `Co-Authored-By` / Claude attribution**.
- Internal scopes follow the existing convention `internal:{owner}:{action}` (ruling R10):
  - `internal:account:erasure-ack` — granted to each participant's confidential client;
  - `internal:account:erasure-subject` — granted to `dfe` only;
  - `internal:{service}:erasure-eligibility` — carried by the token account mints for each participant.
- Each participant's `client_id` is a **dedicated confidential client used only for erasure** (decision of 2026-10-08), not the service's regular client. The operator grants it `internal:account:erasure-ack` (and dfe's also `internal:account:erasure-subject`).
- Ack backoff after the first dispatch: 15 min, 1 h, 6 h, then every 24 h. Log an `ERROR` "participant ack overdue" on every reconcile once 48 h have passed since the first dispatch.
- Eligibility calls: 5 s timeout. Any error or non-2xx is **not** "eligible":
  - at request it is a `503`;
  - at grace end the step fails and is retried.
- The subject endpoint returns the CPF only while the request is `purging`, writes an audit row per call, and never logs the CPF.
- SNS: every publish carries `MessageAttributes.services` (`String.Array`, JSON array of names). Participants subscribe with a `FilterPolicy` `{"services": ["<name>"]}` at the default (attribute) scope.
- `go vet ./... && go test ./...` green after every task.

## Rulings

- **R10 Scope names** follow the repo's `internal:` convention instead of the spec's `account:erasure:ack` / `erasure:eligibility`. Spec and go-common README updated in Task 8.
- **R11 `ERASURE_PARTICIPANTS` replaces `ERASURE_SERVICES`.** The service list is derived from it. `ERASURE_SERVICES` shipped dark in Phase 1 and was never set anywhere.
- **R12 No support-initiated deletion endpoint.** A DPO request is served by the user's own flow or, if needed, by an operator through the same service methods. **No `purged-since` endpoint**: the backup-restore runbook queries the table directly (Task 8 documents the query). Cost if wrong: one more manual runbook step.
- **R13 "Four-eyes" for lifting a legal hold** means a *different* admin than the one who placed it. Cost if wrong: two admins can still collude.

## Review Focus

1. **A participant acks `blocked` after the request is irreversible.** The request becomes `stalled`: no purge, no further re-publishing. Support redrives after resolving. Test: Task 4 `TestPurging_BlockedAckStalls`.
2. **A participant never acks.** Re-publishing follows the backoff, to that participant only, and the purge never runs without its ack. Test: Task 4 `TestPurging_RepublishesOnlyToSilentParticipantsWithBackoff`.
3. **An ack arrives twice, or before its status row exists.** A duplicate is a no-op. A premature ack fails, so the participant's message is redelivered. Test: Task 4 `TestAck_DuplicateAndPremature`.
4. **A participant's eligibility endpoint is down at grace end.** The request stays `pending` and is retried, never locked blind. Test: Task 3 `TestCompositeOwnership_RemoteErrorIsNotEligible`.
5. **A legal hold placed during grace.** Grace ends but the request does not lock. Lifting the hold needs a second admin. Test: Task 6 `TestLegalHold_PausesAndNeedsSecondAdmin`.

---

### Task 1: Participants config and per-service status storage

**Files:** Modify `api/internal/config/config.go`, `api/internal/domain/deletion/model.go`, `api/internal/domain/deletion/repository.go`; Test `api/internal/config/config_test.go`, `api/internal/domain/deletion/service_test.go` (memRepo), `api/internal/handler/testhelpers_test.go` (memDeletionRepo).

**Interfaces:**
- Produces:
  - `config.ErasureParticipant{Service, URL, Audience, ClientID string}` (json tags `service,url,audience,client_id`)
  - `Config.ErasureParticipants []ErasureParticipant`
  - `func (c *Config) ErasureServiceNames() []string`
  - `func (c *Config) ErasureServiceForClient(clientID string) (string, bool)`
- Produces:
  - `deletion.ServiceStatus{PK, SK, RequestID, Service, Status, Attempts int, FirstDispatchAt, LastDispatchAt, AckedAt string, Counts map[string]int, Blockers []string}`
  - statuses `ServiceDispatched="dispatched"`, `ServiceAcked="acked"`, `ServiceBlocked="blocked"`
- Produces `Repository` methods:
  - `PutServices(ctx, []*ServiceStatus) error` (create if absent)
  - `ListServices(ctx, requestID) ([]*ServiceStatus, error)`
  - `SaveService(ctx, st *ServiceStatus, fromStatus string) error` (`ErrStateChanged` on mismatch, `ErrNotFound` if absent)

- [ ] **Step 1: Failing test** — `api/internal/config/config_test.go` (append, or create in `package config`):

```go
func TestErasureParticipants(t *testing.T) {
	t.Setenv("ERASURE_PARTICIPANTS", `[{"service":"wallet","url":"https://w","audience":"https://wallet.aoctech.app","client_id":"wallet-svc"},{"service":"dfe","url":"https://d","audience":"https://dfe.aoctech.app","client_id":"dfe"}]`)
	ps, err := parseErasureParticipants()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := &Config{ErasureParticipants: ps}
	if names := c.ErasureServiceNames(); len(names) != 2 || names[0] != "wallet" || names[1] != "dfe" {
		t.Fatalf("names %v", names)
	}
	if svc, ok := c.ErasureServiceForClient("dfe"); !ok || svc != "dfe" {
		t.Fatalf("client lookup: %q %v", svc, ok)
	}
	if _, ok := c.ErasureServiceForClient("stranger"); ok {
		t.Fatal("unknown client must not map to a service")
	}
	t.Setenv("ERASURE_PARTICIPANTS", `[{"service":"x"}]`)
	if _, err := parseErasureParticipants(); err == nil {
		t.Fatal("a participant without url/audience/client_id must be rejected")
	}
}
```

- [ ] **Step 2: Fail** — `cd api && go test ./internal/config/ -run TestErasureParticipants -count=1` → build FAIL.

- [ ] **Step 3: Implement config.** In `config.go`:
- Replace the field `ErasureServices []string` with `ErasureParticipants []ErasureParticipant`.
- Delete the `ERASURE_SERVICES` parsing block and its literal line.
- Add:

```go
// ErasureParticipant is one service of the account-deletion saga
// (docs/specs/2026-10-06-account-deletion-saga-protocol.md).
type ErasureParticipant struct {
	Service  string `json:"service"`   // name used in erasure messages and SNS filters
	URL      string `json:"url"`       // base URL of the service's API
	Audience string `json:"audience"`  // aud of the token account mints for it
	ClientID string `json:"client_id"` // its confidential client; acks are matched on azp
}

func parseErasureParticipants() ([]ErasureParticipant, error) {
	raw := os.Getenv("ERASURE_PARTICIPANTS")
	if raw == "" {
		return nil, nil
	}
	var ps []ErasureParticipant
	if err := json.Unmarshal([]byte(raw), &ps); err != nil {
		return nil, fmt.Errorf("ERASURE_PARTICIPANTS: %w", err)
	}
	for _, p := range ps {
		if p.Service == "" || p.URL == "" || p.Audience == "" || p.ClientID == "" {
			return nil, fmt.Errorf("ERASURE_PARTICIPANTS: %q needs service, url, audience and client_id", p.Service)
		}
	}
	return ps, nil
}

func (c *Config) ErasureServiceNames() []string {
	names := make([]string, 0, len(c.ErasureParticipants))
	for _, p := range c.ErasureParticipants {
		names = append(names, p.Service)
	}
	return names
}

func (c *Config) ErasureServiceForClient(clientID string) (string, bool) {
	for _, p := range c.ErasureParticipants {
		if p.ClientID == clientID {
			return p.Service, true
		}
	}
	return "", false
}
```

In `Load`, before `return &Config{`:

```go
	erasureParticipants, err := parseErasureParticipants()
	if err != nil {
		return nil, err
	}
```

Then `ErasureParticipants: erasureParticipants,` in the literal (import `encoding/json`). In `cmd/api/main.go`, replace every `cfg.ErasureServices` with `cfg.ErasureServiceNames()`.

- [ ] **Step 4: Status storage.** `model.go`:

```go
const (
	ServiceDispatched = "dispatched"
	ServiceAcked      = "acked"
	ServiceBlocked    = "blocked"
)

// ServiceStatus is one participant's progress on a request
// (pk REQ#{request_id}, sk SVC#{service}).
type ServiceStatus struct {
	PK              string         `dynamodbav:"pk"`
	SK              string         `dynamodbav:"sk"`
	RequestID       string         `dynamodbav:"request_id"`
	Service         string         `dynamodbav:"service"`
	Status          string         `dynamodbav:"service_status"`
	Attempts        int            `dynamodbav:"attempts"`
	FirstDispatchAt string         `dynamodbav:"first_dispatch_at"`
	LastDispatchAt  string         `dynamodbav:"last_dispatch_at"`
	AckedAt         string         `dynamodbav:"acked_at,omitempty"`
	Counts          map[string]int `dynamodbav:"counts,omitempty"`
	Blockers        []string       `dynamodbav:"blockers,omitempty"`
}

func serviceSK(service string) string { return "SVC#" + service }
```

`repository.go` — add to the `Repository` interface:

```go
	// PutServices creates the status rows that do not exist yet.
	PutServices(ctx context.Context, statuses []*ServiceStatus) error
	ListServices(ctx context.Context, requestID string) ([]*ServiceStatus, error)
	// SaveService writes st if its stored status is still fromStatus.
	SaveService(ctx context.Context, st *ServiceStatus, fromStatus string) error
```

and implement on `dynamoRepository`:

```go
func (r *dynamoRepository) PutServices(ctx context.Context, statuses []*ServiceStatus) error {
	for _, st := range statuses {
		item, err := attributevalue.MarshalMap(st)
		if err != nil {
			return err
		}
		_, err = r.db.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(r.table), Item: item,
			ConditionExpression: aws.String("attribute_not_exists(pk)"),
		})
		if err != nil && !dynamo.IsConditionFailed(err) {
			return fmt.Errorf("deletion: put service %s: %w", st.Service, err)
		}
	}
	return nil
}

func (r *dynamoRepository) ListServices(ctx context.Context, requestID string) ([]*ServiceStatus, error) {
	out, err := r.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(r.table),
		KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :svc)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": str(BuildPK(requestID)), ":svc": str("SVC#"),
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("deletion: list services: %w", err)
	}
	sts := make([]*ServiceStatus, 0, len(out.Items))
	for _, it := range out.Items {
		var st ServiceStatus
		if err := attributevalue.UnmarshalMap(it, &st); err != nil {
			return nil, err
		}
		sts = append(sts, &st)
	}
	return sts, nil
}

func (r *dynamoRepository) SaveService(ctx context.Context, st *ServiceStatus, fromStatus string) error {
	item, err := attributevalue.MarshalMap(st)
	if err != nil {
		return err
	}
	_, err = r.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(r.table), Item: item,
		ConditionExpression:       aws.String("service_status = :from"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":from": str(fromStatus)},
	})
	if dynamo.IsConditionFailed(err) {
		return ErrStateChanged
	}
	return err
}
```

Add the same three methods to both in-memory repos. In `deletion/service_test.go`'s `memRepo`, add a `services map[string]ServiceStatus` field keyed `requestID+"|"+service` and initialize it in `newMemRepo`:

```go
func (m *memRepo) PutServices(_ context.Context, sts []*ServiceStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, st := range sts {
		k := st.RequestID + "|" + st.Service
		if _, ok := m.services[k]; !ok {
			m.services[k] = *st
		}
	}
	return nil
}

func (m *memRepo) ListServices(_ context.Context, requestID string) ([]*ServiceStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*ServiceStatus
	for _, st := range m.services {
		if st.RequestID == requestID {
			c := st
			out = append(out, &c)
		}
	}
	return out, nil
}

func (m *memRepo) SaveService(_ context.Context, st *ServiceStatus, from string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := st.RequestID + "|" + st.Service
	cur, ok := m.services[k]
	if !ok {
		return ErrNotFound
	}
	if cur.Status != from {
		return ErrStateChanged
	}
	m.services[k] = *st
	return nil
}
```

Do the same for `memDeletionRepo` in `handler/testhelpers_test.go`, with type names prefixed `deletion.`.

- [ ] **Step 5: Pass** — `go vet ./... && go test ./... 2>&1 | tail -30` → all `ok`.
- [ ] **Step 6: Commit** — `git add internal/config internal/domain/deletion internal/handler/testhelpers_test.go cmd/api/main.go && git commit -m "feat(api): erasure participants config and per-service status rows"`

---

### Task 2: Per-service re-publish and SNS filter attribute

**Files:** Modify `api/internal/domain/deletion/service.go` (`Locker.Erase` signature), `api/internal/domain/deletion/locker.go`, `api/internal/domain/deletion/locker_test.go`, `api/internal/domain/deletion/service_test.go` (`fakeLocker`), `api/internal/erasurepub/sns.go`; Create `api/internal/erasurepub/sns_test.go`.

**Interfaces:**
- Changes `Locker.Erase(ctx context.Context, r *Request, services []string) error`. An empty `services` means "all configured".
- Produces `func erasurepub.publishInput(topicARN string, m erasure.Message) (*sns.PublishInput, error)`, used by `Publish`.

- [ ] **Step 1: Failing tests** — `api/internal/erasurepub/sns_test.go`:

```go
package erasurepub

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"gopkg.aoctech.app/api-commons/erasure"
)

func TestPublishInputCarriesServicesAttribute(t *testing.T) {
	in, err := publishInput("arn:topic", erasure.Message{
		Version: erasure.Version, Type: erasure.TypeErase, RequestID: "r1", Sub: "u1",
		Scope: erasure.ScopeAccount, Services: []string{"wallet", "dfe"}, Attempt: 2, IssuedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("publishInput: %v", err)
	}
	attr, ok := in.MessageAttributes["services"]
	if !ok || aws.ToString(attr.DataType) != "String.Array" || aws.ToString(attr.StringValue) != `["wallet","dfe"]` {
		t.Fatalf("services attribute %+v", attr)
	}
}
```

Append to `locker_test.go`:

```go
func TestAccountLocker_EraseToSubset(t *testing.T) {
	pub := &fakePub{}
	l := NewAccountLocker(&fakeBlocker{}, &fakeSessions{}, &fakeKeys{}, &fakeTokens{}, pub, []string{"wallet", "dfe"})
	if err := l.Erase(context.Background(), lockedRequest(), []string{"dfe"}); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if len(pub.msgs) != 1 || len(pub.msgs[0].Services) != 1 || pub.msgs[0].Services[0] != "dfe" {
		t.Fatalf("published %+v", pub.msgs)
	}
}
```

- [ ] **Step 2: Fail** — `go test ./internal/erasurepub/ ./internal/domain/deletion/ -count=1` → build FAIL.

- [ ] **Step 3: Implement.**

`sns.go`:

```go
// publishInput builds the SNS publish with a services attribute, so each
// participant's subscription filters with {"services": ["<name>"]}.
func publishInput(topicARN string, m erasure.Message) (*sns.PublishInput, error) {
	body, err := erasure.Encode(m)
	if err != nil {
		return nil, err
	}
	services, err := json.Marshal(m.Services)
	if err != nil {
		return nil, err
	}
	return &sns.PublishInput{
		TopicArn: aws.String(topicARN),
		Message:  aws.String(string(body)),
		MessageAttributes: map[string]snstypes.MessageAttributeValue{
			"services": {DataType: aws.String("String.Array"), StringValue: aws.String(string(services))},
		},
	}, nil
}

func (p *SNS) Publish(ctx context.Context, m erasure.Message) error {
	in, err := publishInput(p.topicARN, m)
	if err != nil {
		return err
	}
	_, err = p.client.Publish(ctx, in)
	return err
}
```

(imports `encoding/json`, `snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"`).

`service.go`: change `Locker.Erase` to `Erase(ctx context.Context, r *Request, services []string) error`. The `StateLocked` step calls `s.locker.Erase(ctx, r, nil)`. `fakeLocker.Erase` takes the extra parameter.

`locker.go`:
- `publish` gets a `services []string` parameter: `if len(services) == 0 { services = l.services }`, and `if len(services) == 0 { return nil }`.
- `Lock` and `Unlock` pass `nil`.
- `Erase(ctx, r, services)` passes `services`.

- [ ] **Step 4: Pass** — `go vet ./... && go test ./... 2>&1 | tail -30` → `ok`.
- [ ] **Step 5: Commit** — `git add internal/erasurepub internal/domain/deletion && git commit -m "feat(api): erase to a subset of participants, SNS services attribute"`

---

### Task 3: Participant eligibility

**Files:** Create `api/internal/erasurepub/eligibility.go`, `api/internal/erasurepub/eligibility_test.go`, `api/internal/domain/deletion/composite.go`, `api/internal/domain/deletion/composite_test.go`.

**Interfaces:**
- Produces `type TokenMinter func(audience, scope string) (string, error)`.
- Produces `func erasurepub.NewEligibility(httpClient *http.Client, participants []config.ErasureParticipant, mint TokenMinter) *Eligibility`, with `Check(ctx, userID string) ([]erasure.Blocker, error)`.
- Produces `func deletion.NewCompositeOwnership(local Ownership, remote RemoteEligibility) *CompositeOwnership`, with `type RemoteEligibility interface { Check(ctx, userID string) ([]erasure.Blocker, error) }`.

- [ ] **Step 1: Failing tests.**

`api/internal/erasurepub/eligibility_test.go`:

```go
package erasurepub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.aoctech.app/account/api/internal/config"
)

func TestEligibility_MergesBlockersAndFailsClosed(t *testing.T) {
	var gotAuth, gotPath string
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_, _ = w.Write([]byte(`{"eligible":false,"blockers":[{"code":"wallet.balance_nonzero","detail":{"amount_cents":1250}}]}`))
	}))
	defer ok.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer down.Close()

	mint := func(aud, scope string) (string, error) { return "tok:" + aud + ":" + scope, nil }
	e := NewEligibility(ok.Client(), []config.ErasureParticipant{{Service: "wallet", URL: ok.URL, Audience: "https://wallet", ClientID: "w"}}, mint)
	blockers, err := e.Check(context.Background(), "u1")
	if err != nil || len(blockers) != 1 || blockers[0].Code != "wallet.balance_nonzero" {
		t.Fatalf("blockers %+v err %v", blockers, err)
	}
	if gotPath != "/internal/erasure/eligibility/u1" || gotAuth != "Bearer tok:https://wallet:internal:wallet:erasure-eligibility" {
		t.Fatalf("path %q auth %q", gotPath, gotAuth)
	}

	e = NewEligibility(down.Client(), []config.ErasureParticipant{{Service: "dfe", URL: down.URL, Audience: "https://dfe", ClientID: "d"}}, mint)
	if _, err := e.Check(context.Background(), "u1"); err == nil || !strings.Contains(err.Error(), "dfe") {
		t.Fatalf("an unreachable participant must be an error naming it, got %v", err)
	}
}
```

`api/internal/domain/deletion/composite_test.go`:

```go
package deletion

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/api-commons/erasure"
)

type fakeRemote struct {
	blockers []erasure.Blocker
	err      error
}

func (f fakeRemote) Check(context.Context, string) ([]erasure.Blocker, error) { return f.blockers, f.err }

func TestCompositeOwnership_MergesLocalAndRemote(t *testing.T) {
	c := NewCompositeOwnership(&fakeOwnership{sole: []string{"o1"}, blockers: []erasure.Blocker{{Code: "local"}}},
		fakeRemote{blockers: []erasure.Blocker{{Code: "remote"}}})
	sole, blockers, err := c.Assess(context.Background(), "u1")
	if err != nil || len(sole) != 1 || len(blockers) != 2 {
		t.Fatalf("sole %v blockers %v err %v", sole, blockers, err)
	}
}

func TestCompositeOwnership_RemoteErrorIsNotEligible(t *testing.T) {
	c := NewCompositeOwnership(&fakeOwnership{}, fakeRemote{err: errors.New("dfe down")})
	if _, _, err := c.Assess(context.Background(), "u1"); err == nil {
		t.Fatal("an unreachable participant must fail the assessment, never pass it")
	}
}

func TestGraceEnd_RemoteDownStaysPending(t *testing.T) {
	f := newFixture(t)
	remote := &fakeRemote{}
	f.svc = f.svc.WithOwnership(NewCompositeOwnership(&fakeOwnership{}, remote))
	r := f.confirm(t)
	remote.err = errors.New("wallet down")
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx)
	if s := f.stored(t, r.ID); s.State != StatePending || s.DueState != string(StatePending) {
		t.Fatalf("must stay pending and due: %+v", s)
	}
}
```

(`fakeRemote` is used through a pointer in the last test so its `err` can change; give it a pointer receiver or keep both forms consistent.)

- [ ] **Step 2: Fail** — build FAIL.

- [ ] **Step 3: Implement.**

`erasurepub/eligibility.go`:

```go
package erasurepub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/api-commons/erasure"
)

// TokenMinter signs a short-lived service token for audience with scope.
// ctech-account is the issuer, so it mints instead of calling its own /token.
type TokenMinter func(audience, scope string) (string, error)

// Eligibility asks every participant for blockers (saga protocol §4.1).
type Eligibility struct {
	http         *http.Client
	participants []config.ErasureParticipant
	mint         TokenMinter
}

func NewEligibility(httpClient *http.Client, participants []config.ErasureParticipant, mint TokenMinter) *Eligibility {
	return &Eligibility{http: httpClient, participants: participants, mint: mint}
}

// Check returns every participant's blockers. Any failure is an error, never
// "eligible": a participant we cannot hear from may hold the user's money.
func (e *Eligibility) Check(ctx context.Context, userID string) ([]erasure.Blocker, error) {
	var all []erasure.Blocker
	for _, p := range e.participants {
		blockers, err := e.checkOne(ctx, p, userID)
		if err != nil {
			return nil, fmt.Errorf("eligibility of %s: %w", p.Service, err)
		}
		all = append(all, blockers...)
	}
	return all, nil
}

func (e *Eligibility) checkOne(ctx context.Context, p config.ErasureParticipant, userID string) ([]erasure.Blocker, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := e.mint(p.Audience, "internal:"+p.Service+":erasure-eligibility")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL+"/internal/erasure/eligibility/"+url.PathEscape(userID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var el erasure.Eligibility
	if err := json.NewDecoder(resp.Body).Decode(&el); err != nil {
		return nil, err
	}
	return el.Blockers, nil
}
```

`deletion/composite.go`:

```go
package deletion

import (
	"context"

	"gopkg.aoctech.app/api-commons/erasure"
)

type RemoteEligibility interface {
	Check(ctx context.Context, userID string) ([]erasure.Blocker, error)
}

// CompositeOwnership adds the participants' blockers to the local assessment.
type CompositeOwnership struct {
	local  Ownership
	remote RemoteEligibility
}

func NewCompositeOwnership(local Ownership, remote RemoteEligibility) *CompositeOwnership {
	return &CompositeOwnership{local: local, remote: remote}
}

func (c *CompositeOwnership) Assess(ctx context.Context, userID string) ([]string, []erasure.Blocker, error) {
	sole, blockers, err := c.local.Assess(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	remote, err := c.remote.Check(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return sole, append(blockers, remote...), nil
}
```

`handler/deletion.go` — `request`: an `Assess` error must surface as `503`, not `500`:

```go
	if err != nil && !errors.Is(err, deletion.ErrOpenRequest) {
		var blocked *deletion.BlockedError
		if !errors.As(err, &blocked) {
			return apierror.ServiceUnavailable("Could not check every CTech product right now. Try again in a few minutes.", c.Path()).WithCause(err).Send(c)
		}
	}
```

Place it after the existing `BlockedError` and `ErrOpenRequest` branches, so it replaces the generic `ServerError` branch.

- [ ] **Step 4: Pass** — `go vet ./... && go test ./... 2>&1 | tail -30` → `ok`.
- [ ] **Step 5: Commit** — `git add internal/erasurepub internal/domain/deletion internal/handler/deletion.go && git commit -m "feat(api): participant eligibility in deletion assessment"`

---

### Task 4: Acks, reconciler, `stalled`

**Files:** Modify `api/internal/domain/deletion/model.go`, `api/internal/domain/deletion/service.go`; Test `api/internal/domain/deletion/service_test.go`.

**Interfaces:**
- Produces `StateStalled State = "stalled"` (open: holds the marker).
- Produces `func (s *Service) Ack(ctx context.Context, service string, ack erasure.Ack) error`.
- Produces `func (s *Service) WithParticipants(services []string) *Service`. This replaces `WithPurger`'s `awaitParticipants` bool: a request awaits exactly these services. Update the 2a `WithPurger` signature to `WithPurger(p Purger)` and its callers/tests.
- `ErrUnknownRequest` is `ErrNotFound`; ack on a missing status row returns `ErrNotFound`.

- [ ] **Step 1: Failing tests** — append to `service_test.go`:

```go
func participantFixture(t *testing.T, services ...string) (*fixture, *fakePurger) {
	t.Helper()
	f := newFixture(t)
	p := &fakePurger{}
	f.svc = f.svc.WithOwnership(&fakeOwnership{}).WithPurger(p).WithParticipants(services)
	return f, p
}

func (f *fixture) toPurging(t *testing.T) *Request {
	t.Helper()
	r := f.confirm(t)
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx)
	if s := f.stored(t, r.ID); s.State != StatePurging {
		t.Fatalf("state %s, want purging", s.State)
	}
	return r
}

func TestPurging_PurgesOnlyAfterEveryAck(t *testing.T) {
	f, p := participantFixture(t, "wallet", "dfe")
	r := f.toPurging(t)
	if err := f.svc.Ack(ctx, "wallet", erasure.Ack{RequestID: r.ID, Result: erasure.ResultDone}); err != nil {
		t.Fatalf("Ack wallet: %v", err)
	}
	f.svc.ProcessDue(ctx)
	if p.calls != 0 {
		t.Fatal("must not purge before every participant acked")
	}
	if err := f.svc.Ack(ctx, "dfe", erasure.Ack{RequestID: r.ID, Result: erasure.ResultDone}); err != nil {
		t.Fatalf("Ack dfe: %v", err)
	}
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).State != StatePurged || p.calls != 1 {
		t.Fatalf("state %s calls %d", f.stored(t, r.ID).State, p.calls)
	}
}

func TestPurging_RepublishesOnlyToSilentParticipantsWithBackoff(t *testing.T) {
	f, _ := participantFixture(t, "wallet", "dfe")
	r := f.toPurging(t)
	_ = f.svc.Ack(ctx, "wallet", erasure.Ack{RequestID: r.ID, Result: erasure.ResultDone})
	base := f.locker.erases // the initial broadcast
	f.now = f.now.Add(10 * time.Minute)
	f.svc.ProcessDue(ctx)
	if f.locker.erases != base {
		t.Fatal("no re-publish before the first backoff (15 min)")
	}
	f.now = f.now.Add(6 * time.Minute)
	f.svc.ProcessDue(ctx)
	if f.locker.erases != base+1 || len(f.locker.lastErase) != 1 || f.locker.lastErase[0] != "dfe" {
		t.Fatalf("re-publish to dfe only: erases=%d last=%v", f.locker.erases, f.locker.lastErase)
	}
}

func TestPurging_BlockedAckStalls(t *testing.T) {
	f, p := participantFixture(t, "wallet")
	r := f.toPurging(t)
	if err := f.svc.Ack(ctx, "wallet", erasure.Ack{RequestID: r.ID, Result: erasure.ResultBlocked,
		Blockers: []erasure.Blocker{{Code: "wallet.balance_nonzero"}}}); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	f.svc.ProcessDue(ctx)
	if s := f.stored(t, r.ID); s.State != StateStalled || s.DueState != "" || p.calls != 0 {
		t.Fatalf("stalled request %+v purge calls %d", s, p.calls)
	}
	if _, err := f.svc.Cancel(ctx, r.ID, f.mail.cancelToken); !errors.Is(err, ErrNotCancellable) {
		t.Fatal("a stalled request is past the point of no return")
	}
}

func TestAck_DuplicateAndPremature(t *testing.T) {
	f, _ := participantFixture(t, "wallet")
	if err := f.svc.Ack(ctx, "wallet", erasure.Ack{RequestID: "nope", Result: erasure.ResultDone}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ack for an unknown request: %v", err)
	}
	r := f.toPurging(t)
	ack := erasure.Ack{RequestID: r.ID, Result: erasure.ResultDone}
	if err := f.svc.Ack(ctx, "wallet", ack); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if err := f.svc.Ack(ctx, "wallet", ack); err != nil {
		t.Fatalf("duplicate ack must be a no-op, got %v", err)
	}
}
```

`fakeLocker` gets `lastErase []string`, set by `Erase(_ context.Context, _ *Request, services []string)`. Add `"time"` and `erasure` imports if missing.

- [ ] **Step 2: Fail** — build FAIL (`StateStalled`, `Ack`, `WithParticipants` undefined).

- [ ] **Step 3: Implement.**

`model.go`: `StateStalled State = "stalled" // a participant reported a blocker after LOCKED; support resolves`. It stays open (it holds the marker), so `Open()` needs no change.

`service.go`:

```go
// WithParticipants makes purging wait for an ack from each of these services
// (saga protocol §7). Empty means no participants: purge right away.
func (s *Service) WithParticipants(services []string) *Service {
	s.participants = services
	return s
}

// Ack records a participant's result for a request. A duplicate is a no-op;
// an ack before the status row exists fails, so the participant's message is
// redelivered.
func (s *Service) Ack(ctx context.Context, service string, ack erasure.Ack) error {
	sts, err := s.repo.ListServices(ctx, ack.RequestID)
	if err != nil {
		return err
	}
	for _, st := range sts {
		if st.Service != service {
			continue
		}
		if st.Status != ServiceDispatched {
			return nil // already recorded
		}
		st.AckedAt, st.Counts = ts(s.now()), ack.Counts
		st.Status = ServiceAcked
		if ack.Result == erasure.ResultBlocked {
			st.Status = ServiceBlocked
			for _, b := range ack.Blockers {
				st.Blockers = append(st.Blockers, b.Code)
			}
		}
		if err := s.repo.SaveService(ctx, st, ServiceDispatched); err != nil && !errors.Is(err, ErrStateChanged) {
			return err
		}
		if r, err := s.repo.Get(ctx, ack.RequestID); err == nil && r.State == StatePurging {
			r.setDue(s.now()) // let the worker act on it now
			r.UpdatedAt = ts(s.now())
			_ = s.repo.Save(ctx, r, StatePurging) // a lost race only delays to the next due time
		}
		return nil
	}
	return ErrNotFound
}

var ackBackoff = []time.Duration{15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}

func nextDispatch(st *ServiceStatus) time.Time {
	i := min(st.Attempts-1, len(ackBackoff)-1)
	return parseTime(st.LastDispatchAt).Add(ackBackoff[i])
}

// reconcile re-publishes to silent participants on their backoff and reports
// whether every participant acked. A blocked ack stalls the request.
func (s *Service) reconcile(ctx context.Context, r *Request, now time.Time) (bool, error) {
	sts, err := s.repo.ListServices(ctx, r.ID)
	if err != nil {
		return false, err
	}
	var silent []*ServiceStatus
	next := time.Time{}
	for _, st := range sts {
		switch st.Status {
		case ServiceAcked:
		case ServiceBlocked:
			r.State, r.UpdatedAt = StateStalled, ts(now)
			r.clearDue()
			return false, s.repo.Save(ctx, r, StatePurging)
		default:
			if now.Sub(parseTime(st.FirstDispatchAt)) > 48*time.Hour {
				observability.Error(ctx, "deletion: participant ack overdue", errors.New("no ack after 48h"),
					"request_id", r.ID, "service", st.Service)
			}
			if !now.Before(nextDispatch(st)) {
				silent = append(silent, st)
			} else if next.IsZero() || nextDispatch(st).Before(next) {
				next = nextDispatch(st)
			}
		}
	}
	if len(silent) == 0 && next.IsZero() {
		return true, nil
	}
	if len(silent) > 0 {
		names := make([]string, 0, len(silent))
		for _, st := range silent {
			names = append(names, st.Service)
		}
		if err := s.locker.Erase(ctx, r, names); err != nil {
			return false, err
		}
		for _, st := range silent {
			st.Attempts++
			st.LastDispatchAt = ts(now)
			if err := s.repo.SaveService(ctx, st, ServiceDispatched); err != nil && !errors.Is(err, ErrStateChanged) {
				return false, err
			}
			if n := nextDispatch(st); next.IsZero() || n.Before(next) {
				next = n
			}
		}
	}
	r.UpdatedAt = ts(now)
	r.setDue(next)
	return false, s.repo.Save(ctx, r, StatePurging)
}
```

In `step`:

1. The `StateLocked` branch: after `s.locker.Erase(ctx, r, nil)` succeeds and before moving to `purging`, create the status rows:

```go
		if len(s.participants) > 0 {
			rows := make([]*ServiceStatus, 0, len(s.participants))
			for _, svc := range s.participants {
				rows = append(rows, &ServiceStatus{PK: BuildPK(r.ID), SK: serviceSK(svc), RequestID: r.ID, Service: svc,
					Status: ServiceDispatched, Attempts: 1, FirstDispatchAt: ts(now), LastDispatchAt: ts(now)})
			}
			if err := s.repo.PutServices(ctx, rows); err != nil {
				return err
			}
		}
		r.State, r.UpdatedAt = StatePurging, ts(now)
		if s.purger != nil {
			r.setDue(now)
		} else {
			r.clearDue()
		}
		return s.repo.Save(ctx, r, StateLocked)
```

2. The `StatePurging` branch:

```go
	case StatePurging:
		if len(s.participants) > 0 {
			done, err := s.reconcile(ctx, r, now)
			if err != nil || !done {
				return err
			}
		}
		if err := s.purger.Purge(ctx, r); err != nil {
			return err
		}
		r.State, r.PurgedAt, r.UpdatedAt = StatePurged, ts(now), ts(now)
		r.clearDue()
		return s.repo.Save(ctx, r, StatePurging)
```

Delete the `awaitParticipants` field and its uses (2a's `WithPurger(p, await)` becomes `WithPurger(p)`, with `WithParticipants` covering the wait). Update 2a's `TestPurging_AwaitsParticipants`:
- configure `.WithPurger(p).WithParticipants([]string{"wallet"})`;
- assert `purging`, still due (the reconciler owns it), and `p.calls == 0`.

In `main.go`, `.WithPurger(...)` drops its bool and is followed by `.WithParticipants(cfg.ErasureServiceNames())`.

- [ ] **Step 4: Pass** — `go vet ./... && go test ./internal/domain/deletion/ -race -count=1` → `ok`.
- [ ] **Step 5: Commit** — `git add internal/domain/deletion cmd/api/main.go && git commit -m "feat(api): participant acks, reconciler with backoff, stalled state"`

---

### Task 5: Internal endpoints — ack and subject

**Files:** Modify `api/internal/scopes/catalog.go`, `api/internal/scopes/account-scope-manifest.json`, `api/internal/domain/audit/events.go`, `api/internal/handler/deletion.go`, `api/cmd/api/main.go`, `api/internal/handler/testhelpers_test.go`; Create `api/internal/handler/deletion_internal_test.go`.

**Interfaces:**
- Produces `scopes.InternalAccountErasureAck = "internal:account:erasure-ack"` and `scopes.InternalAccountErasureSubject = "internal:account:erasure-subject"` (both `"visibility": "internal"` in the manifest).
- Produces audit event `EventDeletionSubjectRead = "account.deletion.subject_read"`.
- Produces `func (h *DeletionHandler) RegisterInternal(v1 fiber.Router, requireAuth fiber.Handler, serviceForClient func(string) (string, bool), subjectServices []string)`.
- Produces `func (s *deletion.Service) Subject(ctx, requestID string) (cpf string, err error)` (`ErrNotFound` unless `purging`).
- Routes: `POST /v1.0/internal/erasure/ack` (body `erasure.Ack`, `204`), `GET /v1.0/internal/erasure/:request_id/subject` (`200 {"cpf": "..."}`).

- [ ] **Step 1: Failing test** — `api/internal/handler/deletion_internal_test.go`:

```go
package handler_test

import (
	"net/http"
	"testing"

	"gopkg.aoctech.app/account/api/internal/scopes"
)

func TestErasureAck_RequiresServiceTokenOfAParticipant(t *testing.T) {
	ta := newTestApp(t)
	body := map[string]any{"request_id": "nope", "service": "wallet", "result": "done"}

	user := ta.issueToken(t, "u1")
	if resp := ta.doWithToken("POST", "/v1.0/internal/erasure/ack", body, user); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("user token: %d, want 403", resp.StatusCode)
	}
	// issueServiceToken mints azp "dfe"; the test app maps dfe as a participant.
	svc := ta.issueServiceToken(t, []string{scopes.InternalAccountErasureAck})
	if resp := ta.doWithToken("POST", "/v1.0/internal/erasure/ack", body, svc); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ack for unknown request: %d, want 404", resp.StatusCode)
	}
	if resp := ta.doWithToken("GET", "/v1.0/internal/erasure/nope/subject", nil, ta.issueServiceToken(t, []string{scopes.InternalAccountErasureSubject})); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("subject for unknown request: %d, want 404", resp.StatusCode)
	}
}
```

In `testhelpers_test.go`, after the deletion handler registration:

```go
	deletionH.RegisterInternal(v1, middleware.RequireAuth(jwtSvc),
		func(clientID string) (string, bool) { return "dfe", clientID == "dfe" }, []string{"dfe"})
```

Refactor the existing `handler.NewDeletionHandler(...).Register(...)` call into `deletionH := handler.NewDeletionHandler(...)` followed by `deletionH.Register(...)`.

- [ ] **Step 2: Fail** — build FAIL `RegisterInternal undefined`.

- [ ] **Step 3: Implement.**

`scopes/catalog.go` (next to `InternalAccountKYC`):

```go
// InternalAccountErasureAck lets a deletion-saga participant report its result.
const InternalAccountErasureAck = "internal:account:erasure-ack"

// InternalAccountErasureSubject lets a participant read the CPF of a user being
// purged, to match records keyed by CPF (dfe e-CPF certificates). Purging only.
const InternalAccountErasureSubject = "internal:account:erasure-subject"
```

Add both to `account-scope-manifest.json`, copying the shape of the existing `internal:account:kyc` entry (`"visibility": "internal"`, `"status": "active"`), with en/pt-BR descriptions:
- "Report the result of an account-deletion step." / "Informar o resultado de uma etapa de exclusão de conta."
- "Read the CPF of an account being deleted." / "Consultar o CPF de uma conta em exclusão."

`deletion/service.go`:

```go
// Subject returns the CPF of the user of a purging request (saga protocol
// §4.4). ErrNotFound for any other state, so it cannot be used as a lookup.
func (s *Service) Subject(ctx context.Context, requestID string) (string, error) {
	r, err := s.repo.Get(ctx, requestID)
	if err != nil {
		return "", err
	}
	if r.State != StatePurging {
		return "", ErrNotFound
	}
	u, err := s.users.GetByID(ctx, r.UserID)
	if err != nil {
		return "", err
	}
	return u.CPF, nil
}
```

`handler/deletion.go`:

```go
// RegisterInternal mounts the saga endpoints for participants (service tokens
// only). serviceForClient maps the caller's azp to its participant name;
// subjectServices may read the CPF of a purging user.
func (h *DeletionHandler) RegisterInternal(v1 fiber.Router, requireAuth fiber.Handler,
	serviceForClient func(string) (string, bool), subjectServices []string) {
	v1.Post("/internal/erasure/ack", requireAuth, middleware.RequireInternalScope(scopes.InternalAccountErasureAck),
		func(c fiber.Ctx) error { return h.ack(c, serviceForClient) })
	v1.Get("/internal/erasure/:request_id/subject", requireAuth, middleware.RequireInternalScope(scopes.InternalAccountErasureSubject),
		func(c fiber.Ctx) error { return h.subject(c, serviceForClient, subjectServices) })
}

func (h *DeletionHandler) ack(c fiber.Ctx, serviceForClient func(string) (string, bool)) error {
	service, ok := serviceForClient(middleware.GetClientID(c))
	if !ok {
		return apierror.Forbidden("This client is not an erasure participant.", c.Path()).Send(c)
	}
	var ack erasure.Ack
	if err := c.Bind().JSON(&ack); err != nil || ack.RequestID == "" {
		return apierror.InvalidRequest("Body must be an erasure ack with request_id.", c.Path()).Send(c)
	}
	if ack.Service != "" && ack.Service != service {
		return apierror.Forbidden("A participant may only ack for itself.", c.Path()).Send(c)
	}
	switch err := h.svc.Ack(c.Context(), service, ack); {
	case errors.Is(err, deletion.ErrNotFound):
		return apierror.NotFound("deletion request", c.Path()).Send(c)
	case err != nil:
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *DeletionHandler) subject(c fiber.Ctx, serviceForClient func(string) (string, bool), allowed []string) error {
	service, ok := serviceForClient(middleware.GetClientID(c))
	if !ok || !slices.Contains(allowed, service) {
		return apierror.Forbidden("This participant may not read deletion subjects.", c.Path()).Send(c)
	}
	requestID := c.Params("request_id")
	cpf, err := h.svc.Subject(c.Context(), requestID)
	if errors.Is(err, deletion.ErrNotFound) {
		return apierror.NotFound("deletion request", c.Path()).Send(c)
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	recordAuditAnon(c, h.audit, audit.EventDeletionSubjectRead, map[string]string{"request_id": requestID, "service": service})
	return c.JSON(fiber.Map{"cpf": cpf})
}
```

(imports `slices`, `gopkg.aoctech.app/api-commons/erasure`.) Before relying on `recordAuditAnon`, check its signature in `handler/helpers.go`: it records against an anonymous key, which is right here because the person is being erased.

`audit/events.go`: add `EventDeletionSubjectRead = "account.deletion.subject_read"` to the deletion const group.

`main.go`, inside the deletion block:

```go
		deletionH := handler.NewDeletionHandler(deletionSvc, userSvc, auditSvc)
		deletionH.Register(account, v1.Group("/auth"), middleware.RequireClientID(cfg.SelfClientID), middleware.DeletionRequestLimiters(valkeyClient)...)
		deletionH.RegisterInternal(v1, middleware.RequireAuth(jwtSvc), cfg.ErasureServiceForClient, []string{"dfe"})
```

Also wire the eligibility: replace `.WithOwnership(deletion.NewAccountOwnership(orgSvc, oauthClientRepo))` with:

```go
			WithOwnership(deletion.NewCompositeOwnership(
				deletion.NewAccountOwnership(orgSvc, oauthClientRepo),
				erasurepub.NewEligibility(&http.Client{}, cfg.ErasureParticipants, func(aud, scope string) (string, error) {
					now := time.Now().Unix()
					return jwtSvc.SignAccessToken("ctech-account", "", cfg.SelfClientID, []string{scope}, cfg.AppURL, []string{aud}, now, now, nil, "")
				}))).
```

The issuer is `cfg.AppURL`, the same `issuerURL` that `main.go` passes to `handler.NewTokenHandler`. Every participant verifies `iss` against its `CTECH_ISSUER_URL`. **Deployment check:** that value must equal ctech-account's `APP_URL`, otherwise every eligibility call is rejected and every deletion request answers 503.

- [ ] **Step 4: Pass** — `go vet ./... && go test ./... 2>&1 | tail -30` → `ok`.
- [ ] **Step 5: Commit** — `git add internal/scopes internal/domain internal/handler cmd/api/main.go && git commit -m "feat(api): erasure ack and subject endpoints for participants"`

---

### Task 5b: `cpf_hmac` on the internal KYC endpoint (wallet self-exclusion)

ctech-wallet keys a deleted account's self-exclusion by the same CPF keyed hash ctech-account keeps on the tombstone (overview D13; approved by the user 2026-10-08). The wallet never receives the key: ctech-account computes the hash and returns it next to the CPF that `GET /v1.0/internal/kyc/:user_id` already returns to the wallet.

**Files:** Modify `api/internal/handler/kyc.go`, `api/internal/handler/testhelpers_test.go`, `api/cmd/api/main.go`; Test `api/internal/handler/kyc_test.go`.

**Interfaces:** Produces `func (h *KYCHandler) WithCPFMAC(mac func(cpf string) string) *KYCHandler`. The internal GET gains a `cpf_hmac` field, present only when the user has a CPF and a MAC is configured.

- [ ] **Step 1: Failing test** — append to `api/internal/handler/kyc_test.go`:

```go
func TestInternalKYCReturnsCPFHMAC(t *testing.T) {
	ta := newTestApp(t)
	withCPF := ta.registerUser(t, "kyc-hmac@example.com", "Password!123", "Fulano")
	ta.userRepo.byID[withCPF.ID()].CPF = "12345678909"
	noCPF := ta.registerUser(t, "kyc-nocpf@example.com", "Password!123", "Ciclano")
	m2m := ta.issueMachineToken(t, "wallet", []string{scopes.InternalWalletConfirmDeposit})

	var body map[string]any
	readJSON(t, ta.doWithToken(http.MethodGet, "/v1.0/internal/kyc/"+withCPF.ID(), nil, m2m), &body)
	if body["cpf_hmac"] != "mac:12345678909" {
		t.Fatalf("cpf_hmac = %v, want the keyed hash of the CPF", body["cpf_hmac"])
	}
	body = nil
	readJSON(t, ta.doWithToken(http.MethodGet, "/v1.0/internal/kyc/"+noCPF.ID(), nil, m2m), &body)
	if _, ok := body["cpf_hmac"]; ok {
		t.Fatal("a user without CPF must not get a cpf_hmac")
	}
}
```

In `testhelpers_test.go` change `kycH := handler.NewKYCHandler(kycSvc, auditSvc)` to `kycH := handler.NewKYCHandler(kycSvc, auditSvc).WithCPFMAC(func(cpf string) string { return "mac:" + cpf })`.

- [ ] **Step 2: Fail** — `cd api && go test ./internal/handler/ -run TestInternalKYCReturnsCPFHMAC -count=1` → build FAIL `WithCPFMAC undefined`.

- [ ] **Step 3: Implement** — `handler/kyc.go`: add the field `cpfMAC func(string) string` to `KYCHandler`, plus:

```go
// WithCPFMAC enables cpf_hmac on the internal KYC read: the keyed hash
// ctech-wallet uses to carry a self-exclusion across account deletion (D13).
// The key never leaves ctech-account.
func (h *KYCHandler) WithCPFMAC(mac func(cpf string) string) *KYCHandler {
	h.cpfMAC = mac
	return h
}
```

In `internalGet`, build the response map first and add the hash:

```go
	resp := fiber.Map{
		"level":        u.KYCLevel,
		"status":       u.KYCStatus,
		"cpf":          u.CPF,
		"legal_name":   u.LegalName,
		"birth_date":   u.BirthDate,
		"email":        u.Email,
		"phone_number": u.PhoneNumber,
		"address":      u.Address,
	}
	if u.CPF != "" && h.cpfMAC != nil {
		resp["cpf_hmac"] = h.cpfMAC(u.CPF)
	}
	return c.JSON(resp)
```

`main.go`: `kycH := handler.NewKYCHandler(kycSvc, auditSvc).WithCPFMAC(func(cpf string) string { return sealer.MAC("cpf-hmac", cpf) })`. It must use the same label as Phase 2a's tombstone, so both sides compute one value.

- [ ] **Step 4: Pass** — `go vet ./... && go test ./internal/handler/ -count=1` → `ok`.
- [ ] **Step 5: Commit** — `git add internal/handler cmd/api/main.go && git commit -m "feat(api): cpf_hmac on the internal KYC read for wallet self-exclusion"`

Document the new field in `api/ENDPOINTS.md` (internal KYC section) in Task 8.

---

### Task 6: Admin — legal hold, support cancel, redrive

**Files:** Modify `api/internal/domain/deletion/model.go`, `api/internal/domain/deletion/service.go`, `api/internal/handler/deletion.go`, `api/cmd/api/main.go`, `api/internal/domain/audit/events.go`; Test `api/internal/domain/deletion/service_test.go`, `api/internal/handler/deletion_internal_test.go`.

**Interfaces:**
- Produces `Request.LegalHold bool`, `Request.LegalHoldBy string`.
- Produces `ErrHoldNeedsSecondAdmin`.
- Produces service methods:
  - `SetLegalHold(ctx, requestID, adminID string, on bool) error`
  - `SupportCancel(ctx, requestID, adminID string) (*Request, error)`
  - `Redrive(ctx, requestID, service, adminID string) error`
- Produces audit events `account.deletion.legal_hold_on|legal_hold_off|support_cancelled|redriven`.
- Routes under `/v1.0/admin/deletion` (`RequireSupportRole(userSvc, SupportRoleAdmin)`):
  - `POST /:request_id/legal-hold` `{on}`
  - `POST /:request_id/cancel`
  - `POST /:request_id/redrive` `{service}`
- `GET /v1.0/account/deletion` additionally returns `legal_hold` (D9; never the reason).

- [ ] **Step 1: Failing tests** — append to `service_test.go`:

```go
func TestLegalHold_PausesAndNeedsSecondAdmin(t *testing.T) {
	f, p := participantFixture(t)
	r := f.confirm(t)
	if err := f.svc.SetLegalHold(ctx, r.ID, "admin-a", true); err != nil {
		t.Fatalf("hold: %v", err)
	}
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).State != StatePending || p.calls != 0 {
		t.Fatal("a held request must not lock")
	}
	if err := f.svc.SetLegalHold(ctx, r.ID, "admin-a", false); !errors.Is(err, ErrHoldNeedsSecondAdmin) {
		t.Fatalf("same admin lifting the hold: %v", err)
	}
	if err := f.svc.SetLegalHold(ctx, r.ID, "admin-b", false); err != nil {
		t.Fatalf("second admin: %v", err)
	}
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).State != StatePurged {
		t.Fatalf("after the hold is lifted the request proceeds, got %s", f.stored(t, r.ID).State)
	}
}

func TestSupportCancel_AndRedrive(t *testing.T) {
	f, p := participantFixture(t, "wallet")
	r := f.confirm(t)
	if _, err := f.svc.SupportCancel(ctx, r.ID, "admin-a"); err != nil {
		t.Fatalf("SupportCancel: %v", err)
	}
	if f.stored(t, r.ID).State != StateCancelled {
		t.Fatal("support cancel must cancel a pending request")
	}

	f2, p2 := participantFixture(t, "wallet")
	r2 := f2.toPurging(t)
	_ = f2.svc.Ack(ctx, "wallet", erasure.Ack{RequestID: r2.ID, Result: erasure.ResultBlocked})
	f2.svc.ProcessDue(ctx)
	if err := f2.svc.Redrive(ctx, r2.ID, "wallet", "admin-a"); err != nil {
		t.Fatalf("Redrive: %v", err)
	}
	_ = f2.svc.Ack(ctx, "wallet", erasure.Ack{RequestID: r2.ID, Result: erasure.ResultDone})
	f2.svc.ProcessDue(ctx)
	if f2.stored(t, r2.ID).State != StatePurged || p2.calls != 1 {
		t.Fatalf("after redrive and ack: state %s calls %d", f2.stored(t, r2.ID).State, p2.calls)
	}
	_ = p
}
```

Append to `deletion_internal_test.go`:

```go
func TestAdminDeletion_RequiresAdminRole(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "agent@example.com", "Sup3rSecret!", "Ana")
	if resp := ta.doWithToken("POST", "/v1.0/admin/deletion/nope/cancel", nil, ta.issueToken(t, u.ID())); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin: %d, want 403", resp.StatusCode)
	}
}
```

Register the admin routes in the test app next to `RegisterInternal`:

```go
	deletionH.RegisterAdmin(v1.Group("/admin/deletion", middleware.RequireAuth(jwtSvc), middleware.RequireClientID(cfg.SelfClientID),
		middleware.RequireSupportRole(userSvc, userDomain.SupportRoleAdmin)))
```

- [ ] **Step 2: Fail** — build FAIL.

- [ ] **Step 3: Implement.**

`model.go`, on `Request`:

```go
	LegalHold   bool   `dynamodbav:"legal_hold"`
	LegalHoldBy string `dynamodbav:"legal_hold_by,omitempty"` // admin who placed it; another must lift it (R13)
```

`service.go`:

```go
var ErrHoldNeedsSecondAdmin = errors.New("deletion: a legal hold is lifted by a different admin")

// SetLegalHold pauses (or resumes) a request before it locks or purges.
func (s *Service) SetLegalHold(ctx context.Context, requestID, adminID string, on bool) error {
	r, err := s.repo.Get(ctx, requestID)
	if err != nil {
		return err
	}
	if !on && r.LegalHold && r.LegalHoldBy == adminID {
		return ErrHoldNeedsSecondAdmin
	}
	from := r.State
	r.LegalHold, r.UpdatedAt = on, ts(s.now())
	r.LegalHoldBy = ""
	if on {
		r.LegalHoldBy = adminID
	}
	if !on && (r.State == StatePending || r.State == StatePurging) {
		r.setDue(s.now()) // resume promptly
	}
	return s.repo.Save(ctx, r, from)
}

// SupportCancel cancels on the user's behalf (R1: support is the fallback
// when the user lost every e-mail). Same rules as a link cancel.
func (s *Service) SupportCancel(ctx context.Context, requestID, adminID string) (*Request, error) {
	r, err := s.repo.Get(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if r.State != StatePending && r.State != StateBlocked {
		return nil, ErrNotCancellable
	}
	return s.cancel(ctx, r)
}

// Redrive puts a stalled request back to purging after support resolved the
// participant's blocker: that participant is dispatched again.
func (s *Service) Redrive(ctx context.Context, requestID, service, adminID string) error {
	r, err := s.repo.Get(ctx, requestID)
	if err != nil {
		return err
	}
	sts, err := s.repo.ListServices(ctx, requestID)
	if err != nil {
		return err
	}
	for _, st := range sts {
		if st.Service == service && st.Status == ServiceBlocked {
			st.Status, st.Blockers, st.AckedAt = ServiceDispatched, nil, ""
			st.Attempts, st.LastDispatchAt = 0, ts(time.Time{}) // due immediately
			if err := s.repo.SaveService(ctx, st, ServiceBlocked); err != nil {
				return err
			}
		}
	}
	if r.State == StateStalled {
		r.State, r.UpdatedAt = StatePurging, ts(s.now())
		r.setDue(s.now())
		return s.repo.Save(ctx, r, StateStalled)
	}
	return nil
}
```

Refactor the existing `Cancel`: keep the token check in `Cancel`, then delegate to a shared `s.cancel(ctx, r)` holding the current state-change and `finishCancel` logic, so `SupportCancel` reuses it.

With `Attempts == 0`, `nextDispatch` indexes `ackBackoff[-1]`. Make it `i := max(0, min(st.Attempts-1, len(ackBackoff)-1))` and treat `Attempts == 0` as "due now" (`if st.Attempts == 0 { return time.Time{} }` at the top).

In `step`, honour the hold:
- `StatePending`, grace-end case: `if r.LegalHold { r.setDue(now.Add(time.Hour)); return s.repo.Save(ctx, r, StatePending) }` before assessing.
- `StatePurging`: the same with `StatePurging` before reconciling.

`handler/deletion.go`:

```go
func (h *DeletionHandler) RegisterAdmin(admin fiber.Router) {
	admin.Post("/:request_id/legal-hold", h.adminLegalHold)
	admin.Post("/:request_id/cancel", h.adminCancel)
	admin.Post("/:request_id/redrive", h.adminRedrive)
}
```

Each handler:
- reads `middleware.GetUserID(c)` as the admin;
- calls the service method;
- maps `ErrNotFound` → 404, `ErrNotCancellable` → 409, `ErrHoldNeedsSecondAdmin` → 409 ("A different admin must lift this hold.");
- records the audit event with `request_id` (and `service` for redrive) against the admin's user id;
- returns `204`.

`legal-hold` takes `{"on": bool}` (validate with `parseBody` and a struct `{ On *bool `json:"on" validate:"required"` }`); `redrive` takes `{"service": string}` (required).

In `status`, add `"legal_hold": r.LegalHold` to the response.

`main.go`:

```go
		deletionH.RegisterAdmin(v1.Group("/admin/deletion", adminAuth[0], adminAuth[1],
			middleware.RequireSupportRole(userSvc, userDomain.SupportRoleAdmin)))
```

`audit/events.go`: `EventDeletionLegalHoldOn = "account.deletion.legal_hold_on"`, `EventDeletionLegalHoldOff = "account.deletion.legal_hold_off"`, `EventDeletionSupportCancelled = "account.deletion.support_cancelled"`, `EventDeletionRedriven = "account.deletion.redriven"`.

- [ ] **Step 4: Pass** — `go vet ./... && go test ./... 2>&1 | tail -30` → `ok`.
- [ ] **Step 5: Commit** — `git add internal cmd && git commit -m "feat(api): deletion legal hold, support cancel and redrive"`

---

### Task 7: Infrastructure

**Files:** Modify `cdk/lib/api-stack.ts`.

- [ ] **Step 1:** In the `ssmEnvArgs` list, add `` `ERASURE_PARTICIPANTS=/ctech-account/${environment}/erasure-participants` ``. The parameter is created by the operator once participants are live, with the JSON from the Global Constraints. Until it exists the variable is empty, and there are no participants.
- [ ] **Step 2:** `cd cdk && npx tsc --noEmit && npx jest` → pass.
- [ ] **Step 3: Commit** — `git add cdk && git commit -m "feat(cdk): ERASURE_PARTICIPANTS from SSM"`

---

### Task 8: Documentation (this repo and ctech-go-common)

- [ ] **`api/ENDPOINTS.md`**:
  - the internal routes (`ack`, `subject`) with scopes and bodies;
  - `cpf_hmac` on `GET /v1.0/internal/kyc/:user_id` (Task 5b);
  - the admin routes;
  - `legal_hold` in `GET /account/deletion`;
  - `503` when a participant cannot be reached.
- [ ] **`README.md`**:
  - `ERASURE_PARTICIPANTS` (JSON shape; replaces `ERASURE_SERVICES`);
  - the operator steps per participant: grant `internal:account:erasure-ack` to its confidential client (and `internal:account:erasure-subject` to dfe), add it to the SSM JSON, subscribe its queue to the topic with `FilterPolicy {"services":["<name>"]}`;
  - the `stalled` state and the redrive flow.
- [ ] **Backup-restore runbook** (README "Account deletion"): after restoring any participant table from PITR, list purged requests with:

  `aws dynamodb query --table-name {env}_account_deletion_requests --index-name gsi_state_due ...`

  This does **not** work: purged requests are not in the due index. Document instead a `Scan` with `FilterExpression request_state = :purged AND purged_at >= :restore_point` on the requests table (operator-only, rare). Then re-publish `user.erase` for each one with the admin redrive or by hand.
- [ ] **Specs:**
  - saga protocol §3/§4: scope names (R10);
  - SNS filter on the `services` message attribute, not on the body;
  - §6 status rows `SVC#{service}` in the same table;
  - §8 runbook via scan (R12).
- [ ] **ctech-go-common** `README.md` "Account erasure":
  - step 1: filter policy on the `services` **attribute** (drop "scope `MessageBody`");
  - the ack scope name `internal:account:erasure-ack`;
  - the eligibility scope name `internal:{service}:erasure-eligibility`.

  This is a separate commit and push in that repo; ask the user before pushing.
- [ ] **`PLAN.md`**: check Phase 3 (account side). Add "single-service unlink" as its own pending item.
- [ ] **Commit** — `git add README.md PLAN.md api/ENDPOINTS.md docs/specs && git commit -m "docs: account deletion phase 3"`

## Cross-project impact

- **wallet, dfe, billing, poker:** each must, in its own plan,
  - expose `GET /internal/erasure/eligibility/{sub}` accepting account-minted tokens with `internal:{service}:erasure-eligibility`;
  - run the `erasure.Consumer` against its own `{env}_*_erasure_state` table and SQS queue;
  - obtain a confidential client with `internal:account:erasure-ack`;
  - add `jwtverify.WithRevocation`.
- **ctech-go-common:** README only (filter scope, scope names).
- **cdk:** one new SSM-backed env var. Queues and subscriptions belong to each participant's stack.
