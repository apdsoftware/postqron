package entitlements

import (
	"errors"
	"slices"
)

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

type PlanPrices struct {
	Monthly Money `json:"monthly"`
	Annual  Money `json:"annual"`
}

type PlanLimits struct {
	Members               int64 `json:"members"`
	Channels              int64 `json:"channels"`
	ScheduledPublications int64 `json:"scheduled_publications"`
}

type PublicPlan struct {
	Code   PlanCode   `json:"code"`
	Name   string     `json:"name"`
	Prices PlanPrices `json:"prices"`
	Limits PlanLimits `json:"limits"`
}

var publicCatalog = []PublicPlan{
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

var (
	ErrUnknownPlan     = errors.New("unknown public plan")
	ErrInvalidInterval = errors.New("invalid billing interval")
)

func PublicPlans() []PublicPlan {
	return slices.Clone(publicCatalog)
}

func PublicPlanByCode(code PlanCode) (PublicPlan, error) {
	for _, plan := range publicCatalog {
		if plan.Code == code {
			return plan, nil
		}
	}
	return PublicPlan{}, ErrUnknownPlan
}

func validInterval(interval BillingInterval) bool {
	return interval == IntervalMonthly || interval == IntervalAnnual
}

func validResource(resource Resource) bool {
	return resource == ResourceMembers ||
		resource == ResourceChannels ||
		resource == ResourceScheduledPublications
}
