package handler

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/middleware"
)

// OrganizationHandler exposes the workspace-management routes.
//
// It holds the user service as well as the organization one because acceptance
// has to be checked against the account's stored address; see accept.
type OrganizationHandler struct {
	svc   *organization.Service
	users *user.Service
}

func NewOrganizationHandler(svc *organization.Service, users *user.Service) *OrganizationHandler {
	return &OrganizationHandler{svc: svc, users: users}
}

// Register mounts the routes on a group that already carries RequireAuth and
// RequireClientID: managing who is in an organization is a first-party action,
// not something a delegated token does on a user's behalf.
func (h *OrganizationHandler) Register(r fiber.Router) {
	r.Post("/organizations", h.create)
	r.Get("/organizations", h.listMine)
	r.Post("/invitations/accept", h.accept)

	scoped := func(floor string) fiber.Handler { return middleware.RequireOrgRole(h.svc, floor) }
	r.Get("/organizations/:id", scoped(organization.RoleViewer), h.get)
	r.Patch("/organizations/:id", scoped(organization.RoleAdmin), h.rename)
	r.Get("/organizations/:id/members", scoped(organization.RoleViewer), h.listMembers)
	r.Patch("/organizations/:id/members/:user_id", scoped(organization.RoleAdmin), h.setRole)
	r.Delete("/organizations/:id/members/:user_id", scoped(organization.RoleAdmin), h.removeMember)
	r.Get("/organizations/:id/invitations", scoped(organization.RoleAdmin), h.listInvitations)
	r.Post("/organizations/:id/invitations", scoped(organization.RoleAdmin), h.invite)
	r.Delete("/organizations/:id/invitations/:email", scoped(organization.RoleAdmin), h.revokeInvitation)
	r.Post("/organizations/:id/transfer", scoped(organization.RoleOwner), h.transfer)
}

type organizationRequest struct {
	DisplayName string `json:"display_name" validate:"required,max=120"`
}

type inviteRequest struct {
	Email string `json:"email" validate:"required,email"`
	Role  string `json:"role" validate:"required,oneof=admin member viewer"`
}

type roleRequest struct {
	Role string `json:"role" validate:"required,oneof=admin member viewer"`
}

type transferRequest struct {
	UserID string `json:"user_id" validate:"required"`
}

type acceptRequest struct {
	Token string `json:"token" validate:"required"`
}

type organizationDTO struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	OwnerUserID string `json:"owner_user_id"`
	Role        string `json:"role,omitempty"`
	CreatedAt   string `json:"created_at"`
}

type membershipDTO struct {
	OrganizationID string `json:"organization_id"`
	UserID         string `json:"user_id"`
	Role           string `json:"role"`
	CreatedAt      string `json:"created_at"`
}

type invitationDTO struct {
	Email     string `json:"email"`
	Role      string `json:"role"`
	InvitedBy string `json:"invited_by"`
	ExpiresAt string `json:"expires_at"`
}

func (h *OrganizationHandler) create(c fiber.Ctx) error {
	var req organizationRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	org, err := h.svc.Create(c.Context(), middleware.GetUserID(c), req.DisplayName)
	if err != nil {
		return organizationProblem(c, err)
	}
	return c.Status(http.StatusCreated).JSON(organizationDTO{
		ID: org.ID, DisplayName: org.DisplayName, OwnerUserID: org.OwnerUserID,
		Role: organization.RoleOwner, CreatedAt: org.CreatedAt.Format(time.RFC3339),
	})
}

