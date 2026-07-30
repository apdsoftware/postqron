package entitlements

import (
	"errors"
	"fmt"
	"testing"
)

func TestPlanChangeDirectionMatrix(t *testing.T) {
	plans := []struct {
		code     PlanCode
		channels *int64
	}{
		{PlanStart, limit(3)},
		{PlanPro, limit(6)},
		{PlanTeam, limit(9)},
		{PlanUnlimited, nil},
	}
	for sourceIndex, source := range plans {
		for targetIndex, target := range plans {
			name := fmt.Sprintf("%s_to_%s", source.code, target.code)
			t.Run(name, func(t *testing.T) {
				direction, err := classifyChange(
					BillingBinding{
						Plan: source.code, Interval: IntervalMonthly,
						Channels: source.channels,
					},
					SubscriptionChangeRequest{
						Plan: target.code, Interval: IntervalMonthly,
						Channels: target.channels,
					},
				)
				if sourceIndex == targetIndex {
					if !errors.Is(err, ErrNoSubscriptionChange) {
						t.Fatalf("error = %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				want := ChangeUpgrade
				if sourceIndex > targetIndex {
					want = ChangeDowngrade
				}
				if direction != want {
					t.Fatalf("direction = %s, want %s", direction, want)
				}
			})
		}
	}
}

func TestPlanChangeClassifiesQuantityAndBillingCadence(t *testing.T) {
	tests := []struct {
		name    string
		source  BillingBinding
		target  SubscriptionChangeRequest
		want    ChangeDirection
		wantErr error
	}{
		{
			name: "quantity upgrade",
			source: BillingBinding{
				Plan: PlanPro, Interval: IntervalMonthly, Channels: limit(2),
			},
			target: SubscriptionChangeRequest{
				Plan: PlanPro, Interval: IntervalMonthly, Channels: limit(3),
			},
			want: ChangeUpgrade,
		},
		{
			name: "quantity downgrade",
			source: BillingBinding{
				Plan: PlanTeam, Interval: IntervalAnnual, Channels: limit(9),
			},
			target: SubscriptionChangeRequest{
				Plan: PlanTeam, Interval: IntervalAnnual, Channels: limit(8),
			},
			want: ChangeDowngrade,
		},
		{
			name: "monthly to annual",
			source: BillingBinding{
				Plan: PlanPro, Interval: IntervalMonthly, Channels: limit(4),
			},
			target: SubscriptionChangeRequest{
				Plan: PlanPro, Interval: IntervalAnnual, Channels: limit(4),
			},
			want: ChangeUpgrade,
		},
		{
			name: "annual to monthly",
			source: BillingBinding{
				Plan: PlanTeam, Interval: IntervalAnnual, Channels: limit(7),
			},
			target: SubscriptionChangeRequest{
				Plan: PlanTeam, Interval: IntervalMonthly, Channels: limit(7),
			},
			want: ChangeDowngrade,
		},
		{
			name: "mixed dimensions fail closed",
			source: BillingBinding{
				Plan: PlanPro, Interval: IntervalMonthly, Channels: limit(6),
			},
			target: SubscriptionChangeRequest{
				Plan: PlanTeam, Interval: IntervalMonthly, Channels: limit(5),
			},
			wantErr: ErrMixedSubscriptionChange,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := classifyChange(test.source, test.target)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Fatalf("direction = %s, want %s", got, test.want)
			}
		})
	}
}

func TestDowngradeGuardExactBoundariesAndEachOverage(t *testing.T) {
	target := SubscriptionChangeTarget{
		Plan: PlanTeam, Interval: IntervalMonthly, Channels: limit(9),
	}
	exact := []Usage{
		{Resource: ResourceMembers, Used: 6},
		{Resource: ResourceChannels, Used: 9},
		{Resource: ResourceScheduledPublications, Used: 500},
	}
	overages, err := downgradeOverages(target, exact)
	if err != nil {
		t.Fatal(err)
	}
	if len(overages) != 0 {
		t.Fatalf("exact limits were blocked: %#v", overages)
	}

	for index, resource := range []Resource{
		ResourceMembers,
		ResourceChannels,
		ResourceScheduledPublications,
	} {
		usage := append([]Usage(nil), exact...)
		usage[index].Used++
		overages, err := downgradeOverages(target, usage)
		if err != nil {
			t.Fatal(err)
		}
		if len(overages) != 1 ||
			overages[0].Resource != resource ||
			overages[0].Excess != 1 {
			t.Fatalf("%s overages = %#v", resource, overages)
		}
	}
}
