package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	entitlements "github.com/apdsoftware/postqron/features/f10-entitlements"
	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/api/internal/featurehost"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
)

func TestF10RoutesTraverseRealRuntimeFeatureHost(t *testing.T) {
	feature := discoverF10Feature(t)
	database, err := openDatabase(
		"postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	registry := featurehost.NewRegistry()
	registerF10Factory(t, registry)
	host, err := featurehost.New(
		[]featureruntime.Feature{feature},
		registry,
		f10RuntimeDependencies(database, completeF10Config(t), io.Discard),
		featurehost.ValidatedMigrations{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Stop(context.Background()) })

	api, err := httpapi.NewWithHost(
		host,
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				ctx := featureruntime.WithAuthenticatedAccount(
					request.Context(),
					"account-runtime-test",
				)
				next.ServeHTTP(writer, request.WithContext(ctx))
			})
		},
		"test",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}

	plans := serveRuntimeRequest(api, http.MethodGet, "/api/v1/billing/plans", "")
	if plans.Code != http.StatusOK {
		t.Fatalf("catalog status = %d, body = %s", plans.Code, plans.Body.String())
	}
	var catalog struct {
		Plans []entitlements.PublicPlan `json:"plans"`
	}
	if err := json.NewDecoder(plans.Body).Decode(&catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Plans) != 4 {
		t.Fatalf("catalog plans = %d, want 4", len(catalog.Plans))
	}

	webhook := serveRuntimeRequest(
		api,
		http.MethodPost,
		"/api/v1/billing/paddle/webhook",
		`{}`,
	)
	if webhook.Code != http.StatusBadRequest {
		t.Fatalf(
			"unsigned webhook status = %d, want 400 (body %q)",
			webhook.Code,
			webhook.Body.String(),
		)
	}

	privateRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/workspaces/workspace/billing/checkout"},
		{http.MethodPost, "/api/v1/workspaces/workspace/billing/portal"},
		{http.MethodPost, "/api/v1/workspaces/workspace/billing/subscription/preview"},
		{http.MethodPatch, "/api/v1/workspaces/workspace/billing/subscription"},
		{http.MethodPost, "/api/v1/workspaces/workspace/billing/subscription/cancel"},
	}
	for _, route := range privateRoutes {
		response := serveRuntimeRequest(api, route.method, route.path, `{`)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s %s status = %d, want 400", route.method, route.path, response.Code)
		}
	}
}

func TestF10IncompleteRuntimeConfigFailsClosedWithoutSecrets(t *testing.T) {
	feature := discoverF10Feature(t)
	database, err := openDatabase(
		"postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	const secret = "must-not-appear-in-runtime-errors"
	config := map[string]string{
		"billing.app_domain":            "app.example.test",
		"billing.paddle_environment":    "production",
		"billing.paddle_webhook_secret": secret,
	}
	var logs bytes.Buffer
	registry := featurehost.NewRegistry()
	registerF10Factory(t, registry)
	host, err := featurehost.New(
		[]featureruntime.Feature{feature},
		registry,
		f10RuntimeDependencies(database, config, &logs),
		featurehost.ValidatedMigrations{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	readyErr := host.Ready(context.Background())
	if readyErr == nil || !strings.Contains(readyErr.Error(), "incomplete Paddle runtime configuration") {
		t.Fatalf("Ready() error = %v, want incomplete configuration", readyErr)
	}
	status := host.Statuses()
	combined := readyErr.Error() + logs.String()
	if len(status) == 1 {
		combined += status[0].Error
	}
	if strings.Contains(combined, secret) {
		t.Fatal("runtime error or log exposed a configured secret")
	}
	response := httptest.NewRecorder()
	host.PublicHandler().ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodPost,
			"/api/v1/billing/paddle/webhook",
			strings.NewReader(`{}`),
		),
	)
	if response.Code != http.StatusNotFound {
		t.Fatalf("incomplete configuration webhook status = %d, want fail-closed 404", response.Code)
	}
}

func registerF10Factory(t *testing.T, registry *featurehost.Registry) {
	t.Helper()
	err := registry.Register(
		"f10-entitlements",
		func(
			_ context.Context,
			_ featureruntime.Feature,
			dependencies featurehost.Dependencies,
		) (featurehost.Module, error) {
			return entitlements.NewPostgresModule(
				dependencies.PostgreSQL,
				dependencies.Clock,
			)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func discoverF10Feature(t *testing.T) featureruntime.Feature {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	features, err := featureruntime.Discover(
		filepath.Join(root, "services", "api", "features"),
		filepath.Join(root, "features"),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, feature := range features {
		if feature.Manifest.ID == "f10-entitlements" {
			return feature
		}
	}
	t.Fatal("f10-entitlements was not discovered")
	return featureruntime.Feature{}
}

func f10RuntimeDependencies(
	database *sql.DB,
	config map[string]string,
	logWriter io.Writer,
) featurehost.Dependencies {
	return featurehost.Dependencies{
		PostgreSQL: database,
		Config:     config,
		Logger:     slog.New(slog.NewTextHandler(logWriter, nil)),
		Clock:      func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	}
}

func completeF10Config(t *testing.T) map[string]string {
	t.Helper()
	mappings := make([]entitlements.PaddlePriceMapping, 0, 14)
	priceIndex := 0
	for planIndex, code := range []entitlements.PlanCode{
		entitlements.PlanPro,
		entitlements.PlanTeam,
		entitlements.PlanUnlimited,
	} {
		plan, err := entitlements.PublicPlanByCode(code)
		if err != nil {
			t.Fatal(err)
		}
		tiers := []entitlements.PriceTierCode{
			entitlements.TierOne,
			entitlements.TierTwo,
			entitlements.TierThree,
		}
		if code == entitlements.PlanUnlimited {
			tiers = []entitlements.PriceTierCode{entitlements.TierFlat}
		}
		for _, interval := range []entitlements.BillingInterval{
			entitlements.IntervalMonthly,
			entitlements.IntervalAnnual,
		} {
			for tierIndex, tier := range tiers {
				amount := plan.Prices.Monthly.AmountCents
				if code != entitlements.PlanUnlimited {
					amount = plan.PriceTiers[tierIndex].Monthly.AmountCents
				}
				if interval == entitlements.IntervalAnnual {
					amount = plan.Prices.Annual.AmountCents
					if code != entitlements.PlanUnlimited {
						amount = plan.PriceTiers[tierIndex].Annual.AmountCents
					}
				}
				mappings = append(mappings, entitlements.PaddlePriceMapping{
					Plan:            code,
					Interval:        interval,
					Tier:            tier,
					ProductID:       "pro_" + strings.Repeat(string(rune('a'+planIndex)), 26),
					PriceID:         "pri_" + runtimePriceSuffix(priceIndex),
					UnitAmountCents: amount,
				})
				priceIndex++
			}
		}
	}
	catalog, err := json.Marshal(mappings)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"billing.app_domain":            "app.example.test",
		"billing.paddle_environment":    "production",
		"billing.paddle_api_key":        "pdl_live_apikey_" + strings.Repeat("a", 26) + "_" + strings.Repeat("b", 22) + "_abc",
		"billing.paddle_webhook_secret": "endpoint-secret-runtime-test",
		"billing.paddle_catalog_json":   string(catalog),
	}
}

func runtimePriceSuffix(index int) string {
	return strings.Repeat("a", 24) + string(rune('a'+index/26)) + string(rune('a'+index%26))
}

func serveRuntimeRequest(
	handler http.Handler,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
