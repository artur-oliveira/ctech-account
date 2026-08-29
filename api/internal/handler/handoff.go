package handler

import (
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/api-commons/observability"
)

// maxStateLength caps the opaque value a product asks us to echo. It is not
// parsed, so the only thing worth bounding is its size.
const maxStateLength = 512

// HandoffHandler validates a product's request to send somebody here to create
// an organization, and names the product so the screen can say who sent them.
//
// It exists because the UI is a static export: a check written in the client is
// a check an attacker skips, so the only place this can be decided is here.
type HandoffHandler struct {
	clients oauthclient.Repository
}

func NewHandoffHandler(clients oauthclient.Repository) *HandoffHandler {
	return &HandoffHandler{clients: clients}
}

// Register mounts the route on the organizations group, which already carries
// RequireAuth and RequireClientID(Self).
func (h *HandoffHandler) Register(orgs fiber.Router) {
	orgs.Get("/handoff", h.validate)
}

func (h *HandoffHandler) validate(c fiber.Ctx) error {
	clientID := strings.TrimSpace(c.Query("client_id"))
	returnTo := strings.TrimSpace(c.Query("return_to"))
	if clientID == "" || returnTo == "" {
		return h.reject(c, "missing client_id or return_to", clientID, returnTo)
	}
	if len(c.Query("state")) > maxStateLength {
		return h.reject(c, "state is longer than the cap", clientID, returnTo)
	}

	client, err := h.clients.GetByID(c.Context(), clientID)
	if err != nil || client == nil {
		// An unknown client and an unregistered origin answer alike. Telling
		// them apart would make this route a probe for which client ids exist.
		return h.reject(c, "unknown client", clientID, returnTo)
	}
	// A third party sending a user to create organizations is not a flow that
	// has been designed, so it is refused rather than half-supported.
	if !client.FirstParty {
		return h.reject(c, "client is not first-party", clientID, returnTo)
	}
	if !client.IsRegisteredOrigin(returnTo) {
		return h.reject(c, "return_to is not a registered origin", clientID, returnTo)
	}

	normalized, ok := normalizeReturnTo(returnTo)
	if !ok {
		return h.reject(c, "return_to is not a parseable absolute URL", clientID, returnTo)
	}
	// The normalized value is echoed and the screen redirects to *that*, not to
	// the raw query parameter: whatever was validated is what the browser
	// follows, or the two can differ.
	return c.JSON(fiber.Map{"client_name": client.Name, "return_to": normalized})
}

// normalizeReturnTo re-serializes the URL and drops anything a fragment could
// carry. A fragment never reaches the server, so it cannot be part of what was
// validated, and echoing one back would hand the product a value this route did
// not actually check.
func normalizeReturnTo(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String(), true
}

// reject answers one problem for every cause and logs which it was. The person
// on the screen can do nothing about any of them; whoever is fixing the
// integration needs to know exactly which.
func (h *HandoffHandler) reject(c fiber.Ctx, reason, clientID, returnTo string) error {
	observability.Warn(c.Context(), "organization handoff refused", nil,
		"reason", reason, "client_id", clientID, "return_to", returnTo)
	return apierror.OrganizationHandoffInvalid(c.Path()).Send(c)
}
