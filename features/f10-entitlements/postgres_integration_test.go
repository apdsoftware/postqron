package entitlements

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresPlanChangeGuardIdempotencyRaceAndSignedWebhook(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 15, 0, 0, 0, time.UTC)
	workspaceID := fmt.Sprintf("plan-change-%d", time.Now().UnixNano())
	store := NewSQLStore(pool)
	seedPaidWorkspace(
		t,
		pool,
		workspaceID,
		PlanTeam,
		IntervalMonthly,
		limit(9),
		"ctm_plan_change",
		"sub_plan_change",
		now,
	)
	service := NewService(store)
	service.now = func() time.Time { return now.Add(time.Minute) }
	for _, command := range []struct {
		resource Resource
		amount   int64
	}{
		{ResourceMembers, 3},
		{ResourceChannels, 6},
		{ResourceScheduledPublications, 250},
	} {
		decision, err := service.Reserve(
			ctx,
			workspaceID,
			command.resource,
			command.amount,
			"seed:"+string(command.resource),
		)
		if err != nil || !decision.Accepted {
			t.Fatalf("seed %s = %#v, %v", command.resource, decision, err)
		}
	}

	provider := &changeProviderStub{}
	changes, err := NewSubscriptionChangeService(
		ownerStub{owner: true},
		provider,
		store,
		testPaddleCatalog(),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := SubscriptionChangeRequest{
		WorkspaceID:    workspaceID,
		AccountID:      "owner",
		Plan:           PlanPro,
		Interval:       IntervalMonthly,
		Channels:       limit(6),
		IdempotencyKey: "team-to-pro",
	}
	result, err := changes.Apply(ctx, request)
	if err != nil || result.Status != ChangePending || provider.updates != 1 {
		t.Fatalf("first change = %#v, updates=%d, %v", result, provider.updates, err)
	}
	result, err = changes.Apply(ctx, request)
	if err != nil || result.Status != ChangePending || provider.updates != 1 {
		t.Fatalf("idempotent replay = %#v, updates=%d, %v", result, provider.updates, err)
	}
	reusedKey := request
	reusedKey.Plan = PlanUnlimited
	reusedKey.Channels = nil
	if _, err := changes.Apply(ctx, reusedKey); !errors.Is(
		err,
		ErrIdempotencyConflict,
	) {
		t.Fatalf("reused key error = %v", err)
	}

	var plan PlanCode
	if err := pool.QueryRow(
		ctx,
		`SELECT plan_code
		   FROM f10_workspace_billing
		  WHERE workspace_id = $1`,
		workspaceID,
	).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if plan != PlanTeam {
		t.Fatalf("request activated %s before webhook", plan)
	}

	var conflicts atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < 12; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			concurrent := request
			concurrent.Plan = PlanUnlimited
			concurrent.Channels = nil
			concurrent.IdempotencyKey = fmt.Sprintf("concurrent-%02d", index)
			_, err := changes.Apply(ctx, concurrent)
			if errors.Is(err, ErrChangeInProgress) {
				conflicts.Add(1)
				return
			}
			t.Errorf("concurrent change %d error = %v", index, err)
		}(index)
	}
	wait.Wait()
	if conflicts.Load() != 12 || provider.updates != 1 {
		t.Fatalf("conflicts=%d updates=%d", conflicts.Load(), provider.updates)
	}

	denied, err := service.Reserve(
		ctx,
		workspaceID,
		ResourceChannels,
		1,
		"pending-target-channel",
	)
	if err != nil || denied.Accepted || denied.Code != "limit_reached" ||
		denied.Usage.Limit == nil || *denied.Usage.Limit != 6 {
		t.Fatalf("pending downgrade race guard = %#v, %v", denied, err)
	}

	catalog := testPaddleCatalog()
	items, err := catalog.ExpectedItems(PlanPro, IntervalMonthly, limit(6))
	if err != nil {
		t.Fatal(err)
	}
	webhook, err := NewPaddleWebhookHandler("endpoint_secret", catalog, store)
	if err != nil {
		t.Fatal(err)
	}
	webhook.now = func() time.Time { return now.Add(2 * time.Minute) }
	body := paddleEventBody(
		t,
		"evt_plan_change_applied",
		"transaction.completed",
		now.Add(2*time.Minute),
		map[string]any{
			"id":              "txn_plan_change",
			"status":          "completed",
			"customer_id":     "ctm_plan_change",
			"subscription_id": "sub_plan_change",
			"items":           paddleWebhookItems(items),
			"billing_period": map[string]any{
				"starts_at": now.AddDate(0, 1, 0),
				"ends_at":   now.AddDate(0, 2, 0),
			},
		},
	)
	for delivery := 0; delivery < 2; delivery++ {
		httpRequest := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/billing/paddle/webhook",
			strings.NewReader(string(body)),
		)
		httpRequest.Header.Set(
			"Paddle-Signature",
			paddleSignature("endpoint_secret", now.Add(2*time.Minute), body),
		)
		response := httptest.NewRecorder()
		webhook.ServeHTTP(response, httpRequest)
		if response.Code != http.StatusNoContent {
			t.Fatalf(
				"signed webhook delivery %d status=%d body=%s",
				delivery,
				response.Code,
				response.Body.String(),
			)
		}
	}
	var status ChangeStatus
	if err := pool.QueryRow(
		ctx,
		`SELECT billing.plan_code, change.status
		   FROM f10_workspace_billing AS billing
		   JOIN f10_subscription_changes AS change USING (workspace_id)
		  WHERE billing.workspace_id = $1`,
		workspaceID,
	).Scan(&plan, &status); err != nil {
		t.Fatal(err)
	}
	if plan != PlanPro || status != ChangeApplied {
		t.Fatalf("after webhook plan=%s status=%s", plan, status)
	}
	result, err = changes.Apply(ctx, request)
	if err != nil || result.Status != ChangeApplied || provider.updates != 1 {
		t.Fatalf(
			"post-webhook idempotent replay = %#v, updates=%d, %v",
			result,
			provider.updates,
			err,
		)
	}
}

