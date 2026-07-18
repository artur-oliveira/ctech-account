package handler

import (
	"context"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/config"
	"gopkg.aoctech.app/account/internal/domain/audit"
	"gopkg.aoctech.app/account/internal/domain/session"
	"gopkg.aoctech.app/account/internal/geo"
	"gopkg.aoctech.app/account/internal/utils"
	"gopkg.aoctech.app/account/internal/validate"
)

// clientIP extracts the real client IP from the first entry of the
// X-Forwarded-For chain (e.g. "clientIP, cfIP, instanceIP").
func clientIP(c fiber.Ctx) string {
	return utils.IP(c)
}

// recordAudit records a security event attributed to userID with the request's
// IP/User-Agent. Safe on a nil service (some handlers are built without audit
// in narrow tests).
func recordAudit(c fiber.Ctx, svc *audit.Service, userID, eventType string, meta map[string]string) {
	if svc == nil {
		return
	}
	svc.Record(c.Context(), audit.Entry{
		UserID:    userID,
		Type:      eventType,
		IP:        clientIP(c),
		UserAgent: c.Get("User-Agent"),
		Metadata:  meta,
	})
}

// recordAuditAnon records a security event that cannot be attributed to a
// known user, keyed by the client IP.
func recordAuditAnon(c fiber.Ctx, svc *audit.Service, eventType string, meta map[string]string) {
	if svc == nil {
		return
	}
	svc.Record(c.Context(), audit.Entry{
		AnonIP:    clientIP(c),
		Type:      eventType,
		IP:        clientIP(c),
		UserAgent: c.Get("User-Agent"),
		Metadata:  meta,
	})
}

// emailDomain returns the part after "@" — audit metadata must never store a
// full address for an unauthenticated failure.
func emailDomain(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 {
		return email[i+1:]
	}
	return ""
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

// sessionCookieName holds the session refresh token used by the /authorize endpoint.
const sessionCookieName = "ctech_session"

// clearCookieMaxAge is the MaxAge that instructs the browser to drop a cookie.
const clearCookieMaxAge = -1

// setAuthCookie writes an auth cookie with this service's fixed security attributes.
// Every auth cookie must go through here so HttpOnly/Secure/SameSite can never
// drift apart between call sites.
func setAuthCookie(c fiber.Ctx, cfg *config.Config, name, value string, maxAge int) {
	cookie := &fiber.Cookie{
		Name:     name,
		Value:    value,
		HTTPOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: "Lax",
		Path:     "/",
		Domain:   effectiveCookieDomain(c, cfg),
		MaxAge:   maxAge,
	}
	if maxAge > 0 {
		cookie.Expires = time.Now().Add(time.Duration(maxAge) * time.Second)
	}
	c.Cookie(cookie)
}

// clearAuthCookie expires an auth cookie in the browser.
func clearAuthCookie(c fiber.Ctx, cfg *config.Config, name string) {
	setAuthCookie(c, cfg, name, "", clearCookieMaxAge)
}

// authHintCookieName is a non-HttpOnly marker that tells frontend JS an SSO
// session (or SPA refresh token) may exist, so it knows whether attempting a
// silent token refresh is worthwhile. It carries no secret — only "1".
const authHintCookieName = "ctech_auth"

// setAuthHintCookie writes the JS-readable auth marker cookie.
func setAuthHintCookie(c fiber.Ctx, cfg *config.Config, maxAge int) {
	cookie := &fiber.Cookie{
		Name:     authHintCookieName,
		Value:    "1",
		HTTPOnly: false,
		Secure:   cfg.CookieSecure,
		SameSite: "Lax",
		Path:     "/",
		Domain:   effectiveCookieDomain(c, cfg),
		MaxAge:   maxAge,
	}
	if maxAge > 0 {
		cookie.Expires = time.Now().Add(time.Duration(maxAge) * time.Second)
	}
	c.Cookie(cookie)
}

// setSessionCookies writes the SSO session cookie (read by /authorize) plus the
// auth hint marker. Per-client refresh tokens (ctech_rt) are only issued by the
// /token code exchange — the SSO token must never double as a refresh token.
func setSessionCookies(c fiber.Ctx, cfg *config.Config, rawToken string) {
	maxAge := int(session.SessionTTL.Seconds())
	setAuthCookie(c, cfg, sessionCookieName, rawToken, maxAge)
	setAuthHintCookie(c, cfg, maxAge)
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
