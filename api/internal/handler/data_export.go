package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/account/api/internal/domain/dataexport"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/problem"
	"gopkg.aoctech.app/account/api/internal/scopes"
	"gopkg.aoctech.app/api-commons/authguard"
	wire "gopkg.aoctech.app/api-commons/dataexport"
)

// DataExportHandler is the personal-data export (docs/specs/2026-10-17-data-export.md).
type DataExportHandler struct {
	svc   *dataexport.Service
	users *user.Service
}

func NewDataExportHandler(svc *dataexport.Service, users *user.Service) *DataExportHandler {
	return &DataExportHandler{svc: svc, users: users}
}

// Register mounts the routes on account. selfOnly restricts them to this
// service's own frontend: a third-party app granted the scope must not be able
// to read a person's whole data.
func (h *DataExportHandler) Register(account fiber.Router, selfOnly fiber.Handler, limiters ...fiber.Handler) {
	post := []any{selfOnly}
	for _, l := range limiters {
		post = append(post, l)
	}
	account.Post("/export", middleware.RequireScope(scopes.AccountExportWrite), append(post, h.request)...)
	account.Get("/export", middleware.RequireScope(scopes.AccountExportWrite), selfOnly, h.status)
	account.Delete("/export", middleware.RequireScope(scopes.AccountExportWrite), selfOnly, h.discard)
}

// RegisterInternal mounts the participants' ack (service tokens only).
// serviceForClient maps the caller's azp to its participant name.
func (h *DataExportHandler) RegisterInternal(v1 fiber.Router, requireAuth fiber.Handler, serviceForClient func(string) (string, bool)) {
	v1.Post("/internal/export/ack", requireAuth, authguard.RequireInternalScope(scopes.InternalAccountExportAck),
		func(c fiber.Ctx) error { return h.ack(c, serviceForClient) })
}

func (h *DataExportHandler) ack(c fiber.Ctx, serviceForClient func(string) (string, bool)) error {
	service, ok := serviceForClient(middleware.GetClientID(c))
	if !ok {
		return problem.Forbidden("This client is not an export participant.", c.Path())
	}
	var ack wire.Ack
	if err := c.Bind().JSON(&ack); err != nil || ack.RequestID == "" || ack.Sub == "" {
		return problem.InvalidRequest("Body must be an export ack with request_id and sub.", c.Path())
	}
	if ack.Service != "" && ack.Service != service {
		return problem.Forbidden("A participant may only ack for itself.", c.Path())
	}
	switch err := h.svc.Ack(c.Context(), service, ack); {
	case err == nil, errors.Is(err, dataexport.ErrStale):
		// A stale ack (replaced, expired or discarded export) is dropped with a
		// 2xx so the participant stops retrying.
		return c.SendStatus(fiber.StatusNoContent)
	case errors.Is(err, dataexport.ErrUnknownPart):
		return problem.Forbidden("This service was not asked for a part of this export.", c.Path())
	case errors.Is(err, dataexport.ErrNotUploaded):
		return problem.Conflict("The part is not in the bucket yet; upload it and ack again.", c.Path())
	default:
		return problem.ServerError(c.Path()).WithCause(err)
	}
}

type exportRequestBody struct {
	Password string `json:"password"`
}

func (h *DataExportHandler) request(c fiber.Ctx) error {
	var req exportRequestBody
	if err := parseBody(c, &req); err != nil {
		return err
	}
	userID := middleware.GetUserID(c)
	if err := verifyIdentity(c, h.users, userID, req.Password); err != nil {
		// Sent here, not returned: the rate limiters read the response status.
		if p, ok := errors.AsType[*problem.Problem](err); ok {
			return p.Send(c)
		}
		return err
	}
	e, err := h.svc.Request(c.Context(), userID)
	switch {
	case errors.Is(err, dataexport.ErrLocked):
		return problem.Conflict("This account cannot export its data right now.", c.Path())
	case errors.Is(err, dataexport.ErrBuilding):
		return problem.Conflict("An export is already being prepared.", c.Path())
	case errors.Is(err, dataexport.ErrTooSoon):
		return problem.TooManyRequests("You can request one export every 24 hours.", c.Path(), dataexport.MinInterval)
	case err != nil:
		return problem.ServerError(c.Path()).WithCause(err)
	}
	return c.Status(fiber.StatusAccepted).JSON(exportView(e, nil))
}

func (h *DataExportHandler) status(c fiber.Ctx) error {
	e, err := h.svc.Status(c.Context(), middleware.GetUserID(c))
	if errors.Is(err, dataexport.ErrNotFound) {
		return problem.NotFound("export", c.Path())
	}
	if err != nil {
		return problem.ServerError(c.Path()).WithCause(err)
	}
	var links []dataexport.Link
	if e.State == dataexport.StateReady {
		if links, err = h.svc.Links(c.Context(), e); err != nil {
			return problem.ServerError(c.Path()).WithCause(err)
		}
	}
	return c.JSON(exportView(e, links))
}

func (h *DataExportHandler) discard(c fiber.Ctx) error {
	err := h.svc.Discard(c.Context(), middleware.GetUserID(c))
	if errors.Is(err, dataexport.ErrNotFound) {
		return problem.NotFound("export", c.Path())
	}
	if err != nil {
		return problem.ServerError(c.Path()).WithCause(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func exportView(e *dataexport.Export, links []dataexport.Link) fiber.Map {
	parts := make([]fiber.Map, 0, len(e.Parts))
	for _, p := range e.Parts {
		parts = append(parts, fiber.Map{"service": p.Service, "status": p.Status, "size": p.Size})
	}
	files := make([]fiber.Map, 0, len(links))
	for _, l := range links {
		files = append(files, fiber.Map{"service": l.Service, "url": l.URL, "size": l.Size})
	}
	return fiber.Map{"id": e.ID, "state": e.State, "requested_at": e.RequestedAt, "ready_at": e.ReadyAt,
		"expires_at": e.ExpiresAt, "parts": parts, "links": files}
}
