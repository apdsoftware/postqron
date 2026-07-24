package entitlements

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicCatalogMatchesD03(t *testing.T) {
	plans := PublicPlans()
	if len(plans) != 3 {
		t.Fatalf("PublicPlans() returned %d plans, want 3", len(plans))
	}

	expected := []PublicPlan{
		{
			Code: PlanStart,
			Name: "Start",
			Prices: PlanPrices{
				Monthly: Money{AmountCents: 900, Currency: "EUR"},
				Annual:  Money{AmountCents: 9000, Currency: "EUR"},
			},
			Limits: PlanLimits{
				Members:               1,
				Channels:              5,
				ScheduledPublications: 100,
			},
		},
		{
			Code: PlanPro,
			Name: "Pro",
			Prices: PlanPrices{
				Monthly: Money{AmountCents: 2400, Currency: "EUR"},
				Annual:  Money{AmountCents: 24000, Currency: "EUR"},
			},
			Limits: PlanLimits{
				Members:               5,
				Channels:              15,
				ScheduledPublications: 500,
			},
		},
		{
			Code: PlanTeam,
			Name: "Team",
			Prices: PlanPrices{
				Monthly: Money{AmountCents: 4900, Currency: "EUR"},
				Annual:  Money{AmountCents: 49000, Currency: "EUR"},
			},
			Limits: PlanLimits{
				Members:               15,
				Channels:              50,
				ScheduledPublications: 2000,
			},
		},
	}
	for index := range expected {
		if plans[index] != expected[index] {
			t.Fatalf("plan %d = %#v, want %#v", index, plans[index], expected[index])
		}
	}
}

func TestPublicCatalogDoesNotExposePrivateEntitlement(t *testing.T) {
	payload, err := json.Marshal(PublicPlans())
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(payload))
	for _, forbidden := range []string{"internal", "unlimited", "override"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("public catalog contains forbidden term %q: %s", forbidden, payload)
		}
	}
	if _, err := PublicPlanByCode(PlanCode("internal")); err != ErrUnknownPlan {
		t.Fatalf("private plan lookup error = %v, want ErrUnknownPlan", err)
	}
}

func TestPublicPlansReturnsIndependentSlice(t *testing.T) {
	plans := PublicPlans()
	plans[0].Name = "Changed"
	if PublicPlans()[0].Name != "Start" {
		t.Fatal("PublicPlans() exposed mutable catalog storage")
	}
}