// listMine is what a console calls on sign-in. It returns memberships rather
// than organizations because the role is the half that decides what the UI may
// even offer.
func (h *OrganizationHandler) listMine(c fiber.Ctx) error {
	memberships, err := h.svc.ListForUser(c.Context(), middleware.GetUserID(c))
	if err != nil {
		return organizationProblem(c, err)
	}
	out := make([]membershipDTO, 0, len(memberships))
	for _, m := range memberships {
		out = append(out, membershipDTO{
			OrganizationID: m.OrganizationID, UserID: m.UserID,
			Role: m.Role, CreatedAt: m.CreatedAt.Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"organizations": out})
}

func (h *OrganizationHandler) get(c fiber.Ctx) error {
	org, err := h.svc.Get(c.Context(), middleware.GetOrgID(c), middleware.GetUserID(c))
	if err != nil {
		return organizationProblem(c, err)
	}
	return c.JSON(organizationDTO{
		ID: org.ID, DisplayName: org.DisplayName, OwnerUserID: org.OwnerUserID,
		Role: middleware.GetOrgRole(c), CreatedAt: org.CreatedAt.Format(time.RFC3339),
	})
}

func (h *OrganizationHandler) rename(c fiber.Ctx) error {
	var req organizationRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	if err := h.svc.Rename(c.Context(), middleware.GetOrgID(c), middleware.GetUserID(c), req.DisplayName); err != nil {
		return organizationProblem(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *OrganizationHandler) listMembers(c fiber.Ctx) error {
	members, err := h.svc.ListMembers(c.Context(), middleware.GetOrgID(c), middleware.GetUserID(c))
	if err != nil {
		return organizationProblem(c, err)
	}
	out := make([]membershipDTO, 0, len(members))
	for _, m := range members {
		out = append(out, membershipDTO{
			OrganizationID: m.OrganizationID, UserID: m.UserID,
			Role: m.Role, CreatedAt: m.CreatedAt.Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"members": out})
}

func (h *OrganizationHandler) setRole(c fiber.Ctx) error {
	var req roleRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	err := h.svc.SetRole(c.Context(), middleware.GetOrgID(c), middleware.GetUserID(c), c.Params("user_id"), req.Role)
	if err != nil {
		return organizationProblem(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *OrganizationHandler) removeMember(c fiber.Ctx) error {
	err := h.svc.Remove(c.Context(), middleware.GetOrgID(c), middleware.GetUserID(c), c.Params("user_id"))
	if err != nil {
		return organizationProblem(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *OrganizationHandler) listInvitations(c fiber.Ctx) error {
	invitations, err := h.svc.ListInvitations(c.Context(), middleware.GetOrgID(c), middleware.GetUserID(c))
	if err != nil {
		return organizationProblem(c, err)
	}
	out := make([]invitationDTO, 0, len(invitations))
	for _, inv := range invitations {
		// TokenHash is deliberately absent: the pending list is who was
		// invited, not a set of keys.
		out = append(out, invitationDTO{
			Email: inv.Email, Role: inv.Role, InvitedBy: inv.InvitedBy,
			ExpiresAt: inv.ExpiresAt.Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"invitations": out})
}

func (h *OrganizationHandler) invite(c fiber.Ctx) error {
	var req inviteRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	token, err := h.svc.Invite(c.Context(), middleware.GetOrgID(c), middleware.GetUserID(c), req.Email, req.Role)
	if err != nil {
		return organizationProblem(c, err)
	}
	// The token is returned once, to the admin who created it, so they can send
	// it. It is never readable again — nothing stores it.
	return c.Status(http.StatusCreated).JSON(fiber.Map{"token": token, "email": organization.NormalizeEmail(req.Email), "role": req.Role})
}

func (h *OrganizationHandler) revokeInvitation(c fiber.Ctx) error {
	email, err := url.PathUnescape(c.Params("email"))
	if err != nil {
		return apierror.InvalidRequest("The invitation address is not a valid path segment.", c.Path()).Send(c)
	}
	if err := h.svc.RevokeInvitation(c.Context(), middleware.GetOrgID(c), middleware.GetUserID(c), email); err != nil {
		return organizationProblem(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *OrganizationHandler) transfer(c fiber.Ctx) error {
	var req transferRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	if err := h.svc.Transfer(c.Context(), middleware.GetOrgID(c), middleware.GetUserID(c), req.UserID); err != nil {
		return organizationProblem(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

// accept redeems an invitation token.
//
// The address it is checked against is read from the account record, never
// taken from the request body. A body-supplied address would make the token a
// bearer capability: whoever found the link could name the invited address and
// join. An unverified address is refused for the same reason — an unverified
// address is a claim, not evidence.
func (h *OrganizationHandler) accept(c fiber.Ctx) error {
	var req acceptRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	u, err := h.users.GetByID(c.Context(), middleware.GetUserID(c))
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return apierror.Unauthorized("Sign in to accept an invitation.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	if !u.EmailVerified {
		return apierror.Forbidden("Verify your e-mail address before accepting an invitation.", c.Path()).Send(c)
	}
	m, err := h.svc.Accept(c.Context(), req.Token, middleware.GetUserID(c), u.Email)
	if err != nil {
		return organizationProblem(c, err)
	}
	return c.Status(http.StatusCreated).JSON(membershipDTO{
		OrganizationID: m.OrganizationID, UserID: m.UserID,
		Role: m.Role, CreatedAt: m.CreatedAt.Format(time.RFC3339),
	})
}

// organizationProblem is the single mapping from domain error to HTTP status.
// Membership failures answer 403 with one message, so the API cannot be used to
// discover which organizations exist.
func organizationProblem(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, organization.ErrInvalidName):
		return apierror.ValidationFailed("The organization name is required and must be at most 120 characters.", c.Path()).Send(c)
	case errors.Is(err, organization.ErrNotGrantable):
		return apierror.ValidationFailed("That role cannot be assigned. Ownership moves through transfer.", c.Path()).Send(c)
	case errors.Is(err, organization.ErrNotAMember), errors.Is(err, organization.ErrForbidden), errors.Is(err, organization.ErrNotFound):
		return apierror.Forbidden("You do not have access to this organization.", c.Path()).Send(c)
	case errors.Is(err, organization.ErrInvitationInvalid), errors.Is(err, organization.ErrWrongInvitee):
		// One answer for expired, unknown, already-used and addressed to
		// somebody else: each distinction is a hint to whoever is guessing.
		return apierror.Forbidden("This invitation is no longer valid.", c.Path()).Send(c)
	case errors.Is(err, organization.ErrAlreadyMember):
		return apierror.Conflict("That person is already in this organization.", c.Path()).Send(c)
	default:
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
}
