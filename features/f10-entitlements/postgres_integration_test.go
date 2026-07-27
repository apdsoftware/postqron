package entitlements

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresAtomicLimitsLifecycleAndPublicIsolation(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	workspaceID := "46c847c5-621f-4c2a-a672-bdfeb2f9aa29"
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)

	store := NewSQLStore(pool)
	trialCreated, err := store.ProvisionTrial(ctx, workspaceID, now)
	if err != nil {
		t.Fatal(err)
	}
	if !trialCreated {
		t.Fatal("trial was not provisioned")
	}
	var trialEnds time.Time
	if err := pool.QueryRow(
		ctx,
		`SELECT trial_ends_at
		   FROM f10_workspace_billing
		  WHERE workspace_id = $1`,
		workspaceID,
	).Scan(&trialEnds); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(14 * 24 * time.Hour); !trialEnds.Equal(want) {
		t.Fatalf("Team trial ends at %s, want %s", trialEnds, want)
	}
	trialCreated, err = store.ProvisionTrial(ctx, workspaceID, now.Add(time.Hour))
	if err != nil || trialCreated {
		t.Fatalf("second trial provisioning = %v, %v", trialCreated, err)
	}

	service := NewService(store)
	service.now = func() time.Time { return now.Add(time.Hour) }

	var accepted atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < 30; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			result, err := service.Reserve(
				ctx,
				workspaceID,
				ResourceChannels,
				1,
				fmt.Sprintf("channel:%02d", index),
			)
			if err != nil {
				t.Errorf("concurrent Reserve(%d): %v", index, err)
				return
			}
			if result.Accepted {
				accepted.Add(1)
			}
		}(index)
	}
	wait.Wait()
	if accepted.Load() != 9 {
		t.Fatalf("accepted reservations = %d, want Team trial limit 9", accepted.Load())
	}

	overview, err := service.GetOverview(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	channels := usageFor(t, overview, ResourceChannels)
	if channels.Used != 9 || channels.Remaining == nil ||
		*channels.Remaining != 0 || channels.OverLimit {
		t.Fatalf("channel usage after concurrency = %#v", channels)
	}
	members := usageFor(t, overview, ResourceMembers)
	if members.Limit == nil || *members.Limit != 9 {
		t.Fatalf("Team trial member limit = %#v, want 9", members.Limit)
	}
	publications := usageFor(t, overview, ResourceScheduledPublications)
	if publications.Limit == nil || *publications.Limit != 500 {
		t.Fatalf(
			"Team trial scheduled-publication limit = %#v, want 500",
			publications.Limit,
		)
	}

	for index := 0; index < 30; index++ {
		if _, err := service.Reserve(
			ctx,
			workspaceID,
			ResourceChannels,
			1,
			fmt.Sprintf("channel:%02d", index),
		); err != nil {
			t.Fatalf("replay %d: %v", index, err)
		}
	}
	overview, err = service.GetOverview(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if got := usageFor(t, overview, ResourceChannels).Used; got != 9 {
		t.Fatalf("usage after idempotent replay = %d, want 9", got)
	}

	if _, err := pool.Exec(
		ctx,
		`UPDATE f10_workspace_billing
		    SET plan_code = 'start',
		        billing_state = 'active',
		        channel_quantity = 3,
		        updated_at = $2
		  WHERE workspace_id = $1`,
		workspaceID,
		now.Add(2*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	overview, err = service.GetOverview(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	channels = usageFor(t, overview, ResourceChannels)
	if channels.Used != 9 || channels.Remaining == nil ||
		*channels.Remaining != 0 || !channels.OverLimit {
		t.Fatalf("downgrade did not preserve overage: %#v", channels)
	}

	release, err := service.Release(
		ctx,
		workspaceID,
		ResourceChannels,
		1,
		"disconnect:channel-00",
	)
	if err != nil || !release.Accepted || release.Usage.Used != 8 {
		t.Fatalf("release during overage = %#v, %v", release, err)
	}
	denied, err := service.Reserve(
		ctx,
		workspaceID,
		ResourceChannels,
		1,
		"connect:blocked-after-downgrade",
	)
	if err != nil {
		t.Fatal(err)
	}
	if denied.Accepted || denied.Code != "limit_reached" || denied.Usage.Used != 8 {
		t.Fatalf("over-limit addition = %#v", denied)
	}

	if _, err := pool.Exec(
		ctx,
		`INSERT INTO f10_internal_entitlement_overrides (
		     workspace_id, active, assigned_at
		 ) VALUES ($1, true, $2)`,
		workspaceID,
		now,
	); err != nil {
		t.Fatal(err)
	}
	overrideDecision, err := service.Reserve(
		ctx,
		workspaceID,
		ResourceChannels,
		20,
		"server-authorized-capacity",
	)
	if err != nil || !overrideDecision.Accepted {
		t.Fatalf("server override decision = %#v, %v", overrideDecision, err)
	}
	overview, err = service.GetOverview(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(overview)
	if err != nil {
		t.Fatal(err)
	}
	lowerPayload := strings.ToLower(string(payload))
	for _, forbidden := range []string{
		"internal_unlimited",
		"override",
		"allowlist",
		"assignment_reason",
	} {
		if strings.Contains(lowerPayload, forbidden) {
			t.Fatalf("public overview exposed %q: %s", forbidden, payload)
		}
	}
	if _, err := pool.Exec(
		ctx,
		`UPDATE f10_workspace_billing
		    SET plan_code = 'team',
		        billing_state = 'trialing',
		        channel_quantity = 9,
		        updated_at = $2
		  WHERE workspace_id = $1`,
		workspaceID,
		now.Add(3*time.Hour),
	); err != nil {
		t.Fatal(err)
	}

	testBillingLifecycle(t, pool, store, workspaceID, now)
	testUnlimitedEntitlementAndConservativeDowngrade(t, pool, store, now)
	testMonthlyQuotaWindow(t, pool)
}

func testUnlimitedEntitlementAndConservativeDowngrade(
	t *testing.T,
	pool *pgxpool.Pool,
	store *SQLStore,
	now time.Time,
) {
	t.Helper()
	ctx := context.Background()
	workspaceID := "7a870d8f-4647-43ac-b81d-54e58f1af011"
	registration := CheckoutRegistration{
		SessionID:      "txn_00000000000000000000000002",
		WorkspaceID:    workspaceID,
		Plan:           PlanUnlimited,
		Interval:       IntervalMonthly,
		Channels:       nil,
		CatalogVersion: CatalogVersion,
		Items: []PaddleItem{
			{PriceID: "pri_00000000000000000000000013", Quantity: 1},
		},
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
	}
	if err := store.RegisterCheckout(ctx, registration); err != nil {
		t.Fatal(err)
	}
	paid := BillingEvent{
		ID:             "evt_unlimited_paid_f10",
		Type:           "transaction.completed",
		OccurredAt:     now.Add(time.Minute),
		WorkspaceID:    workspaceID,
		Plan:           PlanUnlimited,
		Interval:       IntervalMonthly,
		Channels:       nil,
		State:          StateActive,
		CustomerID:     "cus_unlimited_f10",
		SubscriptionID: "sub_unlimited_f10",
		TransactionID:  registration.SessionID,
		ApplyState:     true,
		Period: Period{
			Start: now,
			End:   now.AddDate(0, 1, 0),
		},
	}
	if result, err := store.ApplyBillingEvent(ctx, paid); err != nil ||
		!result.FirstDelivery || !result.StateChanged {
		t.Fatalf("Unlimited payment = %#v, %v", result, err)
	}

	service := NewService(store)
	service.now = func() time.Time { return now.Add(2 * time.Minute) }
	for _, resource := range []Resource{
		ResourceMembers,
		ResourceChannels,
		ResourceScheduledPublications,
	} {
		decision, err := service.Reserve(
			ctx,
			workspaceID,
			resource,
			1_000_000,
			"unlimited:"+string(resource),
		)
		if err != nil || !decision.Accepted ||
			decision.Usage.Limit != nil ||
			decision.Usage.Remaining != nil ||
			decision.Usage.OverLimit {
			t.Fatalf("Unlimited %s decision = %#v, %v", resource, decision, err)
		}
	}
	overview, err := service.GetOverview(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Plan.Code != PlanUnlimited ||
		overview.Plan.Limits.Channels != nil {
		t.Fatalf("Unlimited overview = %#v", overview)
	}
	for _, usage := range overview.Usage {
		if usage.Limit != nil || usage.Remaining != nil || usage.OverLimit {
			t.Fatalf("Unlimited usage contains a numeric quota: %#v", usage)
		}
	}
	var checkoutChannels, billingChannels *int64
	if err := pool.QueryRow(
		ctx,
		`SELECT checkout.channel_quantity, billing.channel_quantity
		   FROM f10_checkout_sessions AS checkout
		   JOIN f10_workspace_billing AS billing USING (workspace_id)
		  WHERE checkout.session_id = $1`,
		registration.SessionID,
	).Scan(&checkoutChannels, &billingChannels); err != nil {
		t.Fatal(err)
	}
	if checkoutChannels != nil || billingChannels != nil {
		t.Fatalf(
			"Unlimited persisted fake quantities: checkout=%v billing=%v",
			checkoutChannels,
			billingChannels,
		)
	}

	downgrade := paid
	downgrade.ID = "evt_unlimited_to_team_f10"
	downgrade.OccurredAt = paid.Period.End
	downgrade.Plan = PlanTeam
	downgrade.Interval = IntervalAnnual
	downgrade.Channels = limit(9)
	downgrade.Period = Period{
		Start: paid.Period.End,
		End:   paid.Period.End.AddDate(1, 0, 0),
	}
	if result, err := store.ApplyBillingEvent(ctx, downgrade); err != nil ||
		!result.StateChanged {
		t.Fatalf("Unlimited downgrade = %#v, %v", result, err)
	}
	overview, err = service.GetOverview(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	channels := usageFor(t, overview, ResourceChannels)
	if channels.Used != 1_000_000 || channels.Limit == nil ||
		*channels.Limit != 9 || !channels.OverLimit {
		t.Fatalf("downgrade deleted or hid channel overage: %#v", channels)
	}
	denied, err := service.Reserve(
		ctx,
		workspaceID,
		ResourceChannels,
		1,
		"downgraded:new-channel",
	)
	if err != nil || denied.Accepted || denied.Code != "limit_reached" {
		t.Fatalf("downgraded addition = %#v, %v", denied, err)
	}
}

func testBillingLifecycle(
	t *testing.T,
	pool *pgxpool.Pool,
	store *SQLStore,
	workspaceID string,
	now time.Time,
) {
	t.Helper()
	ctx := context.Background()
	registration := CheckoutRegistration{
		SessionID:      "txn_00000000000000000000000001",
		WorkspaceID:    workspaceID,
		Plan:           PlanTeam,
		Interval:       IntervalAnnual,
		Channels:       limit(9),
		CatalogVersion: CatalogVersion,
		Items: []PaddleItem{
			{PriceID: "pri_00000000000000000000000010", Quantity: 9},
		},
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
	}
	if err := store.RegisterCheckout(ctx, registration); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterCheckout(ctx, registration); err != nil {
		t.Fatalf("idempotent checkout registration: %v", err)
	}
	var stateBeforePayment BillingState
	if err := pool.QueryRow(
		ctx,
		`SELECT billing_state
		   FROM f10_workspace_billing
		  WHERE workspace_id = $1`,
		workspaceID,
	).Scan(&stateBeforePayment); err != nil {
		t.Fatal(err)
	}
	if stateBeforePayment != StateTrialing {
		t.Fatalf("checkout activated billing before payment: %s", stateBeforePayment)
	}

	paid := BillingEvent{
		ID:             "evt_paid_f10",
		Type:           "transaction.completed",
		OccurredAt:     now.Add(2 * time.Minute),
		WorkspaceID:    workspaceID,
		Plan:           PlanTeam,
		Interval:       IntervalAnnual,
		Channels:       limit(9),
		State:          StateActive,
		CustomerID:     "cus_f10",
		SubscriptionID: "sub_f10",
		TransactionID:  registration.SessionID,
		ApplyState:     true,
		Period: Period{
			Start: now,
			End:   now.AddDate(1, 0, 0),
		},
	}
	result, err := store.ApplyBillingEvent(ctx, paid)
	if err != nil || !result.FirstDelivery || !result.StateChanged {
		t.Fatalf("paid event = %#v, %v", result, err)
	}
	result, err = store.ApplyBillingEvent(ctx, paid)
	if err != nil || result.FirstDelivery || result.StateChanged {
		t.Fatalf("replayed paid event = %#v, %v", result, err)
	}

	stale := paid
	stale.ID = "evt_stale_f10"
	stale.OccurredAt = now.Add(90 * time.Second)
	stale.State = StatePaymentRestricted
	stale.Plan = PlanStart
	stale.Interval = IntervalMonthly
	stale.Channels = limit(3)
	result, err = store.ApplyBillingEvent(ctx, stale)
	if err != nil || !result.FirstDelivery || result.StateChanged {
		t.Fatalf("stale event = %#v, %v", result, err)
	}

	var state BillingState
	var plan PlanCode
	if err := pool.QueryRow(
		ctx,
		`SELECT billing_state, plan_code
		   FROM f10_workspace_billing
		  WHERE workspace_id = $1`,
		workspaceID,
	).Scan(&state, &plan); err != nil {
		t.Fatal(err)
	}
	if state != StateActive || plan != PlanTeam {
		t.Fatalf("state after stale/replayed events = %s/%s", state, plan)
	}
	binding, err := store.ResolveSubscription(ctx, "sub_f10")
	if err != nil {
		t.Fatal(err)
	}
	if binding.Plan != PlanTeam || binding.Interval != IntervalAnnual {
		t.Fatalf("stale event changed checkout binding: %#v", binding)
	}

	failed := paid
	failed.ID = "evt_failed_f10"
	failed.Type = "transaction.payment_failed"
	failed.OccurredAt = now.Add(3 * time.Minute)
	failed.State = StatePastDue
	if result, err = store.ApplyBillingEvent(ctx, failed); err != nil ||
		!result.StateChanged {
		t.Fatalf("first payment failure = %#v, %v", result, err)
	}
	retried := failed
	retried.ID = "evt_failed_again_f10"
	retried.OccurredAt = now.Add(24 * time.Hour)
	if _, err = store.ApplyBillingEvent(ctx, retried); err != nil {
		t.Fatal(err)
	}
	var firstFailure, dunningEnds time.Time
	if err := pool.QueryRow(
		ctx,
		`SELECT first_payment_failed_at, dunning_ends_at
		   FROM f10_workspace_billing
		  WHERE workspace_id = $1`,
		workspaceID,
	).Scan(&firstFailure, &dunningEnds); err != nil {
		t.Fatal(err)
	}
	wantFailure := now.Add(3 * time.Minute)
	if !firstFailure.Equal(wantFailure) ||
		!dunningEnds.Equal(wantFailure.Add(30*24*time.Hour)) {
		t.Fatalf(
			"dunning = %s..%s, want 30 days anchored at %s",
			firstFailure,
			dunningEnds,
			wantFailure,
		)
	}

	if _, err := pool.Exec(
		ctx,
		`DELETE FROM f10_internal_entitlement_overrides
		  WHERE workspace_id = $1`,
		workspaceID,
	); err != nil {
		t.Fatal(err)
	}
	afterDunning, err := store.ApplyUsage(ctx, UsageCommand{
		WorkspaceID:    workspaceID,
		Resource:       ResourceMembers,
		Delta:          1,
		IdempotencyKey: "member:after-dunning-window",
		OccurredAt:     dunningEnds.Add(time.Second),
	})
	if err != nil || !afterDunning.Accepted {
		t.Fatalf("usage after dunning deadline = %#v, %v", afterDunning, err)
	}
	if err := pool.QueryRow(
		ctx,
		`SELECT billing_state
		   FROM f10_workspace_billing
		  WHERE workspace_id = $1`,
		workspaceID,
	).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != StatePastDue {
		t.Fatalf("local deadline changed billing state to %s", state)
	}

	paused := retried
	paused.ID = "evt_paused_f10"
	paused.Type = "subscription.paused"
	paused.OccurredAt = dunningEnds.Add(2 * time.Second)
	paused.State = StatePaymentRestricted
	if result, err = store.ApplyBillingEvent(ctx, paused); err != nil ||
		!result.StateChanged {
		t.Fatalf("Paddle pause = %#v, %v", result, err)
	}
	restricted, err := store.ApplyUsage(ctx, UsageCommand{
		WorkspaceID:    workspaceID,
		Resource:       ResourceMembers,
		Delta:          1,
		IdempotencyKey: "member:while-paused",
		OccurredAt:     paused.OccurredAt.Add(time.Second),
	})
	if err != nil || restricted.Accepted ||
		restricted.Code != "payment_restricted" {
		t.Fatalf("usage while Paddle-paused = %#v, %v", restricted, err)
	}

	recovered := paused
	recovered.ID = "evt_recovered_f10"
	recovered.Type = "subscription.resumed"
	recovered.OccurredAt = paused.OccurredAt.Add(2 * time.Second)
	recovered.State = StateActive
	if result, err = store.ApplyBillingEvent(ctx, recovered); err != nil ||
		!result.StateChanged {
		t.Fatalf("recovered payment = %#v, %v", result, err)
	}
	var timestampsCleared bool
	if err := pool.QueryRow(
		ctx,
		`SELECT first_payment_failed_at IS NULL
		        AND dunning_ends_at IS NULL
		   FROM f10_workspace_billing
		  WHERE workspace_id = $1`,
		workspaceID,
	).Scan(&timestampsCleared); err != nil {
		t.Fatal(err)
	}
	if !timestampsCleared {
		t.Fatal("recovered payment did not clear dunning timestamps")
	}
	recoveredUsage, err := store.ApplyUsage(ctx, UsageCommand{
		WorkspaceID:    workspaceID,
		Resource:       ResourceMembers,
		Delta:          1,
		IdempotencyKey: "member:after-recovery",
		OccurredAt:     recovered.OccurredAt.Add(time.Second),
	})
	if err != nil || !recoveredUsage.Accepted {
		t.Fatalf("usage after payment recovery = %#v, %v", recoveredUsage, err)
	}

	canceled := recovered
	canceled.ID = "evt_canceled_f10"
	canceled.Type = "subscription.canceled"
	canceled.OccurredAt = dunningEnds.Add(31 * 24 * time.Hour)
	canceled.Plan = PlanStart
	canceled.Interval = IntervalMonthly
	canceled.Channels = limit(3)
	canceled.State = StateCanceled
	if result, err = store.ApplyBillingEvent(ctx, canceled); err != nil ||
		!result.StateChanged {
		t.Fatalf("Paddle cancellation = %#v, %v", result, err)
	}
	if err := pool.QueryRow(
		ctx,
		`SELECT billing_state, plan_code
		   FROM f10_workspace_billing
		  WHERE workspace_id = $1`,
		workspaceID,
	).Scan(&state, &plan); err != nil {
		t.Fatal(err)
	}
	if state != StateCanceled || plan != PlanStart {
		t.Fatalf("state after Paddle cancellation = %s/%s", state, plan)
	}
}

func testMonthlyQuotaWindow(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var start, end time.Time
	err := pool.QueryRow(
		context.Background(),
		`SELECT window_start, window_end
		   FROM f10_monthly_quota_window(
		       '2027-01-31 10:15:00+00',
		       '2027-02-28 11:00:00+00'
		   )`,
	).Scan(&start, &end)
	if err != nil {
		t.Fatal(err)
	}
	wantStart := time.Date(2027, time.February, 28, 10, 15, 0, 0, time.UTC)
	wantEnd := time.Date(2027, time.March, 31, 10, 15, 0, 0, time.UTC)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Fatalf("quota window = %s..%s, want %s..%s", start, end, wantStart, wantEnd)
	}
}

func usageFor(t *testing.T, overview Overview, resource Resource) Usage {
	t.Helper()
	for _, usage := range overview.Usage {
		if usage.Resource == resource {
			return usage
		}
	}
	t.Fatalf("overview has no %s usage: %#v", resource, overview)
	return Usage{}
}

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}

	schema := fmt.Sprintf("f10_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"000001_create_entitlements.sql",
		"000002_paddle_d07.sql",
	} {
		migration, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	paddleMigration, err := os.ReadFile(filepath.Join(
		"migrations",
		"000002_paddle_d07.sql",
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(paddleMigration)); err != nil {
		t.Fatalf("idempotent Paddle migration replay: %v", err)
	}
	migrationFailureAt := time.Date(
		2020,
		time.July,
		1,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	const (
		pastDueFixture    = "c8ebf596-8a13-4d4b-bafe-365f7c61c448"
		restrictedFixture = "f9c88c87-bf75-4d31-b250-3f270bc21f34"
	)
	for workspaceID, state := range map[string]BillingState{
		pastDueFixture:    StatePastDue,
		restrictedFixture: StatePaymentRestricted,
	} {
		var created bool
		if err := pool.QueryRow(
			ctx,
			"SELECT f10_provision_trial($1, $2)",
			workspaceID,
			migrationFailureAt,
		).Scan(&created); err != nil || !created {
			t.Fatalf("provision %s migration fixture: %v", state, err)
		}
		if _, err := pool.Exec(
			ctx,
			`UPDATE f10_workspace_billing
			    SET plan_code = 'team',
			        billing_state = $3::text,
			        first_payment_failed_at = $2::timestamptz,
			        grace_ends_at = $2::timestamptz + interval '14 days'
			  WHERE workspace_id = $1`,
			workspaceID,
			migrationFailureAt,
			state,
		); err != nil {
			t.Fatalf("seed %s dunning migration fixture: %v", state, err)
		}
	}
	dunningMigration, err := os.ReadFile(filepath.Join(
		"migrations",
		"000003_paddle_dunning_30_days.sql",
	))
	if err != nil {
		t.Fatal(err)
	}
	for replay := 0; replay < 2; replay++ {
		if _, err := pool.Exec(ctx, string(dunningMigration)); err != nil {
			t.Fatalf("dunning migration replay %d: %v", replay, err)
		}
	}
	for workspaceID, wantState := range map[string]BillingState{
		pastDueFixture:    StatePastDue,
		restrictedFixture: StatePaymentRestricted,
	} {
		var (
			state       BillingState
			dunningEnds time.Time
		)
		if err := pool.QueryRow(
			ctx,
			`SELECT billing_state, dunning_ends_at
			   FROM f10_workspace_billing
			  WHERE workspace_id = $1`,
			workspaceID,
		).Scan(&state, &dunningEnds); err != nil {
			t.Fatalf("query %s dunning migration fixture: %v", wantState, err)
		}
		if state != wantState {
			t.Fatalf("migration changed %s fixture to %s", wantState, state)
		}
		if want := migrationFailureAt.Add(30 * 24 * time.Hour); !dunningEnds.Equal(want) {
			t.Fatalf(
				"%s fixture dunning ends at %s, want %s",
				wantState,
				dunningEnds,
				want,
			)
		}
	}
	var publicState BillingState
	if err := pool.QueryRow(
		ctx,
		`SELECT billing_state
		   FROM f10_public_entitlement_usage
		  WHERE workspace_id = $1
		  LIMIT 1`,
		pastDueFixture,
	).Scan(&publicState); err != nil {
		t.Fatalf("query migrated public dunning state: %v", err)
	}
	if publicState != StatePastDue {
		t.Fatalf("public view inferred local suspension state %s", publicState)
	}

	d09Migration, err := os.ReadFile(filepath.Join(
		"migrations",
		"000004_d09_public_unlimited.sql",
	))
	if err != nil {
		t.Fatal(err)
	}
	for replay := 0; replay < 2; replay++ {
		if _, err := pool.Exec(ctx, string(d09Migration)); err != nil {
			t.Fatalf("D09 migration replay %d: %v", replay, err)
		}
	}
	var (
		unlimitedMembers  *int64
		unlimitedChannels *int64
	)
	if err := pool.QueryRow(
		ctx,
		`SELECT member_limit, channel_limit
		   FROM f10_public_plans
		  WHERE code = 'unlimited'`,
	).Scan(&unlimitedMembers, &unlimitedChannels); err != nil {
		t.Fatal(err)
	}
	if unlimitedMembers != nil || unlimitedChannels != nil {
		t.Fatalf(
			"Unlimited migration stored sentinels: members=%v channels=%v",
			unlimitedMembers,
			unlimitedChannels,
		)
	}

	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return pool
}
