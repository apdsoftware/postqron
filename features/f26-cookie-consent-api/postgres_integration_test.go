package cookieconsent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresRepositoryIntegration(t *testing.T) {
	databaseURL := integrationDatabaseURL()
	if databaseURL == "" {
		t.Skip("set F26_DATABASE_URL or DATABASE_URL after applying the F26 migration")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	policies := &StaticPolicySource{Policy: PolicyRelease{
		Version:      "7.4",
		DigestSHA256: testPolicyDigest,
		EffectiveAt:  now.Add(-time.Hour),
	}}
	service, err := NewService(repository, policies, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	subject := Subject{Kind: SubjectBrowser, ID: "integration-browser-" + suffix}
	other := Subject{Kind: SubjectAccount, ID: "integration-account-" + suffix}

	first, replay, err := service.Put(
		context.Background(),
		subject,
		"7.4",
		Selection{Analytics: true},
		"banner",
		"integration-first-"+suffix,
	)
	if err != nil || replay || !first.Analytics || first.Revision != 1 {
		t.Fatalf("first=%+v replay=%v err=%v", first, replay, err)
	}
	retried, replay, err := service.Put(
		context.Background(),
		subject,
		"7.4",
		Selection{Analytics: true},
		"banner",
		"integration-first-"+suffix,
	)
	if err != nil || !replay || retried.Revision != 1 {
		t.Fatalf("retry=%+v replay=%v err=%v", retried, replay, err)
	}
	_, _, err = service.Put(
		context.Background(),
		subject,
		"7.4",
		Selection{Marketing: true},
		"banner",
		"integration-first-"+suffix,
	)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting retry error=%v", err)
	}

	const updates = 8
	var wait sync.WaitGroup
	errorChannel := make(chan error, updates)
	for index := range updates {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, err := service.Put(
				context.Background(),
				subject,
				"7.4",
				Selection{Marketing: index%2 == 0},
				"preferences_center",
				fmt.Sprintf("integration-concurrent-%d-%s", index, suffix),
			)
			errorChannel <- err
		}()
	}
	wait.Wait()
	close(errorChannel)
	for err := range errorChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	exported, err := service.Export(context.Background(), subject)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Current.Revision != updates+1 ||
		len(exported.Evidence) != (updates+1)*3 {
		t.Fatalf(
			"revision=%d evidence=%d",
			exported.Current.Revision,
			len(exported.Evidence),
		)
	}
	isolated, err := service.Get(context.Background(), other)
	if err != nil || isolated.HasRecordedChoice {
		t.Fatalf("isolated=%+v err=%v", isolated, err)
	}
	if err := service.Erase(context.Background(), subject); err != nil {
		t.Fatal(err)
	}
	erased, err := service.Export(context.Background(), subject)
	if err != nil || erased.Current.HasRecordedChoice || len(erased.Evidence) != 0 {
		t.Fatalf("erased=%+v err=%v", erased, err)
	}
}

func TestIntegrationDatabaseURLPrefersFeatureOverrideAndFallsBackToCI(t *testing.T) {
	t.Setenv("F26_DATABASE_URL", "feature-override-value")
	t.Setenv("DATABASE_URL", "ci-fallback-value")
	if got := integrationDatabaseURL(); got != "feature-override-value" {
		t.Fatalf("integrationDatabaseURL() = %q, want feature override", got)
	}

	t.Setenv("F26_DATABASE_URL", "")
	if got := integrationDatabaseURL(); got != "ci-fallback-value" {
		t.Fatalf("integrationDatabaseURL() = %q, want CI fallback", got)
	}
}

func integrationDatabaseURL() string {
	if databaseURL := os.Getenv("F26_DATABASE_URL"); databaseURL != "" {
		return databaseURL
	}
	return os.Getenv("DATABASE_URL")
}
