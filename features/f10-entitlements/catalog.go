package entitlements

import (
	"errors"
	"slices"
)

const CatalogVersion = "d07-v1"

type PlanCode string

const (
	PlanStart PlanCode = "start"
	PlanPro   PlanCode = "pro"
	PlanTeam  PlanCode = "team"
)

type BillingInterval string

const (
	IntervalMonthly BillingInterval = "monthly"
	IntervalAnnual  BillingInterval = "annual"
)

type Resource string

const (
	ResourceMembers               Resource = "members"
	ResourceChannels              Resource = "channels"
	ResourceScheduledPublications Resource = "scheduled_publications"
)

type Money struct {
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
}

// PlanPrices is the price for the first channel. Paid plans use progressive
// PriceTiers; Start is permanently free.
type PlanPrices struct {
	Monthly Money `json:"monthly"`
	Annual  Money `json:"annual"`
}

type PriceTier struct {
	FromChannel int64 `json:"from_channel"`
	ToChannel   int64 `json:"to_channel"`
	Monthly     Money `json:"monthly"`
	Annual      Money `json:"annual"`
}

type PlanLimits struct {
	Members                         int64 `json:"members"`
	Channels                        int64 `json:"channels"`
	ScheduledPublications           int64 `json:"scheduled_publications"`
	ScheduledPublicationsPerChannel int64 `json:"scheduled_publications_per_channel"`
}

type TrialTerms struct {
	Days                            int64 `json:"days"`
	Members                         int64 `json:"members"`
	Channels                        int64 `json:"channels"`
	ScheduledPublicationsPerChannel int64 `json:"scheduled_publications_per_channel"`
}

type PublicPlan struct {
	Code        PlanCode    `json:"code"`
	Name        string      `json:"name"`
	Purchasable bool        `json:"purchasable"`
	Prices      PlanPrices  `json:"prices"`
	PriceTiers  []PriceTier `json:"price_tiers,omitempty"`
	Limits      PlanLimits  `json:"limits"`
	Trial       *TrialTerms `json:"trial,omitempty"`
}

var publicCatalog = []PublicPlan{
	{
		Code:        PlanStart,
		Name:        "Start",
		Purchasable: false,
		Prices: PlanPrices{
			Monthly: Money{AmountCents: 0, Currency: "EUR"},
			Annual:  Money{AmountCents: 0, Currency: "EUR"},
		},
		Limits: PlanLimits{
			Members:                         1,
			Channels:                        3,
			ScheduledPublications:           10,
			ScheduledPublicationsPerChannel: 10,
		},
	},
	{
		Code:        PlanPro,
		Name:        "Pro",
		Purchasable: true,
		Prices: PlanPrices{
			Monthly: Money{AmountCents: 450, Currency: "EUR"},
			Annual:  Money{AmountCents: 4500, Currency: "EUR"},
		},
		PriceTiers: paidTiers(450, 300, 225),
		Limits: PlanLimits{
			Members:                         1,
			Channels:                        50,
			ScheduledPublications:           500,
			ScheduledPublicationsPerChannel: 500,
		},
	},
	{
		Code:        PlanTeam,
		Name:        "Team",
		Purchasable: true,
		Prices: PlanPrices{
			Monthly: Money{AmountCents: 900, Currency: "EUR"},
			Annual:  Money{AmountCents: 9000, Currency: "EUR"},
		},
		PriceTiers: paidTiers(900, 300, 225),
		Limits: PlanLimits{
			Members:                         15,
			Channels:                        50,
			ScheduledPublications:           500,
			ScheduledPublicationsPerChannel: 500,
		},
		Trial: &TrialTerms{
			Days:                            14,
			Members:                         15,
			Channels:                        10,
			ScheduledPublicationsPerChannel: 500,
		},
	},
}

var (
	ErrUnknownPlan     = errors.New("unknown public plan")
	ErrInvalidInterval = errors.New("invalid billing interval")
	ErrFreePlan        = errors.New("free plan does not use Paddle checkout")
	ErrInvalidChannels = errors.New("channel quantity is outside plan limits")
)

func paidTiers(first, second, third int64) []PriceTier {
	return []PriceTier{
		{
			FromChannel: 1,
			ToChannel:   10,
			Monthly:     Money{AmountCents: first, Currency: "EUR"},
			Annual:      Money{AmountCents: first * 10, Currency: "EUR"},
		},
		{
			FromChannel: 11,
			ToChannel:   25,
			Monthly:     Money{AmountCents: second, Currency: "EUR"},
			Annual:      Money{AmountCents: second * 10, Currency: "EUR"},
		},
		{
			FromChannel: 26,
			ToChannel:   50,
			Monthly:     Money{AmountCents: third, Currency: "EUR"},
			Annual:      Money{AmountCents: third * 10, Currency: "EUR"},
		},
	}
}

func PublicPlans() []PublicPlan {
	plans := slices.Clone(publicCatalog)
	for index := range plans {
		plans[index].PriceTiers = slices.Clone(plans[index].PriceTiers)
		if plans[index].Trial != nil {
			trial := *plans[index].Trial
			plans[index].Trial = &trial
		}
	}
	return plans
}

func PublicPlanByCode(code PlanCode) (PublicPlan, error) {
	for _, plan := range PublicPlans() {
		if plan.Code == code {
			return plan, nil
		}
	}
	return PublicPlan{}, ErrUnknownPlan
}

func PriceForChannels(
	planCode PlanCode,
	interval BillingInterval,
	channels int64,
) (Money, error) {
	plan, err := PublicPlanByCode(planCode)
	if err != nil {
		return Money{}, err
	}
	if !validInterval(interval) {
		return Money{}, ErrInvalidInterval
	}
	if channels < 1 || channels > plan.Limits.Channels {
		return Money{}, ErrInvalidChannels
	}
	if !plan.Purchasable {
		return Money{Currency: "EUR"}, nil
	}
	var total int64
	for _, tier := range plan.PriceTiers {
		quantity := tierQuantity(channels, tier.FromChannel, tier.ToChannel)
		if quantity == 0 {
			continue
		}
		unit := tier.Monthly.AmountCents
		if interval == IntervalAnnual {
			unit = tier.Annual.AmountCents
		}
		total += quantity * unit
	}
	return Money{AmountCents: total, Currency: "EUR"}, nil
}

func tierQuantity(channels, from, to int64) int64 {
	if channels < from {
		return 0
	}
	last := min(channels, to)
	return last - from + 1
}

func validInterval(interval BillingInterval) bool {
	return interval == IntervalMonthly || interval == IntervalAnnual
}

func validResource(resource Resource) bool {
	return resource == ResourceMembers ||
		resource == ResourceChannels ||
		resource == ResourceScheduledPublications
}
