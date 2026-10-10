package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
)

type stubLimits struct {
	quotas map[string]orgDomain.Quotas
	err    error
}

func (s *stubLimits) Quotas(_ context.Context, owner string) (orgDomain.Quotas, error) {
	if s.err != nil {
		return orgDomain.Quotas{}, s.err
	}
	if q, ok := s.quotas[owner]; ok {
		return q, nil
	}
	return orgDomain.Quotas{Plan: "free", Spaces: 1, PeoplePerSpace: 1}, nil
}
func (s *stubLimits) LevelsChanged(context.Context, string)             {}
func (s *stubLimits) ScheduleLevels(context.Context, string, time.Time) {}

func newLimitedOrgTestApp(t *testing.T, limits *stubLimits) *orgTestApp {
	t.Helper()
	return newOrgTestAppWith(t, func(svc *orgDomain.Service, repo *memOrgRepo) {
		svc.WithPlanLimits(limits, repo)
	})
}

func decodeMap(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestASecondSpaceOnFreeIs402WithTheNumbers(t *testing.T) {
	a := newLimitedOrgTestApp(t, &stubLimits{})
	owner := a.registerUser(t, "plan-402@example.com", "Sup3rSecret!pass", "Dono")
	tok := a.issueToken(t, owner.ID())
	create := `{"display_name":"Casa","kind":"personal"}`
	if resp := a.do(t, http.MethodPost, "/v1.0/organizations", tok, create); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first space: %d", resp.StatusCode)
	}
	resp := a.do(t, http.MethodPost, "/v1.0/organizations", tok, create)
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	if body["code"] != "plan_limit" || body["resource"] != "spaces" || body["limit"] != float64(1) ||
		body["used"] != float64(1) || body["plan"] != "free" || body["type"] != "https://accounts.aoctech.app/problems/plan-limit" {
		t.Fatalf("body = %v", body)
	}
}

// A limit of 0 (malformed catalogue) must still say limit 0, not omit it.
func TestAZeroLimitIsStillInTheBody(t *testing.T) {
	a := newLimitedOrgTestApp(t, &stubLimits{quotas: map[string]orgDomain.Quotas{}})
	owner := a.registerUser(t, "plan-zero@example.com", "Sup3rSecret!pass", "Dono")
	a.svc.WithPlanLimits(&stubLimits{quotas: map[string]orgDomain.Quotas{owner.ID(): {Plan: "free"}}}, a.repo)
	resp := a.do(t, http.MethodPost, "/v1.0/organizations", a.issueToken(t, owner.ID()), `{"display_name":"Casa","kind":"personal"}`)
	body := decodeMap(t, resp)
	if v, ok := body["limit"]; !ok || v != float64(0) {
		t.Fatalf("limit = %v (present %v)", v, ok)
	}
}

// Spec test 5 at the route / Review Focus 3.
func TestOrganizationsIgnoreBillingEntirely(t *testing.T) {
	a := newLimitedOrgTestApp(t, &stubLimits{err: fmt.Errorf("%w: down", orgDomain.ErrPlanUnavailable)})
	owner := a.registerUser(t, "plan-org@example.com", "Sup3rSecret!pass", "Dono")
	tok := a.issueToken(t, owner.ID())

	resp := a.do(t, http.MethodPost, "/v1.0/organizations", tok, `{"display_name":"Casa","kind":"personal"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("space with billing down: %d", resp.StatusCode)
	}
	if body := decodeMap(t, resp); body["code"] != "plan_unavailable" {
		t.Fatalf("body = %v", body)
	}
	if len(a.repo.orgs) != 0 {
		t.Fatal("a space was written with billing down")
	}

	resp = a.do(t, http.MethodPost, "/v1.0/organizations", tok, `{"display_name":"CTech"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("organization with billing down: %d (%s)", resp.StatusCode, bodyString(resp))
	}
	var org struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&org)
	if resp := a.do(t, http.MethodPost, "/v1.0/organizations/"+org.ID+"/invitations", tok, `{"email":"x@example.com","role":"member"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("organization invite with billing down: %d", resp.StatusCode)
	}
}

// Review Focus 4.
func TestATransferRefusalRevealsNothingAboutTheOtherPlan(t *testing.T) {
	limits := &stubLimits{quotas: map[string]orgDomain.Quotas{}}
	a := newLimitedOrgTestApp(t, limits)
	owner := a.registerUser(t, "plan-from@example.com", "Sup3rSecret!pass", "Dono")
	to := a.registerUser(t, "plan-to@example.com", "Sup3rSecret!pass", "Bia")
	limits.quotas[owner.ID()] = orgDomain.Quotas{Plan: "pro", Spaces: 10, PeoplePerSpace: 10}
	ctx := context.Background()
	space, err := a.svc.CreateOfKind(ctx, orgDomain.KindPersonal, owner.ID(), "Dono", "Casa")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.svc.CreateOfKind(ctx, orgDomain.KindPersonal, to.ID(), "Bia", "Dela"); err != nil {
		t.Fatal(err)
	}
	if err := a.repo.PutMembership(ctx, &orgDomain.Membership{OrganizationID: space.ID, UserID: to.ID(), Role: orgDomain.RoleMember, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	resp := a.do(t, http.MethodPost, "/v1.0/organizations/"+space.ID+"/transfer", a.issueToken(t, owner.ID()), `{"user_id":"`+to.ID()+`"}`)
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	if body["code"] != "plan_limit" || body["resource"] != "spaces" {
		t.Fatalf("body = %v", body)
	}
	for _, key := range []string{"plan", "limit", "used"} {
		if _, present := body[key]; present {
			t.Fatalf("the refusal carries the other person's %s: %v", key, body)
		}
	}
}

func TestPlanUsage(t *testing.T) {
	limits := &stubLimits{quotas: map[string]orgDomain.Quotas{}}
	a := newLimitedOrgTestApp(t, limits)
	owner := a.registerUser(t, "plan-usage@example.com", "Sup3rSecret!pass", "Dono")
	limits.quotas[owner.ID()] = orgDomain.Quotas{Plan: "basic", Spaces: 3, PeoplePerSpace: 5}
	tok := a.issueToken(t, owner.ID())
	ctx := context.Background()
	space, _ := a.svc.CreateOfKind(ctx, orgDomain.KindPersonal, owner.ID(), "Dono", "Casa")
	_, _ = a.svc.Invite(ctx, space.ID, owner.ID(), "b@example.com", orgDomain.RoleMember, nil)

	resp := a.do(t, http.MethodGet, "/v1.0/organizations/"+space.ID+"/plan-usage", tok, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%s)", resp.StatusCode, bodyString(resp))
	}
	body := decodeMap(t, resp)
	if body["people"] != float64(0) || body["pending_invitations"] != float64(1) || body["limit"] != float64(5) || body["plan"] != "basic" {
		t.Fatalf("body = %v", body)
	}

	org, _ := a.svc.Create(ctx, owner.ID(), "Dono", "CTech")
	if resp := a.do(t, http.MethodGet, "/v1.0/organizations/"+org.ID+"/plan-usage", tok, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("organization: %d", resp.StatusCode)
	}
	limits.err = fmt.Errorf("%w: down", orgDomain.ErrPlanUnavailable)
	if resp := a.do(t, http.MethodGet, "/v1.0/organizations/"+space.ID+"/plan-usage", tok, ""); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("billing down: %d", resp.StatusCode)
	}
}
