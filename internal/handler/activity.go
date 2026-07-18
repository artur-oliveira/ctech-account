package handler

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/domain/audit"
	"gopkg.aoctech.app/account/internal/middleware"
)

const (
	activityDefaultLimit = 25
	activityMaxLimit     = 100
)

// activityMetadataAllowlist filters event metadata exposed to the account
// owner — internal keys stay internal.
var activityMetadataAllowlist = map[string]bool{
	"client_id": true, "client_name": true, "key_id": true,
	"session_id": true, "method": true, "device_name": true,
}

type ActivityHandler struct {
	auditSvc *audit.Service
}

func NewActivityHandler(auditSvc *audit.Service) *ActivityHandler {
	return &ActivityHandler{auditSvc: auditSvc}
}

func (h *ActivityHandler) Register(account fiber.Router) {
	account.Get("/activity", h.list)
}

func (h *ActivityHandler) list(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	limit := int32(activityDefaultLimit)
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > activityMaxLimit {
			return apierror.InvalidRequest("limit must be an integer between 1 and 100.", c.Path()).Send(c)
		}
		limit = int32(n)
	}

	events, next, err := h.auditSvc.ListByUser(c.Context(), userID, c.Query("cursor"), limit)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	result := make([]fiber.Map, 0, len(events))
	for _, e := range events {
		meta := map[string]string{}
		for k, v := range e.Metadata {
			if activityMetadataAllowlist[k] {
				meta[k] = v
			}
		}
		result = append(result, fiber.Map{
			"event_type": e.EventType,
			"ip":         e.IP,
			"user_agent": e.UserAgent,
			"metadata":   meta,
			"created_at": e.CreatedAt,
		})
	}
	return c.JSON(fiber.Map{"events": result, "next_cursor": next})
}
