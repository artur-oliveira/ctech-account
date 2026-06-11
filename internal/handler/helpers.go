package handler

import (
	"context"
	"strings"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/config"
	"github.com/artur-oliveira/ctech-account/internal/domain/session"
	"github.com/artur-oliveira/ctech-account/internal/geo"
	"github.com/artur-oliveira/ctech-account/internal/validate"
	"github.com/gofiber/fiber/v3"
)

// clientIP extracts the real client IP from the first entry of the
// X-Forwarded-For chain (e.g. "clientIP, cfIP, instanceIP").
func clientIP(c fiber.Ctx) string {
	raw := c.IP()
	if idx := strings.IndexByte(raw, ','); idx != -1 {
		return strings.TrimSpace(raw[:idx])
	}
	return strings.TrimSpace(raw)
}

// enrichSessionAsync fires a goroutine that looks up geo data for ip and
// writes it back onto the session. Failures are silently ignored so they
// never block a login.
func enrichSessionAsync(svc *session.Service, userID, sessionID, ip string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		loc, err := geo.Lookup(ip)
		if err != nil {
			return
		}
		_ = svc.UpdateGeoData(ctx, userID, sessionID, loc.City, loc.Region, loc.Latitude, loc.Longitude)
	}()
}

// effectiveCookieDomain returns cfg.CookieDomain for production hosts, or ""
// for localhost/127.x requests so that browsers don't reject the cookie.
// Browsers ignore Set-Cookie Domain attributes that don't match the request host,
// and localhost is never a registerable domain.
func effectiveCookieDomain(c fiber.Ctx, cfg *config.Config) string {
	host := c.Hostname()
	if host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1" {
		return ""
	}
	return cfg.CookieDomain
}

// parseBody decodes JSON from the request body and validates the struct.
// Returns a *apierror.Problem (as error) on failure so the caller can return it directly
// and Fiber's error handler will send the RFC 7807 response.
func parseBody[T any](c fiber.Ctx, dst *T) error {
	if err := c.Bind().JSON(dst); err != nil {
		return apierror.InvalidRequest("Request body is malformed or contains invalid JSON.", c.Path())
	}
	if err := validate.Struct(*dst); err != nil {
		ve, _ := validate.IsValidationError(err)
		return apierror.ValidationFailed(ve.Detail(), c.Path())
	}
	return nil
}
