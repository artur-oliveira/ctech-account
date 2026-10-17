package handler

import (
	"errors"
	"slices"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/deletion"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/scopes"
	"gopkg.aoctech.app/api-commons/erasure"
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
func (h *DeletionHandler) Register(account, auth fiber.Router, selfOnly fiber.Handler, limiters ...fiber.Handler) {
	chain := []any{selfOnly}
	for _, l := range limiters {
		chain = append(chain, l)
	}
	account.Post("/deletion", middleware.RequireScope(scopes.AccountDeletionWrite), append(chain, h.request)...)
	account.Get("/deletion", middleware.RequireScope(scopes.AccountProfileRead), h.status)
	account.Get("/deletion/services", middleware.RequireScope(scopes.AccountProfileRead), h.services)
	auth.Post("/deletion/confirm", h.confirm)
	auth.Post("/deletion/cancel", h.cancel)
}

type deletionRequestBody struct {
	ConfirmationPhrase string `json:"confirmation_phrase" validate:"required"`
	Password           string `json:"password"`
	// Scope is "account" (default) or "service" (leave one product, which needs
	// Service). docs/specs/2026-10-17-service-unlink.md.
	Scope   string `json:"scope"   validate:"omitempty,oneof=account service"`
	Service string `json:"service" validate:"required_if=Scope service"`
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
	scope := deletion.ScopeAccount
	phrase := deletion.ConfirmationPhrase
	if req.Scope == deletion.ScopeService {
		scope, phrase = deletion.ScopeService, deletion.ServicePhrase(req.Service)
	}
	if req.ConfirmationPhrase != phrase {
		return apierror.InvalidRequest(`confirmation_phrase must be exactly "`+phrase+`".`, c.Path())
	}
	userID := middleware.GetUserID(c)
	if err := h.verifyIdentity(c, userID, req.Password); err != nil {
		// Sent here, not returned: the rate limiters read the response status.
		if p, ok := errors.AsType[*apierror.Problem](err); ok {
			return p.Send(c)
		}
		return err
	}
	var r *deletion.Request
	var err error
	if scope == deletion.ScopeService {
		r, err = h.svc.RequestService(c.Context(), userID, req.Service)
	} else {
		r, err = h.svc.Request(c.Context(), userID)
	}
	if errors.Is(err, deletion.ErrUnknownService) {
		return apierror.UnknownService("This product cannot be left on its own.", c.Path())
	}
	var blocked *deletion.BlockedError
	if errors.As(err, &blocked) {
		return apierror.DeletionBlocked(blocked.Blockers, c.Path()).Send(c)
	}
	if errors.Is(err, deletion.ErrOpenRequest) {
		return apierror.Conflict("A deletion request is already in progress for this account.", c.Path())
	}
	if errors.Is(err, deletion.ErrEligibilityUnavailable) {
		return apierror.ServiceUnavailable("Could not check every CTech product right now. Try again in a few minutes.", c.Path()).WithCause(err)
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	recordAudit(c, h.audit, userID, audit.EventDeletionRequested, map[string]string{"request_id": r.ID, "scope": r.Scope, "service": r.Service})
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"request_id": r.ID, "state": r.State, "confirm_by": r.ConfirmBy, "scope": r.Scope, "service": r.Service})
}

func (h *DeletionHandler) verifyIdentity(c fiber.Ctx, userID, password string) error {
	return verifyIdentity(c, h.users, userID, password)
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
		"confirm_by": r.ConfirmBy, "grace_until": r.GraceUntil, "legal_hold": r.LegalHold,
		"scope": r.Scope, "service": r.Service,
	})
}

