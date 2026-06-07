package handler

import (
	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/domain/session"
	"github.com/artur-oliveira/ctech-account/internal/middleware"
	"github.com/gofiber/fiber/v3"
)

type SessionsHandler struct {
	sessionSvc *session.Service
}

func NewSessionsHandler(sessionSvc *session.Service) *SessionsHandler {
	return &SessionsHandler{sessionSvc: sessionSvc}
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
			"session_id":   s.ID(),
			"device_name":  s.DeviceName,
			"ip_address":   s.IPAddress,
			"created_at":   s.CreatedAt,
			"last_used_at": s.LastUsedAt,
			"is_current":   s.ID() == currentSessionID,
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

	return c.Status(fiber.StatusNoContent).Send(nil)
}

func (h *SessionsHandler) revokeAll(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	currentSessionID := middleware.GetSessionID(c)

	if err := h.sessionSvc.RevokeAll(c.Context(), userID, currentSessionID); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.Status(fiber.StatusNoContent).Send(nil)
}