func TestPostgresPlanChangeAndUsageRaceFailsClosed(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 16, 0, 0, 0, time.UTC)
	workspaceID := fmt.Sprintf("plan-change-race-%d", time.Now().UnixNano())
	store := NewSQLStore(pool)
	seedPaidWorkspace(
		t,
		pool,
		workspaceID,
		PlanTeam,
		IntervalMonthly,
		limit(9),
		"ctm_plan_change_race",
		"sub_plan_change_race",
		now,
	)
	service := NewService(store)
	service.now = func() time.Time { return now.Add(time.Minute) }
	if decision, err := service.Reserve(
		ctx,
		workspaceID,
		ResourceChannels,
		6,
		"race:seed-six",
	); err != nil || !decision.Accepted {
		t.Fatalf("seed race usage = %#v, %v", decision, err)
	}

	provider := &changeProviderStub{}
	changes, err := NewSubscriptionChangeService(
		ownerStub{owner: true},
		provider,
		store,
		testPaddleCatalog(),
	)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var (
		changeAccepted bool
		usageAccepted  bool
		changeErr      error
		usageErr       error
	)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, changeErr = changes.Apply(ctx, SubscriptionChangeRequest{
			WorkspaceID:    workspaceID,
			AccountID:      "owner",
			Plan:           PlanPro,
			Interval:       IntervalMonthly,
			Channels:       limit(6),
			IdempotencyKey: "race:downgrade",
		})
		changeAccepted = changeErr == nil
	}()
	go func() {
		defer wait.Done()
		<-start
		decision, err := service.Reserve(
			ctx,
			workspaceID,
			ResourceChannels,
			1,
			"race:seventh-channel",
		)
		usageErr = err
		usageAccepted = err == nil && decision.Accepted
	}()
	close(start)
	wait.Wait()

	if changeErr != nil && !errors.Is(changeErr, ErrDowngradeBlocked) {
		t.Fatalf("change race error = %v", changeErr)
	}
	if usageErr != nil {
		t.Fatalf("usage race error = %v", usageErr)
	}
	if changeAccepted == usageAccepted {
		t.Fatalf(
			"race did not fail closed: changeAccepted=%v usageAccepted=%v",
			changeAccepted,
			usageAccepted,
		)
	}
}

