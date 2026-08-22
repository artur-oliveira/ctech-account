package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

type supportUserRepo struct{ user *user.User }

func (r supportUserRepo) GetByID(_ context.Context, id string) (*user.User, error) {
	if r.user == nil || r.user.ID() != id {
		return nil, user.ErrNotFound
	}
	return r.user, nil
}
func (supportUserRepo) GetByEmail(context.Context, string) (*user.User, error) {
	return nil, user.ErrNotFound
}
func (supportUserRepo) Create(context.Context, *user.User) error             { return nil }
func (supportUserRepo) Update(context.Context, string, map[string]any) error { return nil }

func TestRequireSupportRole(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, role, minimum string
		want                int
	}{
		{"rejects below minimum", user.SupportRoleAgent, user.SupportRoleAdmin, http.StatusForbidden},
		{"allows at minimum", user.SupportRoleManager, user.SupportRoleManager, http.StatusOK},
		{"allows above minimum", user.SupportRoleAdmin, user.SupportRoleAgent, http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := user.NewService(supportUserRepo{user: &user.User{PK: user.BuildPK("user-1"), SupportRole: tt.role}})
			app := fiber.New()
			app.Get("/x", func(c fiber.Ctx) error { c.Locals(LocalUserID, "user-1"); return c.Next() }, RequireSupportRole(svc, tt.minimum), func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}
