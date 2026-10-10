// Package billingclient is ctech-account's client for ctech-billing: the
// entitlement read behind plan limits and the level reports behind on-demand
// billing (docs/specs/2026-10-10-space-plan-limits.md §§ 1, 4).
//
// api-commons has no retrying HTTP client (only oauth2client, which fetches a
// token), so the one retry the read needs lives here (decision P5). ctech-dfe
// has its own billingclient; a shared one belongs in ctech-go-common later.
package billingclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// EntitlementsTimeout is the whole budget of one entitlement read,
	// retries included: a person is waiting on the create or invite.
	EntitlementsTimeout = 2 * time.Second
	// reportTimeout bounds one level report.
	reportTimeout       = 2 * time.Second
	entitlementAttempts = 2
	maxBody             = 1 << 20

	entitlementsPath = "/v1.0/entitlements"
	levelsPath       = "/v1.0/usage/levels"

	OwnerKeyFinance = "finance"
	MeterSpaces     = "finance_spaces"
	MeterPeople     = "finance_people"

	headerIdempotencyKey = "Idempotency-Key"
)

// ErrUnavailable is billing unreachable, slow, or answering anything but a
// usable 2xx. Callers turn it into 503 plan_unavailable.
var ErrUnavailable = errors.New("billing is unavailable")

// TokenSource yields the bearer token for billing (token.go).
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

type Client struct {
	http    *http.Client
	baseURL string
	tokens  TokenSource
}

func New(baseURL string, tokens TokenSource) *Client {
	return &Client{
		http:    &http.Client{Timeout: EntitlementsTimeout},
		baseURL: strings.TrimSuffix(baseURL, "/"),
		tokens:  tokens,
	}
}

// CustomerRef is billing's external reference for a person.
func CustomerRef(userID string) string { return "USER_" + userID }

// LevelKey is the report's idempotency key (spec § 4).
func LevelKey(ownerUserID, meter string, at time.Time) string {
	return fmt.Sprintf("lvl:%s:%s:%d", ownerUserID, meter, at.UnixMilli())
}

// Wire types: only the fields this service reads (billing dto.go:243-292).
type EntitlementItem struct {
	PriceID  string            `json:"price_id"`
	Metadata map[string]string `json:"metadata"`
}

type EntitlementSubscription struct {
	ID       string            `json:"id"`
	Status   string            `json:"status"`
	Entitled bool              `json:"entitled"`
	Plan     string            `json:"plan"`
	Items    []EntitlementItem `json:"items"`
}

type EntitlementDefault struct {
	PriceID  string            `json:"price_id"`
	Plan     string            `json:"plan"`
	Metadata map[string]string `json:"metadata"`
}

type Entitlements struct {
	Entitled      bool                      `json:"entitled"`
	Subscriptions []EntitlementSubscription `json:"subscriptions"`
	Default       *EntitlementDefault       `json:"default"`
}

type LevelReport struct {
	CustomerRef    string
	Meter          string
	Value          int64
	OccurredAt     time.Time
	IdempotencyKey string
}

// Entitlements reads a person's standing for one owner's products. One retry
// on a transport error or 5xx, inside EntitlementsTimeout; a 4xx is final.
func (c *Client) Entitlements(ctx context.Context, customerRef, ownerKey string) (*Entitlements, error) {
	ctx, cancel := context.WithTimeout(ctx, EntitlementsTimeout)
	defer cancel()
	q := url.Values{"customer_ref": {customerRef}, "owner_key": {ownerKey}}
	var lastErr error
	for attempt := 0; attempt < entitlementAttempts; attempt++ {
		var out Entitlements
		status, err := c.do(ctx, http.MethodGet, entitlementsPath+"?"+q.Encode(), "", nil, &out)
		if err == nil {
			return &out, nil
		}
		lastErr = err
		if (status != 0 && status < 500) || ctx.Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf("%w: %v", ErrUnavailable, lastErr)
}

// ReportLevel sends one level. A 409 idempotency_key_reused (same key, already
// recorded with another body) is treated as delivered: the next report carries
// the whole level. A 409 concurrent_update recorded nothing — another report
// moved the latest level first — so it is an error and the queue retries it.
func (c *Client) ReportLevel(ctx context.Context, r LevelReport) error {
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()
	body, err := json.Marshal(map[string]any{
		"customer_ref":    r.CustomerRef,
		"meter":           r.Meter,
		"value":           r.Value,
		"occurred_at":     r.OccurredAt.UTC().Format(time.RFC3339Nano),
		"idempotency_key": r.IdempotencyKey,
	})
	if err != nil {
		return err
	}
	status, err := c.do(ctx, http.MethodPost, levelsPath, r.IdempotencyKey, body, nil)
	if status == http.StatusConflict && !strings.Contains(err.Error(), `"concurrent_update"`) {
		return nil
	}
	return err
}

// do performs one call. status is 0 when no response arrived.
func (c *Client) do(ctx context.Context, method, path, idempotencyKey string, body []byte, out any) (int, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return 0, fmt.Errorf("billing token: %w", err)
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set(headerIdempotencyKey, idempotencyKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Billing's detail is for the log, never for a person on our screens.
		return resp.StatusCode, fmt.Errorf("billing %s %s: status %d: %.200s", method, path, resp.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decoding billing %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}
