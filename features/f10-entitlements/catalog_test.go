package entitlements

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func testPaddleCatalog() PaddleCatalog {
	catalog := make(PaddleCatalog, 14)
	index := 1
	for planIndex, plan := range []PlanCode{PlanPro, PlanTeam, PlanUnlimited} {
		productID := fmt.Sprintf("pro_%026d", planIndex+1)
		publicPlan, _ := PublicPlanByCode(plan)
		for _, interval := range []BillingInterval{IntervalMonthly, IntervalAnnual} {
			tiers := []PriceTierCode{TierOne, TierTwo, TierThree}
			if plan == PlanUnlimited {
				tiers = []PriceTierCode{TierFlat}
			}
			for tierIndex, tier := range tiers {
				var amount int64
				if plan == PlanUnlimited {
					amount = publicPlan.Prices.Monthly.AmountCents
					if interval == IntervalAnnual {
						amount = publicPlan.Prices.Annual.AmountCents
					}
				} else {
					amount = publicPlan.PriceTiers[tierIndex].Monthly.AmountCents
					if interval == IntervalAnnual {
						amount = publicPlan.PriceTiers[tierIndex].Annual.AmountCents
					}
				}
				key := PaddlePriceKey{Plan: plan, Interval: interval, Tier: tier}
				catalog[key] = PaddlePriceMapping{
					Plan:            plan,
					Interval:        interval,
					Tier:            tier,
					ProductID:       productID,
					PriceID:         fmt.Sprintf("pri_%026d", index),
					UnitAmountCents: amount,
				}
				index++
			}
		}
	}
	return catalog
}

func TestPublicCatalogMatchesD09(t *testing.T) {
	plans := PublicPlans()
	if len(plans) != 4 {
		t.Fatalf("PublicPlans() returned %d plans, want 4", len(plans))
	}
	start, pro, team, unlimited := plans[0], plans[1], plans[2], plans[3]
	if start.Code != PlanStart || start.Purchasable ||
		start.Prices.Monthly.AmountCents != 0 ||
		*start.Limits.Members != 1 ||
		*start.Limits.Channels != 3 ||
		*start.Limits.ScheduledPublicationsPerChannel != 10 {
		t.Fatalf("Start = %#v", start)
	}
	if pro.Code != PlanPro || !pro.Purchasable ||
		*pro.Limits.Members != 1 || *pro.Limits.Channels != 6 ||
		*pro.Limits.ScheduledPublicationsPerChannel != 500 {
		t.Fatalf("Pro = %#v", pro)
	}
	if team.Code != PlanTeam || !team.Purchasable ||
		*team.Limits.Members != 9 || *team.Limits.Channels != 9 ||
		team.Trial == nil || team.Trial.Days != 14 ||
		team.Trial.Channels != 9 || team.Trial.Members != 9 {
		t.Fatalf("Team = %#v", team)
	}
	if unlimited.Code != PlanUnlimited || !unlimited.Purchasable ||
		unlimited.Prices.Monthly.AmountCents != 12900 ||
		unlimited.Prices.Annual.AmountCents != 129000 ||
		unlimited.Limits.Members != nil ||
		unlimited.Limits.Channels != nil ||
		unlimited.Limits.ScheduledPublications != nil ||
		unlimited.Limits.ScheduledPublicationsPerChannel != nil {
		t.Fatalf("Unlimited = %#v", unlimited)
	}
	tests := []struct {
		plan     PlanCode
		interval BillingInterval
		channels *int64
		want     int64
	}{
		{PlanStart, IntervalMonthly, limit(3), 0},
		{PlanPro, IntervalMonthly, limit(1), 450},
		{PlanPro, IntervalMonthly, limit(6), 2700},
		{PlanPro, IntervalAnnual, limit(6), 27000},
		{PlanTeam, IntervalMonthly, limit(1), 900},
		{PlanTeam, IntervalMonthly, limit(9), 8100},
		{PlanTeam, IntervalAnnual, limit(9), 81000},
		{PlanUnlimited, IntervalMonthly, nil, 12900},
		{PlanUnlimited, IntervalAnnual, nil, 129000},
	}
	for _, test := range tests {
		got, err := PriceForChannels(test.plan, test.interval, test.channels)
		if err != nil || got.AmountCents != test.want || got.Currency != "EUR" {
			t.Fatalf("%s/%s/%v = %#v, %v", test.plan, test.interval, test.channels, got, err)
		}
	}
}

