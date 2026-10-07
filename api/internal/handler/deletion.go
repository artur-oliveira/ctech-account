package handler

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/deletion"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

type DeletionHandler struct {
	svc   *deletion.Service
	users *user.Service
	audit *audit.Service
}

func NewDeletionHandler(svc *deletion.Service, users *user.Service, auditSvc *audit.Service) *DeletionHandler {
	return &DeletionHandler{svc: svc, users: users, audit: auditSvc}
}

// Register mounts the signed-in routes on account and the e-mail-link routes
// on auth (public: the link token is the credential). selfOnly restricts the
// request to this service's own frontend (spec §4): a third-party app granted
// the scope must not be able to start a deletion.
func (h *DeletionHandler) Register(account, auth fiber.Router, selfOnly, requestLimiter fiber.Handler) {
	account.Post("/deletion", middleware.RequireScope(scopes.AccountDeletionWrite), selfOnly, requestLimiter, h.request)
	account.Get("/deletion", middleware.RequireScope(scopes.AccountProfileRead), h.status)
	auth.Post("/deletion/confirm", h.confirm)
	auth.Post("/deletion/cancel", h.cancel)
}

type deletionRequestBody struct {
	ConfirmationPhrase string `json:"confirmation_phrase" validate:"required"`
	Password           string `json:"password"`
}

type deletionLinkBody struct {
	RequestID string `json:"request_id" validate:"required"`
	Token     string `json:"token"      validate:"required"`
}

func (h *DeletionHandler) request(c fiber.Ctx) error {
	var req deletionRequestBody
	if err := parseBody(c, &req); err != nil {
		return err
	}
	if req.ConfirmationPhrase != deletion.ConfirmationPhrase {
		return apierror.InvalidRequest(`confirmation_phrase must be exactly "`+deletion.ConfirmationPhrase+`".`, c.Path())
	}
	userID := middleware.GetUserID(c)
	if err := h.verifyIdentity(c, userID, req.Password); err != nil {
		return err
	}
	r, err := h.svc.Request(c.Context(), userID)
	if errors.Is(err, deletion.ErrOpenRequest) {
		return apierror.Conflict("A deletion request is already in progress for this account.", c.Path())
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	recordAudit(c, h.audit, userID, audit.EventDeletionRequested, map[string]string{"request_id": r.ID})
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"request_id": r.ID, "state": r.State, "confirm_by": r.ConfirmBy})
}

// verifyIdentity (ruling R2): a recent MFA proof, or the account password.
// A password-less (Google-only) account relies on the e-mail confirmation.
func (h *DeletionHandler) verifyIdentity(c fiber.Ctx, userID, password string) error {
	if last := middleware.GetLastMFAAt(c); last != 0 && time.Since(time.Unix(last, 0)) <= middleware.StepUpMaxAge {
		return nil
	}
	hasPassword, err := h.users.HasPassword(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	if !hasPassword {
		return nil
	}
	if password == "" {
		return apierror.StepUpRequired(middleware.StepUpMaxAge, c.Path())
	}
	if err := h.users.CheckPassword(c.Context(), userID, password); err != nil {
		if errors.Is(err, user.ErrInvalidCredentials) {
			return apierror.InvalidCredentials(c.Path())
		}
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	return nil
}

func (h *DeletionHandler) status(c fiber.Ctx) error {
	r, err := h.svc.Status(c.Context(), middleware.GetUserID(c))
	if errors.Is(err, deletion.ErrNotFound) {
		return apierror.NotFound("deletion request", c.Path())
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	return c.JSON(fiber.Map{
		"request_id": r.ID, "state": r.State, "requested_at": r.RequestedAt,
		"confirm_by": r.ConfirmBy, "grace_until": r.GraceUntil,
	})
}

func (h *DeletionHandler) confirm(c fiber.Ctx) error {
	var req deletionLinkBody
	if err := parseBody(c, &req); err != nil {
		return err
	}
	r, err := h.svc.Confirm(c.Context(), req.RequestID, req.Token)
	if errors.Is(err, deletion.ErrInvalidToken) {
		return apierror.InvalidToken("This confirmation link is invalid or has expired.", c.Path())
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	recordAudit(c, h.audit, r.UserID, audit.EventDeletionConfirmed, map[string]string{"request_id": r.ID})
	return c.JSON(fiber.Map{"request_id": r.ID, "state": r.State, "grace_until": r.GraceUntil})
}

func (h *DeletionHandler) cancel(c fiber.Ctx) error {
	var req deletionLinkBody
	if err := parseBody(c, &req); err != nil {
		return err
	}
	r, err := h.svc.Cancel(c.Context(), req.RequestID, req.Token)
	switch {
	case errors.Is(err, deletion.ErrInvalidToken):
		return apierror.InvalidToken("This cancel link is invalid or has expired.", c.Path())
	case errors.Is(err, deletion.ErrNotCancellable):
		return apierror.Conflict("This deletion can no longer be cancelled.", c.Path())
	case err != nil:
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	recordAudit(c, h.audit, r.UserID, audit.EventDeletionCancelled, map[string]string{"request_id": r.ID})
	return c.JSON(fiber.Map{"request_id": r.ID, "state": r.State})
}
