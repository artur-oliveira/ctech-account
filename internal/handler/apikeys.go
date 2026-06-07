package handler

import (
	"time"

	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/domain/apikey"
	"github.com/artur-oliveira/ctech-account/internal/middleware"
	"github.com/artur-oliveira/ctech-account/internal/validate"
	"github.com/gofiber/fiber/v3"
)

type APIKeysHandler struct {
	apiKeySvc *apikey.Service
}

func NewAPIKeysHandler(apiKeySvc *apikey.Service) *APIKeysHandler {
	return &APIKeysHandler{apiKeySvc: apiKeySvc}
}

func (h *APIKeysHandler) Register(account fiber.Router) {
	account.Get("/api-keys", h.list)
	account.Post("/api-keys", h.create)
	account.Delete("/api-keys/:id", h.revoke)
}

func (h *APIKeysHandler) list(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	keys, err := h.apiKeySvc.List(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	result := make([]fiber.Map, 0, len(keys))
	for _, k := range keys {
		result = append(result, fiber.Map{
			"key_id":       k.ID(),
			"key_prefix":   k.KeyPrefix,
			"name":         k.Name,
			"scopes":       k.Scopes,
			"last_used_at": k.LastUsedAt,
			"expires_at":   k.ExpiresAt,
			"created_at":   k.CreatedAt,
		})
	}

	return c.JSON(fiber.Map{"api_keys": result})
}

type createAPIKeyRequest struct {
	Name          string   `json:"name"            validate:"required,max=100"`
	Scopes        []string `json:"scopes"          validate:"omitempty,dive,oneof=read write admin"`
	ExpiresInDays int      `json:"expires_in_days" validate:"omitempty,min=0,max=365"`
}

func (h *APIKeysHandler) create(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req createAPIKeyRequest
	if err := c.Bind().JSON(&req); err != nil {
		return apierror.InvalidRequest("Request body is malformed or contains invalid JSON.", c.Path()).Send(c)
	}
	if err := validate.Struct(req); err != nil {
		ve, _ := validate.IsValidationError(err)
		return apierror.ValidationFailed(ve.Detail(), c.Path()).Send(c)
	}

	if len(req.Scopes) == 0 {
		req.Scopes = []string{"read"}
	}

	var expiresIn time.Duration
	if req.ExpiresInDays > 0 {
		expiresIn = time.Duration(req.ExpiresInDays) * 24 * time.Hour
	}

	k, rawKey, err := h.apiKeySvc.Create(c.Context(), userID, req.Name, req.Scopes, expiresIn)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"key_id":     k.ID(),
		"key_prefix": k.KeyPrefix,
		"name":       k.Name,
		"scopes":     k.Scopes,
		"expires_at": k.ExpiresAt,
		"created_at": k.CreatedAt,
		"raw_key":    rawKey,
	})
}

func (h *APIKeysHandler) revoke(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	keyID := c.Params("id")

	if err := h.apiKeySvc.Revoke(c.Context(), userID, keyID); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.Status(fiber.StatusNoContent).Send(nil)
}
