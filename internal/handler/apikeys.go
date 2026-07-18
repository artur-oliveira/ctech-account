package handler

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/domain/apikey"
	"gopkg.aoctech.app/account/internal/domain/audit"
	"gopkg.aoctech.app/account/internal/middleware"
	"gopkg.aoctech.app/account/internal/scopes"
)

type APIKeysHandler struct {
	apiKeySvc  *apikey.Service
	catalogSvc *scopes.CatalogService
	audit      *audit.Service
}

func NewAPIKeysHandler(apiKeySvc *apikey.Service, catalogSvc *scopes.CatalogService, auditSvc *audit.Service) *APIKeysHandler {
	return &APIKeysHandler{apiKeySvc: apiKeySvc, catalogSvc: catalogSvc, audit: auditSvc}
}

func (h *APIKeysHandler) Register(account fiber.Router, stepUp fiber.Handler) {
	account.Get("/api-keys", h.list)
	account.Post("/api-keys", stepUp, h.create)
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

// defaultAPIKeyScope grants read-only profile access when no explicit scopes
// are requested. Must be a concrete catalog scope.
const defaultAPIKeyScope = "account:profile:read"

type createAPIKeyRequest struct {
	Name          string   `json:"name"            validate:"required,max=100"`
	Scopes        []string `json:"scopes"          validate:"omitempty,max=20,dive,max=100"`
	ExpiresInDays int      `json:"expires_in_days" validate:"omitempty,min=0,max=365"`
}

func (h *APIKeysHandler) create(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req createAPIKeyRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	if len(req.Scopes) == 0 {
		req.Scopes = []string{defaultAPIKeyScope}
	}
	// Scopes must come from the catalog (GET /v1.0/scopes). Identity scopes
	// (openid/profile/email) make no sense on machine keys and are rejected.
	// Catalog lookup failures fail closed — no key is issued with unchecked scopes.
	bad, err := h.catalogSvc.ValidateGrantable(c.Context(), req.Scopes)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	if bad == "" {
		for _, s := range req.Scopes {
			if scopes.IsOIDC(s) {
				bad = s
				break
			}
		}
	}
	if bad != "" {
		return apierror.InvalidRequest(
			"Scope "+bad+" is not a grantable API key scope. See GET /v1.0/scopes.", c.Path()).Send(c)
	}

	var expiresIn time.Duration
	if req.ExpiresInDays > 0 {
		expiresIn = time.Duration(req.ExpiresInDays) * 24 * time.Hour
	}

	k, rawKey, err := h.apiKeySvc.Create(c.Context(), userID, req.Name, req.Scopes, expiresIn)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	recordAudit(c, h.audit, userID, audit.EventAPIKeyCreated, map[string]string{"key_id": k.ID()})

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

	recordAudit(c, h.audit, userID, audit.EventAPIKeyRevoked, map[string]string{"key_id": keyID})

	return c.Status(fiber.StatusNoContent).Send(nil)
}
