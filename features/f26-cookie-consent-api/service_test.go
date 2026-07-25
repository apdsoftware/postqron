package cookieconsent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

const testPolicyDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testService(t *testing.T) (
	*Service,
	*MemoryRepository,
	*StaticPolicySource,
	*time.Time,
) {
	t.Helper()
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	policies := &StaticPolicySource{Policy: PolicyRelease{
		Version:      "1.0",
		DigestSHA256: testPolicyDigest,
		EffectiveAt:  now.Add(-time.Hour),
	}}
	service, err := NewService(repository, policies, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, policies, &now
}

func TestCookiePreferencesDefaultToStrictOptIn(t *testing.T) {
	service, _, _, _ := testService(t)
	state, err := service.Get(context.Background(), testBrowser("first-visit"))
	if err != nil {
		t.Fatal(err)
	}
	if !state.Necessary || state.Preferences || state.Analytics || state.Marketing ||
		state.HasRecordedChoice || state.SelectedAt != nil || state.ExpiresAt != nil {
		t.Fatalf("unexpected first-visit state: %+v", state)
	}
}

func TestAcceptRejectGranularRevokeAndRetry(t *testing.T) {
	service, _, _, _ := testService(t)
	ctx := context.Background()
	subject := testBrowser("choice-flow")

	accepted, replay, err := service.Put(
		ctx, subject, "1.0",
		Selection{Preferences: true, Analytics: true, Marketing: true},
		"banner", "accept-all-0001",
	)
	if err != nil || replay || !accepted.Preferences ||
		!accepted.Analytics || !accepted.Marketing {
		t.Fatalf("accept = %+v replay=%v err=%v", accepted, replay, err)
	}
	retried, replay, err := service.Put(
		ctx, subject, "1.0",
		Selection{Preferences: true, Analytics: true, Marketing: true},
		"banner", "accept-all-0001",
	)
	if err != nil || !replay || retried.Revision != accepted.Revision {
		t.Fatalf("retry = %+v replay=%v err=%v", retried, replay, err)
	}
	_, _, err = service.Put(
		ctx, subject, "1.0", Selection{},
		"banner", "accept-all-0001",
	)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting retry error = %v", err)
	}

	granular, replay, err := service.Put(
		ctx, subject, "1.0",
		Selection{Preferences: true, Analytics: false, Marketing: true},
		"preferences_center", "granular-0002",
	)
	if err != nil || replay || !granular.Preferences ||
		granular.Analytics || !granular.Marketing {
		t.Fatalf("granular = %+v replay=%v err=%v", granular, replay, err)
	}
	revoked, _, err := service.Put(
		ctx, subject, "1.0", Selection{},
		"preferences_center", "revoke-all-0003",
	)
	if err != nil || revoked.Preferences || revoked.Analytics || revoked.Marketing {
		t.Fatalf("revoke = %+v err=%v", revoked, err)
	}
	exported, err := service.Export(ctx, subject)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Evidence) != 9 {
		t.Fatalf("evidence count = %d, want 9", len(exported.Evidence))
	}
	actions := map[string]EvidenceAction{}
	for _, event := range exported.Evidence {
		if event.PreferenceState == 3 {
			actions[event.Category] = event.Action
		}
	}
	if actions["preferences"] != ActionWithdrawn ||
		actions["marketing"] != ActionWithdrawn ||
		actions["analytics"] != ActionRejected {
		t.Fatalf("revocation evidence = %#v", actions)
	}
}