// services lists the products the person may leave on their own.
func (h *DeletionHandler) services(c fiber.Ctx) error {
	opts, err := h.svc.Services(c.Context(), middleware.GetUserID(c))
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	return c.JSON(opts)
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
	return c.JSON(fiber.Map{"request_id": r.ID, "state": r.State, "grace_until": r.GraceUntil, "scope": r.Scope, "service": r.Service})
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
	return c.JSON(fiber.Map{"request_id": r.ID, "state": r.State, "scope": r.Scope, "service": r.Service})
}

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
		return apierror.Forbidden("This client is not an erasure participant.", c.Path())
	}
	var ack erasure.Ack
	if err := c.Bind().JSON(&ack); err != nil || ack.RequestID == "" {
		return apierror.InvalidRequest("Body must be an erasure ack with request_id.", c.Path())
	}
	if ack.Service != "" && ack.Service != service {
		return apierror.Forbidden("A participant may only ack for itself.", c.Path())
	}
	switch err := h.svc.Ack(c.Context(), service, ack); {
	case errors.Is(err, deletion.ErrNotFound):
		return apierror.NotFound("deletion request", c.Path())
	case err != nil:
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *DeletionHandler) subject(c fiber.Ctx, serviceForClient func(string) (string, bool), allowed []string) error {
	service, ok := serviceForClient(middleware.GetClientID(c))
	if !ok || !slices.Contains(allowed, service) {
		return apierror.Forbidden("This participant may not read deletion subjects.", c.Path())
	}
	requestID := c.Params("request_id")
	cpf, err := h.svc.Subject(c.Context(), requestID)
	if errors.Is(err, deletion.ErrNotFound) {
		return apierror.NotFound("deletion request", c.Path())
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	recordAuditAnon(c, h.audit, audit.EventDeletionSubjectRead, map[string]string{"request_id": requestID, "service": service})
	return c.JSON(fiber.Map{"cpf": cpf})
}

// RegisterAdmin mounts the support-admin operations on a request.
func (h *DeletionHandler) RegisterAdmin(admin fiber.Router) {
	admin.Post("/:request_id/legal-hold", h.adminLegalHold)
	admin.Post("/:request_id/cancel", h.adminCancel)
	admin.Post("/:request_id/redrive", h.adminRedrive)
}

type legalHoldBody struct {
	On *bool `json:"on" validate:"required"`
}

type redriveBody struct {
	Service string `json:"service" validate:"required"`
}

// adminError maps the service errors shared by the admin operations.
func adminError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, deletion.ErrNotFound):
		return apierror.NotFound("deletion request", c.Path())
	case errors.Is(err, deletion.ErrNotCancellable):
		return apierror.Conflict("This deletion can no longer be cancelled.", c.Path())
	case errors.Is(err, deletion.ErrHoldNeedsSecondAdmin):
		return apierror.Conflict("A different admin must lift this hold.", c.Path())
	case errors.Is(err, deletion.ErrStateChanged):
		return apierror.Conflict("The request changed concurrently; retry.", c.Path())
	}
	return apierror.ServerError(c.Path()).WithCause(err)
}

func (h *DeletionHandler) adminLegalHold(c fiber.Ctx) error {
	var req legalHoldBody
	if err := parseBody(c, &req); err != nil {
		return err
	}
	admin, id := middleware.GetUserID(c), c.Params("request_id")
	if err := h.svc.SetLegalHold(c.Context(), id, admin, *req.On); err != nil {
		return adminError(c, err)
	}
	event := audit.EventDeletionLegalHoldOff
	if *req.On {
		event = audit.EventDeletionLegalHoldOn
	}
	recordAudit(c, h.audit, admin, event, map[string]string{"request_id": id})
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *DeletionHandler) adminCancel(c fiber.Ctx) error {
	admin, id := middleware.GetUserID(c), c.Params("request_id")
	if _, err := h.svc.SupportCancel(c.Context(), id, admin); err != nil {
		return adminError(c, err)
	}
	recordAudit(c, h.audit, admin, audit.EventDeletionSupportCancelled, map[string]string{"request_id": id})
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *DeletionHandler) adminRedrive(c fiber.Ctx) error {
	var req redriveBody
	if err := parseBody(c, &req); err != nil {
		return err
	}
	admin, id := middleware.GetUserID(c), c.Params("request_id")
	if err := h.svc.Redrive(c.Context(), id, req.Service, admin); err != nil {
		return adminError(c, err)
	}
	recordAudit(c, h.audit, admin, audit.EventDeletionRedriven, map[string]string{"request_id": id, "service": req.Service})
	return c.SendStatus(fiber.StatusNoContent)
}
