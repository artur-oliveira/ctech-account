package handler

import (
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/session"
	"gopkg.aoctech.app/account/api/internal/middleware"
)

type SessionsHandler struct {
	sessionSvc *session.Service
	audit      *audit.Service
}

func NewSessionsHandler(sessionSvc *session.Service, auditSvc *audit.Service) *SessionsHandler {
	return &SessionsHandler{sessionSvc: sessionSvc, audit: auditSvc}
}

func (h *SessionsHandler) Register(account fiber.Router) {
	account.Get("/sessions", h.list)
	account.Delete("/sessions/:id", h.revoke)
	account.Delete("/sessions", h.revokeAll)
}

func (h *SessionsHandler) list(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	currentSessionID := middleware.GetSessionID(c)

	sessions, err := h.sessionSvc.List(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	result := make([]fiber.Map, 0, len(sessions))
	for _, s := range sessions {
		result = append(result, fiber.Map{
			"session_id":    s.ID(),
			"device_name":   s.DeviceName,
			"ip_address":    s.IPAddress,
			"created_at":    s.CreatedAt,
			"last_used_at":  s.LastUsedAt,
			"is_current":    s.ID() == currentSessionID,
			"geo_city":      s.GeoCity,
			"geo_region":    s.GeoRegion,
			"geo_latitude":  s.GeoLatitude,
			"geo_longitude": s.GeoLongitude,
		})
	}

	return c.JSON(fiber.Map{"sessions": result})
}

func (h *SessionsHandler) revoke(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	currentSessionID := middleware.GetSessionID(c)
	targetSessionID := c.Params("id")

	if targetSessionID == currentSessionID {
		return apierror.InvalidRequest("Cannot revoke the current session — use POST /v1.0/auth/logout instead.", c.Path()).Send(c)
	}

	if err := h.sessionSvc.Revoke(c.Context(), userID, targetSessionID); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	recordAudit(c, h.audit, userID, audit.EventSessionRevoked, map[string]string{"session_id": targetSessionID})

	return c.Status(fiber.StatusNoContent).Send(nil)
}

func (h *SessionsHandler) revokeAll(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	currentSessionID := middleware.GetSessionID(c)

	if err := h.sessionSvc.RevokeAll(c.Context(), userID, currentSessionID); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	recordAudit(c, h.audit, userID, audit.EventSessionRevokedAll, nil)

	return c.Status(fiber.StatusNoContent).Send(nil)
}
