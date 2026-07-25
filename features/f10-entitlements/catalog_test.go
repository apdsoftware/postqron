package entitlements

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func testPaddleCatalog() PaddleCatalog {
	catalog := make(PaddleCatalog, 12)
	index := 1
	for planIndex, plan := range []PlanCode{PlanPro, PlanTeam} {
		productID := fmt.Sprintf("pro_%026d", planIndex+1)
		publicPlan, _ := PublicPlanByCode(plan)
		for _, interval := range []BillingInterval{IntervalMonthly, IntervalAnnual} {
			for tierIndex, tier := range []PriceTierCode{TierOne, TierTwo, TierThree} {
				amount := publicPlan.PriceTiers[tierIndex].Monthly.AmountCents
				if interval == IntervalAnnual {
					amount = publicPlan.PriceTiers[tierIndex].Annual.AmountCents
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

func TestPublicCatalogMatchesD07(t *testing.T) {
	plans := PublicPlans()
	if len(plans) != 3 {
		t.Fatalf("PublicPlans() returned %d plans, want 3", len(plans))
	}
	start, pro, team := plans[0], plans[1], plans[2]
	if start.Code != PlanStart || start.Purchasable ||
		start.Prices.Monthly.AmountCents != 0 ||
		start.Limits.Members != 1 ||
		start.Limits.Channels != 3 ||
		start.Limits.ScheduledPublicationsPerChannel != 10 {
		t.Fatalf("Start = %#v", start)
	}
	if pro.Code != PlanPro || !pro.Purchasable ||
		pro.Limits.Members != 1 || pro.Limits.Channels != 50 ||
		pro.Limits.ScheduledPublicationsPerChannel != 500 {
		t.Fatalf("Pro = %#v", pro)
	}
	if team.Code != PlanTeam || !team.Purchasable ||
		team.Limits.Members != 15 ||
		team.Trial == nil || team.Trial.Days != 14 || team.Trial.Channels != 10 {
		t.Fatalf("Team = %#v", team)
	}
	tests := []struct {
		plan     PlanCode
		interval BillingInterval
		channels int64
		want     int64
	}{
		{PlanPro, IntervalMonthly, 10, 4500},
		{PlanPro, IntervalMonthly, 25, 9000},
		{PlanPro, IntervalAnnual, 50, 146250},
		{PlanTeam, IntervalMonthly, 25, 13500},
		{PlanTeam, IntervalAnnual, 50, 191250},
	}
	for _, test := range tests {
		got, err := PriceForChannels(test.plan, test.interval, test.channels)
		if err != nil || got.AmountCents != test.want || got.Currency != "EUR" {
			t.Fatalf("%s/%s/%d = %#v, %v", test.plan, test.interval, test.channels, got, err)
		}
	}
}

func TestPaddleCatalogHasExactDistinctD07Mappings(t *testing.T) {
	catalog := testPaddleCatalog()
	if err := catalog.Validate(); err != nil {
		t.Fatal(err)
	}
	items, err := catalog.ExpectedItems(PlanPro, IntervalMonthly, 26)
	if err != nil {
		t.Fatal(err)
	}
	want := []PaddleItem{
		{PriceID: "pri_00000000000000000000000001", Quantity: 10},
		{PriceID: "pri_00000000000000000000000002", Quantity: 15},
		{PriceID: "pri_00000000000000000000000003", Quantity: 1},
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("items = %#v, want %#v", items, want)
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
	for _, forbidden := range []string{"internal", "unlimited", "override"} {
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
	if PublicPlans()[1].Name != "Pro" ||
		PublicPlans()[1].PriceTiers[0].Monthly.AmountCents != 450 {
		t.Fatal("PublicPlans() exposed mutable catalog storage")
	}
}
