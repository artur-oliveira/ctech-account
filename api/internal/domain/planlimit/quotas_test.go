package planlimit

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"gopkg.aoctech.app/account/api/internal/billingclient"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func sub(entitled bool, plan string, meta map[string]string) billingclient.EntitlementSubscription {
	return billingclient.EntitlementSubscription{Entitled: entitled, Plan: plan, Items: []billingclient.EntitlementItem{{PriceID: "p", Metadata: meta}}}
}

func TestTheEntitledSubscriptionWins(t *testing.T) {
	q, err := QuotasFrom(context.Background(), &billingclient.Entitlements{
		Subscriptions: []billingclient.EntitlementSubscription{
			sub(false, "pro", map[string]string{"quota_spaces": "10", "quota_people_per_space": "10"}),
			sub(true, "basic", map[string]string{"quota_spaces": "3", "quota_people_per_space": "5"}),
		},
		Default: &billingclient.EntitlementDefault{Plan: "free", Metadata: map[string]string{"quota_spaces": "1", "quota_people_per_space": "1"}},
	})
	if err != nil || q != (organization.Quotas{Plan: "basic", Spaces: 3, PeoplePerSpace: 5}) {
		t.Fatalf("q = %+v, %v", q, err)
	}
}

func TestNoEntitledSubscriptionReadsTheDefault(t *testing.T) {
	q, err := QuotasFrom(context.Background(), &billingclient.Entitlements{
		Default: &billingclient.EntitlementDefault{Plan: "free", Metadata: map[string]string{"quota_spaces": "1", "quota_people_per_space": "1"}},
	})
	if err != nil || q != (organization.Quotas{Plan: "free", Spaces: 1, PeoplePerSpace: 1}) {
		t.Fatalf("q = %+v, %v", q, err)
	}
}

// Sob demanda bills two prices; both carry the -1/-1 quotas.
func TestOnDemandIsUnlimited(t *testing.T) {
	unlimited := map[string]string{"quota_spaces": "-1", "quota_people_per_space": "-1"}
	q, _ := QuotasFrom(context.Background(), &billingclient.Entitlements{Subscriptions: []billingclient.EntitlementSubscription{{
		Entitled: true, Plan: "ondemand",
		Items: []billingclient.EntitlementItem{{PriceID: "price_finance_ondemand_spaces", Metadata: unlimited}, {PriceID: "price_finance_ondemand_people", Metadata: unlimited}},
	}}})
	if q.Spaces != organization.Unlimited || q.PeoplePerSpace != organization.Unlimited {
		t.Fatalf("q = %+v", q)
	}
}

func TestTwoEntitledSubscriptionsTakeTheMoreGenerous(t *testing.T) {
	q, _ := QuotasFrom(context.Background(), &billingclient.Entitlements{Subscriptions: []billingclient.EntitlementSubscription{
		sub(true, "basic", map[string]string{"quota_spaces": "3", "quota_people_per_space": "5"}),
		sub(true, "pro", map[string]string{"quota_spaces": "10", "quota_people_per_space": "10"}),
	}})
	if q.Plan != "pro" {
		t.Fatalf("q = %+v", q)
	}
}

// Spec test 10: a catalogue mistake refuses growth, never grants unlimited.
func TestMalformedQuotaIsZeroAndLogged(t *testing.T) {
	logs := captureLogs(t)
	q, err := QuotasFrom(context.Background(), &billingclient.Entitlements{Subscriptions: []billingclient.EntitlementSubscription{
		sub(true, "basic", map[string]string{"quota_spaces": "three", "quota_people_per_space": "-7"}),
	}})
	if err != nil || q.Spaces != 0 || q.PeoplePerSpace != 0 {
		t.Fatalf("q = %+v, %v", q, err)
	}
	if !strings.Contains(logs.String(), "quota_spaces") || !strings.Contains(logs.String(), "quota_people_per_space") {
		t.Fatalf("not logged: %s", logs)
	}
	q, _ = QuotasFrom(context.Background(), &billingclient.Entitlements{Subscriptions: []billingclient.EntitlementSubscription{sub(true, "basic", nil)}})
	if q.Spaces != 0 || q.PeoplePerSpace != 0 {
		t.Fatalf("missing metadata: %+v", q)
	}
}

func TestNoEntitlementAndNoDefaultIsUnavailable(t *testing.T) {
	if _, err := QuotasFrom(context.Background(), &billingclient.Entitlements{}); !errors.Is(err, organization.ErrPlanUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