func TestPostgresDowngradeGuardReturnsAllStructuredOverages(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 17, 0, 0, 0, time.UTC)
	workspaceID := fmt.Sprintf("plan-change-overages-%d", time.Now().UnixNano())
	store := NewSQLStore(pool)
	seedPaidWorkspace(
		t,
		pool,
		workspaceID,
		PlanUnlimited,
		IntervalAnnual,
		nil,
		"ctm_plan_change_overages",
		"sub_plan_change_overages",
		now,
	)
	service := NewService(store)
	service.now = func() time.Time { return now.Add(time.Minute) }
	for _, command := range []struct {
		resource Resource
		amount   int64
	}{
		{ResourceMembers, 4},
		{ResourceChannels, 7},
		{ResourceScheduledPublications, 251},
	} {
		decision, err := service.Reserve(
			ctx,
			workspaceID,
			command.resource,
			command.amount,
			"overage:"+string(command.resource),
		)
		if err != nil || !decision.Accepted {
			t.Fatalf("seed %s = %#v, %v", command.resource, decision, err)
		}
	}
	provider := &changeProviderStub{}
	changes, err := NewSubscriptionChangeService(
		ownerStub{owner: true},
		provider,
		store,
		testPaddleCatalog(),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = changes.Apply(ctx, SubscriptionChangeRequest{
		WorkspaceID:    workspaceID,
		AccountID:      "owner",
		Plan:           PlanPro,
		Interval:       IntervalAnnual,
		Channels:       limit(6),
		IdempotencyKey: "blocked-all-resources",
	})
	var blocked *DowngradeBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("error = %v", err)
	}
	want := map[Resource]DowngradeOverage{
		ResourceMembers: {
			Resource: ResourceMembers, Used: 4, Limit: 3, Excess: 1,
		},
		ResourceChannels: {
			Resource: ResourceChannels, Used: 7, Limit: 6, Excess: 1,
		},
		ResourceScheduledPublications: {
			Resource: ResourceScheduledPublications,
			Used:     251,
			Limit:    250,
			Excess:   1,
		},
	}
	if len(blocked.Overages) != len(want) {
		t.Fatalf("overages = %#v", blocked.Overages)
	}
	for _, overage := range blocked.Overages {
		if overage != want[overage.Resource] {
			t.Fatalf("%s overage = %#v", overage.Resource, overage)
		}
	}
	var changesCount int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*)
		   FROM f10_subscription_changes
		  WHERE workspace_id = $1`,
		workspaceID,
	).Scan(&changesCount); err != nil {
		t.Fatal(err)
	}
	if changesCount != 0 || provider.updates != 0 {
		t.Fatalf(
			"blocked change persisted/dispatched: rows=%d updates=%d",
			changesCount,
			provider.updates,
		)
	}
	overview, err := service.GetOverview(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Plan.Code != PlanUnlimited {
		t.Fatalf("blocked downgrade changed plan to %s", overview.Plan.Code)
	}
	for _, current := range overview.Usage {
		if current.Used != want[current.Resource].Used {
			t.Fatalf("blocked downgrade changed %s usage: %#v", current.Resource, current)
		}
	}
}

func seedPaidWorkspace(
	t *testing.T,
	pool *pgxpool.Pool,
	workspaceID string,
	plan PlanCode,
	interval BillingInterval,
	channels *int64,
	customerID string,
	subscriptionID string,
	now time.Time,
) {
	t.Helper()
	store := NewSQLStore(pool)
	created, err := store.ProvisionTrial(
		context.Background(),
		workspaceID,
		now.Add(-24*time.Hour),
	)
	if err != nil || !created {
		t.Fatalf("seed trial = %v, %v", created, err)
	}
	if _, err := pool.Exec(
		context.Background(),
		`UPDATE f10_workspace_billing
		    SET plan_code = $2,
		        billing_interval = $3,
		        billing_state = 'active',
		        channel_quantity = $4,
		        paddle_customer_id = $5,
		        paddle_subscription_id = $6,
		        provider_period_start = $7,
		        provider_period_end = $8,
		        last_provider_event_created_at = $7,
		        last_provider_event_id = 'evt_seed',
		        updated_at = $7
		  WHERE workspace_id = $1`,
		workspaceID,
		plan,
		interval,
		channels,
		customerID,
		subscriptionID,
		now,
		now.AddDate(0, 1, 0),
	); err != nil {
		t.Fatalf("seed paid workspace: %v", err)
	}
}

func TestPostgresAcceptsCanonicalF4TextWorkspaceID(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	workspaceID := fmt.Sprintf("personal-account-270-%d", time.Now().UnixNano())
	store := NewSQLStore(pool)

	created, err := store.ProvisionTrial(ctx, workspaceID, now)
	if err != nil || !created {
		t.Fatalf("ProvisionTrial(%q) = %v, %v", workspaceID, created, err)
	}
	decision, err := store.ApplyUsage(ctx, UsageCommand{
		WorkspaceID:    workspaceID,
		Resource:       ResourceChannels,
		Delta:          1,
		IdempotencyKey: "issue-270-real-f4-id",
		OccurredAt:     now.Add(time.Minute),
	})
	if err != nil || !decision.Accepted {
		t.Fatalf("ApplyUsage(%q) = %#v, %v", workspaceID, decision, err)
	}
	overview, err := store.Overview(ctx, workspaceID)
	if err != nil {
		t.Fatalf("Overview(%q): %v", workspaceID, err)
	}
	if overview.Plan.Code != PlanTeam ||
		usageFor(t, overview, ResourceChannels).Used != 1 {
		t.Fatalf("text workspace overview = %#v", overview)
	}
}

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
	if members.Limit == nil || *members.Limit != 6 {
		t.Fatalf("Team trial member limit = %#v, want 6", members.Limit)
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

	billingLifecycleWorkspace := "6b864720-e4ea-42bb-9d39-f824b19b4ea4"
	if created, err := store.ProvisionTrial(
		ctx,
		billingLifecycleWorkspace,
		now,
	); err != nil || !created {
		t.Fatalf("provision billing lifecycle workspace = %v, %v", created, err)
	}
	testBillingLifecycle(t, pool, store, billingLifecycleWorkspace, now)
	testUnlimitedEntitlementAndConservativeDowngrade(t, pool, store, now)
	testProductOwnerPlanLimitBoundaries(t, pool, store, now)
	testMonthlyQuotaWindow(t, pool)
}

func testProductOwnerPlanLimitBoundaries(
	t *testing.T,
	pool *pgxpool.Pool,
	store *SQLStore,
	now time.Time,
) {
	t.Helper()
	ctx := context.Background()
	workspaceID := "bc688d50-4a8f-4d73-b4f7-bfed67e9568c"
	created, err := store.ProvisionTrial(ctx, workspaceID, now)
	if err != nil || !created {
		t.Fatalf("provision plan-limit fixture = %v, %v", created, err)
	}
	if _, err := pool.Exec(
		ctx,
		`UPDATE f10_workspace_billing
		    SET plan_code = 'pro',
		        billing_state = 'active',
		        channel_quantity = 6,
		        updated_at = $2
		  WHERE workspace_id = $1`,
		workspaceID,
		now,
	); err != nil {
		t.Fatal(err)
	}

	service := NewService(store)
	service.now = func() time.Time { return now.Add(time.Minute) }
	for _, boundary := range []struct {
		resource Resource
		limit    int64
	}{
		{ResourceMembers, 3},
		{ResourceChannels, 6},
		{ResourceScheduledPublications, 250},
	} {
		accepted, err := service.Reserve(
			ctx,
			workspaceID,
			boundary.resource,
			boundary.limit,
			"pro:boundary:"+string(boundary.resource),
		)
		if err != nil || !accepted.Accepted ||
			accepted.Usage.Limit == nil ||
			*accepted.Usage.Limit != boundary.limit ||
			accepted.Usage.Remaining == nil ||
			*accepted.Usage.Remaining != 0 {
			t.Fatalf("Pro %s boundary = %#v, %v", boundary.resource, accepted, err)
		}
		denied, err := service.Reserve(
			ctx,
			workspaceID,
			boundary.resource,
			1,
			"pro:over:"+string(boundary.resource),
		)
		if err != nil || denied.Accepted || denied.Code != "limit_reached" {
			t.Fatalf("Pro %s overage = %#v, %v", boundary.resource, denied, err)
		}
	}

	if _, err := pool.Exec(
		ctx,
		`UPDATE f10_workspace_billing
		    SET plan_code = 'team',
		        channel_quantity = 9,
		        updated_at = $2
		  WHERE workspace_id = $1`,
		workspaceID,
		now.Add(2*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	for _, boundary := range []struct {
		resource Resource
		delta    int64
		limit    int64
	}{
		{ResourceMembers, 3, 6},
		{ResourceChannels, 3, 9},
		{ResourceScheduledPublications, 250, 500},
	} {
		accepted, err := service.Reserve(
			ctx,
			workspaceID,
			boundary.resource,
			boundary.delta,
			"team:boundary:"+string(boundary.resource),
		)
		if err != nil || !accepted.Accepted ||
			accepted.Usage.Limit == nil ||
			*accepted.Usage.Limit != boundary.limit ||
			accepted.Usage.Used != boundary.limit {
			t.Fatalf("Team %s boundary = %#v, %v", boundary.resource, accepted, err)
		}
		denied, err := service.Reserve(
			ctx,
			workspaceID,
			boundary.resource,
			1,
			"team:over:"+string(boundary.resource),
		)
		if err != nil || denied.Accepted || denied.Code != "limit_reached" {
			t.Fatalf("Team %s overage = %#v, %v", boundary.resource, denied, err)
		}
	}
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
	if result, err := store.ApplyBillingEvent(ctx, downgrade); err == nil ||
		result.StateChanged {
		t.Fatalf("unguarded Unlimited downgrade = %#v, %v", result, err)
	}
	overview, err = service.GetOverview(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	channels := usageFor(t, overview, ResourceChannels)
	if overview.Plan.Code != PlanUnlimited ||
		channels.Used != 1_000_000 ||
		channels.Limit != nil ||
		channels.OverLimit {
		t.Fatalf("blocked downgrade changed or deleted resources: %#v", overview)
	}
	accepted, err := service.Reserve(
		ctx,
		workspaceID,
		ResourceChannels,
		1,
		"blocked-downgrade:new-channel",
	)
	if err != nil || !accepted.Accepted {
		t.Fatalf("blocked downgrade changed active entitlement = %#v, %v", accepted, err)
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
	if released, err := store.ApplyUsage(ctx, UsageCommand{
		WorkspaceID:    workspaceID,
		Resource:       ResourceMembers,
		Delta:          -1,
		IdempotencyKey: "member:before-cancellation",
		OccurredAt:     recovered.OccurredAt.Add(2 * time.Second),
	}); err != nil || !released.Accepted || released.Usage.Used != 1 {
		t.Fatalf("usage before cancellation = %#v, %v", released, err)
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
	limitsMigration, err := os.ReadFile(filepath.Join(
		"migrations",
		"000005_po_20260727_plan_limits.sql",
	))
	if err != nil {
		t.Fatal(err)
	}
	for replay := 0; replay < 2; replay++ {
		if _, err := pool.Exec(ctx, string(limitsMigration)); err != nil {
			t.Fatalf("2026-07-27 limits migration replay %d: %v", replay, err)
		}
	}
	boundaryMigration, err := os.ReadFile(filepath.Join(
		"migrations",
		"000006_align_workspace_ids_with_f04.sql",
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(boundaryMigration)); err != nil {
		t.Fatalf("align F4/F10 workspace IDs: %v", err)
	}
	planChangeMigration, err := os.ReadFile(filepath.Join(
		"migrations",
		"000007_authoritative_plan_changes.sql",
	))
	if err != nil {
		t.Fatal(err)
	}
	for replay := 0; replay < 2; replay++ {
		if _, err := pool.Exec(ctx, string(planChangeMigration)); err != nil {
			t.Fatalf("authoritative plan change migration replay %d: %v", replay, err)
		}
	}
	var (
		preservedState BillingState
		preservedType  string
	)
	if err := pool.QueryRow(
		ctx,
		`SELECT billing_state, pg_typeof(workspace_id)::text
		   FROM f10_workspace_billing
		  WHERE workspace_id = $1`,
		pastDueFixture,
	).Scan(&preservedState, &preservedType); err != nil {
		t.Fatalf("read preserved UUID billing row: %v", err)
	}
	if preservedState != StatePastDue || preservedType != "text" {
		t.Fatalf(
			"preserved billing row = state %s, type %s",
			preservedState,
			preservedType,
		)
	}
	for _, table := range []string{
		"f10_workspace_billing",
		"f10_checkout_sessions",
		"f10_provider_events",
		"f10_usage_counters",
		"f10_usage_operations",
		"f10_internal_entitlement_overrides",
		"f10_subscription_changes",
	} {
		var dataType string
		if err := pool.QueryRow(
			ctx,
			`SELECT data_type
			   FROM information_schema.columns
			  WHERE table_schema = current_schema()
			    AND table_name = $1
			    AND column_name = 'workspace_id'`,
			table,
		).Scan(&dataType); err != nil {
			t.Fatalf("read %s workspace type: %v", table, err)
		}
		if dataType != "text" {
			t.Fatalf("%s.workspace_id type = %s, want text", table, dataType)
		}
	}
	for _, signatures := range [][2]string{
		{
			"f10_provision_trial(text,timestamptz)",
			"f10_provision_trial(uuid,timestamptz)",
		},
		{
			"f10_register_checkout(text,text,text,text,bigint,text,jsonb,timestamptz)",
			"f10_register_checkout(text,uuid,text,text,bigint,text,jsonb,timestamptz)",
		},
		{
			"f10_apply_billing_event(text,text,timestamptz,text,text,text,bigint,text,text,text,text,timestamptz,timestamptz,boolean)",
			"f10_apply_billing_event(text,text,timestamptz,uuid,text,text,bigint,text,text,text,text,timestamptz,timestamptz,boolean)",
		},
		{
			"f10_apply_usage(text,text,bigint,text,timestamptz)",
			"f10_apply_usage(uuid,text,bigint,text,timestamptz)",
		},
	} {
		var (
			textSignaturePresent bool
			uuidSignatureAbsent  bool
		)
		if err := pool.QueryRow(
			ctx,
			`SELECT to_regprocedure($1) IS NOT NULL,
			        to_regprocedure($2) IS NULL`,
			signatures[0],
			signatures[1],
		).Scan(&textSignaturePresent, &uuidSignatureAbsent); err != nil {
			t.Fatalf("inspect F10 function signatures: %v", err)
		}
		if !textSignaturePresent || !uuidSignatureAbsent {
			t.Fatalf(
				"workspace signatures not aligned: text %q=%v uuid %q absent=%v",
				signatures[0],
				textSignaturePresent,
				signatures[1],
				uuidSignatureAbsent,
			)
		}
	}
	for _, expected := range []struct {
		code                  PlanCode
		members               *int64
		channels              *int64
		scheduledPublications *int64
	}{
		{PlanStart, limit(1), limit(3), limit(10)},
		{PlanPro, limit(3), limit(6), limit(250)},
		{PlanTeam, limit(6), limit(9), limit(500)},
		{PlanUnlimited, nil, nil, nil},
	} {
		var members, channels, publications *int64
		if err := pool.QueryRow(
			ctx,
			`SELECT member_limit, channel_limit, scheduled_publication_limit
			   FROM f10_public_plans
			  WHERE code = $1`,
			expected.code,
		).Scan(&members, &channels, &publications); err != nil {
			t.Fatal(err)
		}
		if !equalLimit(members, expected.members) ||
			!equalLimit(channels, expected.channels) ||
			!equalLimit(publications, expected.scheduledPublications) {
			t.Fatalf(
				"%s database limits = members %v, channels %v, publications %v",
				expected.code,
				members,
				channels,
				publications,
			)
		}
	}

	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return pool
}

func equalLimit(left, right *int64) bool {
	return left == nil && right == nil ||
		left != nil && right != nil && *left == *right
}
