package apierror_test

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/account/api/internal/apierror"
)

// The JSON the UI and OAuth clients consume; captured from the pre-migration
// apierror implementation (verified identical across every constructor).
func TestWireFormat(t *testing.T) {
	const i = "/x"
	cases := []struct {
		name   string
		p      *apierror.Problem
		status int
		retry  string
		want   map[string]any
	}{
		{"oauth", apierror.InvalidGrant("bad code", i), 400, "", map[string]any{
			"type": "https://accounts.aoctech.app/problems/invalid-grant", "title": "Invalid Grant", "status": 400.0,
			"detail": "bad code", "instance": i, "error": "invalid_grant", "error_description": "bad code"}},
		{"rate limit", apierror.TooManyRequests("slow", i, 90*time.Second), 429, "90", map[string]any{
			"type": "https://accounts.aoctech.app/problems/too-many-requests", "title": "Too Many Requests", "status": 429.0,
			"detail": "slow", "instance": i, "retry_after_seconds": 90.0}},
		{"step-up", apierror.StepUpRequired(5*time.Minute, i), 403, "", map[string]any{
			"type": "https://accounts.aoctech.app/problems/step-up-required", "title": "Step-up Authentication Required", "status": 403.0,
			"detail": "This operation requires recent multi-factor authentication.", "instance": i, "max_age_seconds": 300.0}},
		{"plan limit zero", apierror.WithPlanUsage(apierror.PlanLimit("space", "full", i), 0, 0, "free"), 402, "", map[string]any{
			"type": "https://accounts.aoctech.app/problems/plan-limit", "title": "Plan Limit Reached", "status": 402.0,
			"detail": "full", "instance": i, "code": "plan_limit", "resource": "space", "limit": 0.0, "used": 0.0, "plan": "free"}},
		{"blockers", apierror.DeletionBlocked([]string{"a"}, i), 409, "", map[string]any{
			"type": "https://accounts.aoctech.app/problems/deletion-blocked", "title": "Deletion Blocked", "status": 409.0,
			"detail": "Resolve the listed items before deleting this account.", "instance": i, "blockers": []any{"a"}}},
		{"scope", apierror.InsufficientScope("a:b", i), 403, "", map[string]any{
			"type": "https://accounts.aoctech.app/problems/insufficient-scope", "title": "Insufficient Scope", "status": 403.0,
			"detail": "The access token does not include the required permission.", "instance": i, "required_scope": "a:b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/", func(ctx fiber.Ctx) error { return c.p.Send(ctx) })
			r, err := app.Test(httptest.NewRequest("GET", "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(r.Body)
			got := map[string]any{}
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			if r.StatusCode != c.status || r.Header.Get("Content-Type") != apierror.ContentType || r.Header.Get("Retry-After") != c.retry || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("status=%d ct=%q retry=%q body=%v", r.StatusCode, r.Header.Get("Content-Type"), r.Header.Get("Retry-After"), got)
			}
		})
	}
}
