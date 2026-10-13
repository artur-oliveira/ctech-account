package erasurepub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/api-commons/erasure"
)

const (
	eligibilityTimeout   = 5 * time.Second
	eligibilityPath      = "/internal/erasure/eligibility/"
	eligibilityScopeFmt  = "internal:%s:erasure-eligibility"
	workspacesQueryParam = "workspaces"
)

// TokenMinter signs a short-lived service token for audience with scope.
// ctech-account is the issuer, so it mints instead of calling its own /token.
type TokenMinter func(audience, scope string) (string, error)

// Eligibility asks every participant for blockers (saga protocol §4.1).
type Eligibility struct {
	http         *http.Client
	participants []config.ErasureParticipant
	mint         TokenMinter
}

func NewEligibility(httpClient *http.Client, participants []config.ErasureParticipant, mint TokenMinter) *Eligibility {
	return &Eligibility{http: httpClient, participants: participants, mint: mint}
}

// Check returns every participant's blockers. workspaceIDs are the workspaces
// where the person is the only member. Any failure is an error, never
// "eligible": a participant we cannot hear from may hold the user's money.
func (e *Eligibility) Check(ctx context.Context, userID string, workspaceIDs []string) ([]erasure.Blocker, error) {
	var all []erasure.Blocker
	for _, p := range e.participants {
		blockers, err := e.checkOne(ctx, p, userID, workspaceIDs)
		if err != nil {
			return nil, fmt.Errorf("eligibility of %s: %w", p.Service, err)
		}
		all = append(all, blockers...)
	}
	return all, nil
}

func (e *Eligibility) checkOne(ctx context.Context, p config.ErasureParticipant, userID string, workspaceIDs []string) ([]erasure.Blocker, error) {
	ctx, cancel := context.WithTimeout(ctx, eligibilityTimeout)
	defer cancel()
	token, err := e.mint(p.Audience, fmt.Sprintf(eligibilityScopeFmt, p.Service))
	if err != nil {
		return nil, err
	}
	target := p.URL + eligibilityPath + url.PathEscape(userID)
	if len(workspaceIDs) > 0 {
		target += "?" + workspacesQueryParam + "=" + url.QueryEscape(strings.Join(workspaceIDs, ","))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var el erasure.Eligibility
	if err := json.NewDecoder(resp.Body).Decode(&el); err != nil {
		return nil, err
	}
	return el.Blockers, nil
}