func TestPolicyChangeAndExpiryDeterministicallyInvalidateChoice(t *testing.T) {
	service, _, policies, now := testService(t)
	subject := testBrowser("policy-change")
	if _, _, err := service.Put(
		context.Background(), subject, "1.0",
		Selection{Analytics: true}, "banner", "policy-old-0001",
	); err != nil {
		t.Fatal(err)
	}
	policies.Policy = PolicyRelease{
		Version:      "1.1",
		DigestSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EffectiveAt:  now.Add(time.Minute),
	}
	*now = now.Add(2 * time.Minute)
	state, err := service.Get(context.Background(), subject)
	if err != nil {
		t.Fatal(err)
	}
	if state.HasRecordedChoice || state.Analytics || state.PolicyVersion != "1.1" {
		t.Fatalf("new policy state = %+v", state)
	}
	if _, _, err := service.Put(
		context.Background(), subject, "1.0",
		Selection{Analytics: true}, "banner", "stale-policy-0002",
	); !errors.Is(err, ErrPolicyMismatch) {
		t.Fatalf("stale version error = %v", err)
	}

	policies.Policy.Version = "1.0"
	policies.Policy.DigestSHA256 = testPolicyDigest
	policies.Policy.EffectiveAt = time.Date(2026, 7, 25, 11, 0, 0, 0, time.UTC)
	*now = time.Date(2027, 1, 26, 12, 0, 0, 0, time.UTC)
	state, err = service.Get(context.Background(), subject)
	if err != nil {
		t.Fatal(err)
	}
	if state.HasRecordedChoice || state.Analytics {
		t.Fatalf("expired state = %+v", state)
	}
}

func TestSubjectsAreIsolatedAndErasureUnlinksRetainedEvidence(t *testing.T) {
	service, repository, _, now := testService(t)
	ctx := context.Background()
	browser := testBrowser("isolation")
	account := Subject{Kind: SubjectAccount, ID: "account-isolation"}
	if _, _, err := service.Put(
		ctx, browser, "1.0", Selection{Analytics: true},
		"banner", "browser-choice-01",
	); err != nil {
		t.Fatal(err)
	}
	accountState, err := service.Get(ctx, account)
	if err != nil || accountState.Analytics || accountState.HasRecordedChoice {
		t.Fatalf("account leaked browser state: %+v err=%v", accountState, err)
	}
	if _, _, err := service.Put(
		ctx, account, "1.0", Selection{Marketing: true},
		"account", "account-choice-01",
	); err != nil {
		t.Fatal(err)
	}
	browserExport, _ := service.Export(ctx, browser)
	accountExport, _ := service.Export(ctx, account)
	if len(browserExport.Evidence) != 3 || len(accountExport.Evidence) != 3 ||
		browserExport.Current.Marketing || !accountExport.Current.Marketing {
		t.Fatalf("isolation exports: browser=%+v account=%+v", browserExport, accountExport)
	}
	if err := service.Erase(ctx, account); err != nil {
		t.Fatal(err)
	}
	erased, err := service.Export(ctx, account)
	if err != nil || erased.Current.HasRecordedChoice || len(erased.Evidence) != 0 {
		t.Fatalf("erased export = %+v err=%v", erased, err)
	}
	if len(repository.events) != 6 {
		t.Fatalf("retained anonymous evidence = %d, want 6", len(repository.events))
	}
	*now = now.Add(evidenceRetention + time.Second)
	purged, err := service.PurgeExpiredEvidence(ctx)
	if err != nil || purged != 6 {
		t.Fatalf("purge = %d, %v", purged, err)
	}
}

func TestConcurrentUpdatesRemainAtomic(t *testing.T) {
	service, _, _, _ := testService(t)
	subject := testBrowser("concurrent")
	var wait sync.WaitGroup
	errorsChannel := make(chan error, 12)
	for index := range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, err := service.Put(
				context.Background(),
				subject,
				"1.0",
				Selection{Analytics: index%2 == 0},
				"banner",
				fmt.Sprintf("concurrent-%04d", index),
			)
			errorsChannel <- err
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	exported, err := service.Export(context.Background(), subject)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Current.Revision != 12 || len(exported.Evidence) != 36 {
		t.Fatalf(
			"concurrent result revision=%d evidence=%d",
			exported.Current.Revision,
			len(exported.Evidence),
		)
	}
}

func testBrowser(suffix string) Subject {
	return Subject{Kind: SubjectBrowser, ID: "browser-" + suffix}
}