func TestD09PriceBoundariesAndAnnualTotals(t *testing.T) {
	for _, test := range []struct {
		plan     PlanCode
		channels *int64
	}{
		{PlanPro, limit(1)},
		{PlanPro, limit(6)},
		{PlanTeam, limit(1)},
		{PlanTeam, limit(9)},
		{PlanUnlimited, nil},
	} {
		monthly, err := PriceForChannels(test.plan, IntervalMonthly, test.channels)
		if err != nil {
			t.Fatal(err)
		}
		annual, err := PriceForChannels(test.plan, IntervalAnnual, test.channels)
		if err != nil {
			t.Fatal(err)
		}
		if annual.AmountCents != monthly.AmountCents*10 {
			t.Fatalf("%s annual = %d, want 10 * %d", test.plan, annual.AmountCents, monthly.AmountCents)
		}
	}
	for _, test := range []struct {
		plan     PlanCode
		channels *int64
	}{
		{PlanPro, limit(7)},
		{PlanTeam, limit(10)},
		{PlanUnlimited, limit(1)},
	} {
		if _, err := PriceForChannels(test.plan, IntervalMonthly, test.channels); err != ErrInvalidChannels {
			t.Fatalf("%s/%v error = %v, want ErrInvalidChannels", test.plan, test.channels, err)
		}
	}
}

func TestPaddleCatalogHasExactDistinctD09Mappings(t *testing.T) {
	catalog := testPaddleCatalog()
	if err := catalog.Validate(); err != nil {
		t.Fatal(err)
	}
	items, err := catalog.ExpectedItems(PlanPro, IntervalMonthly, limit(6))
	if err != nil {
		t.Fatal(err)
	}
	want := []PaddleItem{
		{PriceID: "pri_00000000000000000000000001", Quantity: 6},
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("items = %#v, want %#v", items, want)
	}
	unlimitedItems, err := catalog.ExpectedItems(PlanUnlimited, IntervalAnnual, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unlimitedItems) != 1 || unlimitedItems[0].Quantity != 1 {
		t.Fatalf("Unlimited items = %#v", unlimitedItems)
	}
	resolvedPlan, resolvedInterval, resolvedChannels, ok := catalog.ResolveItems(
		unlimitedItems,
	)
	if !ok || resolvedPlan != PlanUnlimited ||
		resolvedInterval != IntervalAnnual || resolvedChannels != nil {
		t.Fatalf(
			"resolved Unlimited = %s/%s/%v/%v",
			resolvedPlan,
			resolvedInterval,
			resolvedChannels,
			ok,
		)
	}
	duplicate := catalog[PaddlePriceKey{
		Plan: PlanTeam, Interval: IntervalAnnual, Tier: TierThree,
	}]
	duplicate.PriceID = catalog[PaddlePriceKey{
		Plan: PlanPro, Interval: IntervalMonthly, Tier: TierOne,
	}].PriceID
	catalog[PaddlePriceKey{
		Plan: PlanTeam, Interval: IntervalAnnual, Tier: TierThree,
	}] = duplicate
	if err := catalog.Validate(); err == nil {
		t.Fatal("duplicate Paddle price was accepted")
	}
}

func TestPublicCatalogDoesNotExposePrivateEntitlement(t *testing.T) {
	payload, err := json.Marshal(PublicPlans())
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(payload))
	if !strings.Contains(lower, `"code":"unlimited"`) {
		t.Fatalf("public catalog omits Unlimited: %s", payload)
	}
	for _, forbidden := range []string{
		"internal_unlimited",
		"internal entitlement",
		"override",
		"allowlist",
		"assignment_reason",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("public catalog contains forbidden term %q: %s", forbidden, payload)
		}
	}
	if _, err := PublicPlanByCode(PlanCode("internal")); err != ErrUnknownPlan {
		t.Fatalf("private plan lookup error = %v, want ErrUnknownPlan", err)
	}
}

func TestPublicPlansReturnsIndependentData(t *testing.T) {
	plans := PublicPlans()
	plans[1].Name = "Changed"
	plans[1].PriceTiers[0].Monthly.AmountCents = 1
	*plans[1].Limits.Channels = 1
	if PublicPlans()[1].Name != "Pro" ||
		PublicPlans()[1].PriceTiers[0].Monthly.AmountCents != 450 ||
		*PublicPlans()[1].Limits.Channels != 6 {
		t.Fatal("PublicPlans() exposed mutable catalog storage")
	}
}
