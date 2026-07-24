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
	if accepted.Load() != 15 {
		t.Fatalf("accepted reservations = %d, want Pro limit 15", accepted.Load())
	}

	overview, err := service.GetOverview(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	channels := usageFor(t, overview, ResourceChannels)
	if channels.Used != 15 || channels.Remaining != 0 || channels.OverLimit {
		t.Fatalf("channel usage after concurrency = %#v", channels)
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
	if got := usageFor(t, overview, ResourceChannels).Used; got != 15 {
		t.Fatalf("usage after idempotent replay = %d, want 15", got)
	}

	if _, err := pool.Exec(
		ctx,
		`UPDATE f10_workspace_billing
		    SET plan_code = 'start', updated_at = $2
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
	if channels.Used != 15 || channels.Remaining != 0 || !channels.OverLimit {
		t.Fatalf("downgrade did not preserve overage: %#v", channels)
	}

	release, err := service.Release(
		ctx,
		workspaceID,
		ResourceChannels,
		1,
		"disconnect:channel-00",
	)
	if err != nil || !release.Accepted || release.Usage.Used != 14 {
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
	if denied.Accepted || denied.Code != "limit_reached" || denied.Usage.Used != 14 {
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
	for _, forbidden := range []string{"internal", "unlimited", "override"} {
		if strings.Contains(lowerPayload, forbidden) {
			t.Fatalf("public overview exposed %q: %s", forbidden, payload)
		}
	}

	testBillingLifecycle(t, pool, store, workspaceID, now)
	testMonthlyQuotaWindow(t, pool)
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
		SessionID:   "cs_test_f10",
		WorkspaceID: workspaceID,
		Plan:        PlanTeam,
		Interval:    IntervalAnnual,
		ExpiresAt:   now.Add(time.Hour),
		CreatedAt:   now,
	}
	if err := store.RegisterCheckout(ctx, registration); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterCheckout(ctx, registration); err != nil {
		t.Fatalf("idempotent checkout registration: %v", err)
	}
	first, err := store.CompleteCheckout(
		ctx,
		"evt_checkout_f10",
		now.Add(time.Minute),
		registration.SessionID,
		"cus_f10",
		"sub_f10",
	)
	if err != nil || !first {
		t.Fatalf("first checkout completion = %v, %v", first, err)
	}
	first, err = store.CompleteCheckout(
		ctx,
		"evt_checkout_f10",
		now.Add(time.Minute),
		registration.SessionID,
		"cus_f10",
		"sub_f10",
	)
	if err != nil || first {
		t.Fatalf("replayed checkout completion = %v, %v", first, err)
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
		Type:           "invoice.paid",
		CreatedAt:      now.Add(2 * time.Minute),
		WorkspaceID:    workspaceID,
		Plan:           PlanTeam,
		Interval:       IntervalAnnual,
		State:          StateActive,
		CustomerID:     "cus_f10",
		SubscriptionID: "sub_f10",
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
	stale.CreatedAt = now.Add(90 * time.Second)
	stale.State = StatePaymentRestricted
	stale.Plan = PlanStart
	stale.Interval = IntervalMonthly
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

	migration, err := os.ReadFile(filepath.Join(
		"migrations",
		"000001_create_entitlements.sql",
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return pool
}
