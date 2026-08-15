package handler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

const (
	headerIfMatch         = "If-Match"
	headerETag            = "ETag"
	headerSourceRepo      = "X-CTech-Source-Repository"
	headerSourceRevision  = "X-CTech-Source-Revision"
	maxSourceMetadataSize = 200
)

type scopeRegistryClientRepository interface {
	GetByID(ctx context.Context, clientID string) (*oauthclient.OAuthClient, error)
}

type scopeRegistryClientReconciler interface {
	EnsureFirstPartyPublicClientScopes(ctx context.Context, clientID string, requiredScopes []string) (*oauthclient.OAuthClient, bool, error)
}

// ScopeRegistryHandler is the OAuth-protected control plane through which a
// trusted Resource Server reconciles its own scope manifest.
type ScopeRegistryHandler struct {
	registry  *scopes.RegistryService
	clients   scopeRegistryClientRepository
	reconcile scopeRegistryClientReconciler
	audit     *audit.Service
}

func NewScopeRegistryHandler(registry *scopes.RegistryService, clients scopeRegistryClientRepository, reconciler scopeRegistryClientReconciler, auditSvc *audit.Service) *ScopeRegistryHandler {
	return &ScopeRegistryHandler{registry: registry, clients: clients, reconcile: reconciler, audit: auditSvc}
}

func (h *ScopeRegistryHandler) Register(v1 fiber.Router, auth ...fiber.Handler) {
	handlers := make([]any, len(auth))
	for i, mw := range auth {
		handlers[i] = mw
	}
	group := v1.Group("/internal/resource-servers", handlers...)
	group.Get("/:id/manifest", h.get)
	group.Put("/:id/manifest", h.put)
}

func (h *ScopeRegistryHandler) authorize(c fiber.Ctx, id string) (string, error) {
	clientID := middleware.GetClientID(c)
	if clientID == "" || middleware.GetUserID(c) != clientID {
		return "", apierror.Forbidden("Publisher token subject and authorized party must match.", c.Path()).Send(c)
	}
	client, err := h.clients.GetByID(c.Context(), clientID)
	if err != nil || client.ManagedResourceID != id {
		return "", apierror.Forbidden("Publisher is not bound to this resource server.", c.Path()).Send(c)
	}
	return clientID, nil
}

func (h *ScopeRegistryHandler) get(c fiber.Ctx) error {
	id := c.Params("id")
	if _, err := h.authorize(c, id); err != nil {
		return err
	}
	resource, err := h.registry.Get(c.Context(), id)
	if err != nil {
		if errors.Is(err, scopes.ErrResourceNotFound) {
			return apierror.NotFound("Resource server", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}
	c.Set(headerETag, resourceETag(resource))
	return c.JSON(resource)
}

func (h *ScopeRegistryHandler) put(c fiber.Ctx) error {
	id := c.Params("id")
	actor, err := h.authorize(c, id)
	if err != nil {
		return err
	}
	expected, err := parseETag(c.Get(headerIfMatch))
	if err != nil {
		return apierror.PreconditionFailed("A valid If-Match resource revision is required.", c.Path()).Send(c)
	}
	current, err := h.registry.Get(c.Context(), id)
	if err != nil {
		if errors.Is(err, scopes.ErrResourceNotFound) {
			return apierror.NotFound("Resource server", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}
	if c.Get(headerIfMatch) != resourceETag(current) {
		return apierror.PreconditionFailed("If-Match does not identify the current resource manifest.", c.Path()).Send(c)
	}
	var manifest scopes.Manifest
	if err := parseBody(c, &manifest); err != nil {
		return err
	}
	if manifest.ResourceServerID != id {
		return apierror.ValidationFailed("resource_server_id must match the route resource.", c.Path()).Send(c)
	}
	sourceRepo, sourceRevision := c.Get(headerSourceRepo), c.Get(headerSourceRevision)
	if len(sourceRepo) > maxSourceMetadataSize || len(sourceRevision) > maxSourceMetadataSize {
		return apierror.ValidationFailed("Source metadata is too long.", c.Path()).Send(c)
	}
	resource, changed, publishErr := h.registry.Publish(c.Context(), actor, manifest, expected, sourceRepo, sourceRevision)
	if publishErr != nil {
		h.record(c, actor, audit.EventScopeManifestRejected, id, publishErr.Error())
		switch {
		case errors.Is(publishErr, scopes.ErrRevisionConflict):
			return apierror.PreconditionFailed("The resource manifest changed; fetch its current ETag and retry.", c.Path()).Send(c)
		case errors.Is(publishErr, scopes.ErrScopeRemoval):
			return apierror.Conflict(publishErr.Error(), c.Path()).Send(c)
		case scopes.IsRegistryValidationError(publishErr):
			return apierror.ValidationFailed(publishErr.Error(), c.Path()).Send(c)
		case errors.Is(publishErr, scopes.ErrResourceNotFound):
			return apierror.NotFound("Resource server", c.Path()).Send(c)
		default:
			return apierror.ServerError(c.Path()).Send(c)
		}
	}
	_, clientChanged, reconcileErr := h.reconcile.EnsureFirstPartyPublicClientScopes(c.Context(), id, publicActiveScopes(manifest.Scopes))
	if reconcileErr != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	c.Set(headerETag, resourceETag(resource))
	h.record(c, actor, audit.EventScopeManifestPublished, id, resource.ManifestHash)
	if clientChanged {
		h.record(c, actor, audit.EventOAuthClientUpdated, id, "public scopes reconciled")
	}
	return c.JSON(fiber.Map{
		"resource_server_id": resource.ID(), "revision": resource.Revision,
		"manifest_hash": resource.ManifestHash, "changed": changed,
		"first_party_client_changed": clientChanged,
		"active_scopes":              countScopes(resource.Scopes, scopes.StatusActive),
		"deprecated_scopes":          countScopes(resource.Scopes, scopes.StatusDeprecated),
	})
}

func publicActiveScopes(entries []scopes.ScopeDefinition) []string {
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Visibility == scopes.VisibilityPublic && entry.Status == scopes.StatusActive {
			result = append(result, entry.Scope)
		}
	}
	return result
}

func resourceETag(resource *scopes.ResourceServer) string {
	return fmt.Sprintf(`"%d:%s"`, resource.Revision, resource.ManifestHash)
}

func parseETag(raw string) (int64, error) {
	raw = strings.Trim(raw, `"`)
	revision, _, ok := strings.Cut(raw, ":")
	if !ok {
		return 0, fmt.Errorf("malformed ETag")
	}
	return strconv.ParseInt(revision, 10, 64)
}

func countScopes(entries []scopes.ScopeDefinition, status string) int {
	count := 0
	for _, entry := range entries {
		if entry.Status == status {
			count++
		}
	}
	return count
}

func (h *ScopeRegistryHandler) record(c fiber.Ctx, actor, event, resourceID, detail string) {
	if len(detail) > maxSourceMetadataSize {
		detail = detail[:maxSourceMetadataSize]
	}
	h.audit.Record(c.Context(), audit.Entry{UserID: actor, Type: event, IP: clientIP(c), UserAgent: c.Get(fiber.HeaderUserAgent), Metadata: map[string]string{"resource_server_id": resourceID, "detail": detail}})
}
