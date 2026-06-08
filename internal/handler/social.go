package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/cache"
	"github.com/artur-oliveira/ctech-account/internal/config"
	"github.com/artur-oliveira/ctech-account/internal/crypto"
	"github.com/artur-oliveira/ctech-account/internal/domain/session"
	"github.com/artur-oliveira/ctech-account/internal/domain/user"
	"github.com/gofiber/fiber/v3"
)

const googleStateTTL = 10 * time.Minute

type SocialHandler struct {
	userSvc    *user.Service
	sessionSvc *session.Service
	cache      *cache.Client
	cfg        *config.Config
}

func NewSocialHandler(userSvc *user.Service, sessionSvc *session.Service, c *cache.Client, cfg *config.Config) *SocialHandler {
	return &SocialHandler{userSvc: userSvc, sessionSvc: sessionSvc, cache: c, cfg: cfg}
}

func (h *SocialHandler) Register(v1 fiber.Router) {
	auth := v1.Group("/auth")
	auth.Get("/google", h.googleRedirect)
	auth.Get("/google/callback", h.googleCallback)
}

func (h *SocialHandler) googleRedirect(c fiber.Ctx) error {
	if h.cfg.GoogleClientID == "" {
		return apierror.ServerError(c.Path()).Send(c)
	}

	continueURL := c.Query("continue", "/")
	rawState, stateHash, err := crypto.GenerateMFAToken()
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	if h.cache != nil && h.cache.Enabled() {
		_ = h.cache.Set(c.Context(), "gs:"+stateHash, continueURL, googleStateTTL)
	}

	redirectURI := h.cfg.BaseURL + "/v1.0/auth/google/callback"
	params := url.Values{
		"client_id":     {h.cfg.GoogleClientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {rawState},
		"access_type":   {"offline"},
		"prompt":        {"select_account"},
	}
	return c.Redirect().Status(fiber.StatusFound).To("https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode())
}

func (h *SocialHandler) googleCallback(c fiber.Ctx) error {
	if errParam := c.Query("error"); errParam != "" {
		return c.Redirect().Status(fiber.StatusFound).To(h.cfg.AppURL + "/login?error=google_denied")
	}

	code := c.Query("code")
	rawState := c.Query("state")
	if code == "" || rawState == "" {
		return apierror.InvalidRequest("Missing code or state.", c.Path()).Send(c)
	}

	continueURL := "/"
	stateHash := crypto.HashToken(rawState)
	if h.cache != nil && h.cache.Enabled() {
		if err := h.cache.Get(c.Context(), "gs:"+stateHash, &continueURL); err != nil {
			return apierror.InvalidToken("OAuth state is invalid or has expired.", c.Path()).Send(c)
		}
		_ = h.cache.Delete(c.Context(), "gs:"+stateHash)
	}

	googleProfile, err := h.exchangeGoogleCode(code)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	u, err := h.userSvc.FindOrCreateByGoogle(
		c.Context(),
		googleProfile.Sub,
		googleProfile.Email,
		googleProfile.GivenName,
		googleProfile.FamilyName,
		googleProfile.Picture,
	)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	if err := h.issueSessionFromSocial(c, u); err != nil {
		return err
	}

	// If there's a pending OAuth authorize flow, redirect back to it.
	// Otherwise go to the continueURL (app's landing page).
	return c.Redirect().Status(fiber.StatusFound).To(continueURL)
}

type googleUserInfo struct {
	Sub        string `json:"sub"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	GivenName  string `json:"given_name"`
	FamilyName string `json:"family_name"`
	Picture    string `json:"picture"`
}

func (h *SocialHandler) exchangeGoogleCode(code string) (*googleUserInfo, error) {
	redirectURI := h.cfg.BaseURL + "/v1.0/auth/google/callback"

	tokenBody := url.Values{
		"code":          {code},
		"client_id":     {h.cfg.GoogleClientID},
		"client_secret": {h.cfg.GoogleClientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	}

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", tokenBody)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &tokenResp); err != nil || tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token parse error: %s", tokenResp.Error)
	}

	req, _ := http.NewRequest(http.MethodGet, "https://www.googleapis.com/oauth2/v3/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	uiResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo fetch: %w", err)
	}
	defer uiResp.Body.Close()

	var profile googleUserInfo
	if err := json.NewDecoder(uiResp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("userinfo decode: %w", err)
	}
	if profile.Email == "" {
		return nil, fmt.Errorf("google did not return email")
	}
	return &profile, nil
}

func (h *SocialHandler) issueSessionFromSocial(c fiber.Ctx, u *user.User) error {
	deviceName := parseDeviceName(c.Get("User-Agent"))
	sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), deviceName, c.IP(), c.Get("User-Agent"))
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	secure := strings.HasPrefix(h.cfg.BaseURL, "https")
	c.Cookie(&fiber.Cookie{
		Name:     "ctech_session",
		Value:    session.BuildCookieValue(u.ID(), sess.ID(), rawToken),
		HTTPOnly: true,
		Secure:   secure,
		SameSite: "Lax",
		Path:     "/",
		MaxAge:   int(session.SessionTTL.Seconds()),
	})
	return nil
}
