// Package planlimit connects the organization domain to ctech-billing: it turns
// entitlements into quotas, and keeps billing told of every owner's levels
// through a small durable queue (docs/specs/2026-10-10-space-plan-limits.md).
package planlimit

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"gopkg.aoctech.app/api-commons/observability"

	"gopkg.aoctech.app/account/api/internal/billingclient"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

const (
	metaQuotaSpaces = "quota_spaces"
	metaQuotaPeople = "quota_people_per_space"
)

// QuotasFrom reads the plan from an entitlement answer (decision P4): the
// entitled subscription's item metadata, the most generous if several, else
// billing's default (Free). Neither → unavailable: that is a billing without
// owner_key support, and treating it as 0 would refuse everyone silently.
func QuotasFrom(ctx context.Context, e *billingclient.Entitlements) (organization.Quotas, error) {
	var chosen *organization.Quotas
	for _, sub := range e.Subscriptions {
		if !sub.Entitled {
			continue
		}
		meta := metadataOf(sub.Items)
		q := organization.Quotas{
			Plan:           sub.Plan,
			Spaces:         quota(ctx, meta, metaQuotaSpaces, sub.Plan),
			PeoplePerSpace: quota(ctx, meta, metaQuotaPeople, sub.Plan),
		}
		if chosen == nil || moreGenerous(q, *chosen) {
			chosen = &q
		}
	}
	if chosen != nil {
		return *chosen, nil
	}
	if e.Default == nil {
		return organization.Quotas{}, fmt.Errorf("%w: no entitled subscription and no default plan", organization.ErrPlanUnavailable)
	}
	return organization.Quotas{
		Plan:           e.Default.Plan,
		Spaces:         quota(ctx, e.Default.Metadata, metaQuotaSpaces, e.Default.Plan),
		PeoplePerSpace: quota(ctx, e.Default.Metadata, metaQuotaPeople, e.Default.Plan),
	}, nil
}

func metadataOf(items []billingclient.EntitlementItem) map[string]string {
	for _, it := range items {
		if _, ok := it.Metadata[metaQuotaSpaces]; ok {
			return it.Metadata
		}
	}
	if len(items) > 0 {
		return items[0].Metadata
	}
	return nil
}

// quota parses one value. Missing, not an integer, or below -1 → 0, logged: a
// catalogue mistake must not turn into unlimited spaces (spec § 1).
func quota(ctx context.Context, meta map[string]string, key, plan string) int64 {
	raw, ok := meta[key]
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if !ok || err != nil || v < organization.Unlimited {
		observability.Error(ctx, "plan limits: malformed quota in billing metadata, treating as 0", err,
			"key", key, "plan", plan, "value", raw)
		return 0
	}
	return v
}

func rank(limit int64) int64 {
	if limit == organization.Unlimited {
		return math.MaxInt64
	}
	return limit
}

func moreGenerous(a, b organization.Quotas) bool {
	if rank(a.Spaces) != rank(b.Spaces) {
		return rank(a.Spaces) > rank(b.Spaces)
	}
	return rank(a.PeoplePerSpace) > rank(b.PeoplePerSpace)
}
