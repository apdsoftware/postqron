package scheduling

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostgresUpgradeFrom000003PreservesDuplicateRecoveryAndFailsClosedReplay(t *testing.T) {
	databaseURL := os.Getenv("F07_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F07_DATABASE_URL is not configured")
	}

	t.Run("prepared duplicate reuses the downstream key committed before upgrade", func(t *testing.T) {
		database := newF07LegacyUpgradeDatabase(t, databaseURL)
		ctx := context.Background()
		applyF07Migration(t, database, "000001_create_scheduled_posts.sql")
		applyF07Migration(t, database, "000002_add_draft_revision_to_scheduling.sql")
		applyF07Migration(t, database, "000003_add_idempotency_operations.sql")

		now := time.Now().UTC().Truncate(time.Second)
		workspaceID := "workspace-upgrade-prepared"
		rawKey := "legacy-prepared-private-key"
		sourceID := "post_upgrade_source"
		fingerprint, err := duplicatePayloadFingerprint(sourceID, 1, nil)
		if err != nil {
			t.Fatal(err)
		}
		legacyOperation := IdempotencyOperation{
			WorkspaceID:        workspaceID,
			Kind:               OperationDuplicate,
			IdempotencyKey:     rawKey,
			PayloadFingerprint: fingerprint,
		}
		legacyDownstreamKey := deriveComposerDuplicateIdempotencyKey(legacyOperation)
		insertLegacyWorkspace(t, database, workspaceID)
		insertLegacyScheduledPost(t, database, legacyScheduledPost{
			id: sourceID, workspaceID: workspaceID, draftID: "draft-upgrade-source",
			status: StatusScheduled, revision: 1, scheduledFor: now.Add(2 * time.Hour),
		})
		if _, err := database.ExecContext(ctx, `
			INSERT INTO f07_idempotency_operations (
				workspace_id, operation_kind, idempotency_key, payload_fingerprint,
				actor_account_id, state, post_id, publication_command_id,
				source_post_id, source_post_revision, source_draft_id,
				source_draft_revision, channel_ids, scheduled_for_utc,
				scheduled_local, scheduled_timezone, scheduled_utc_offset_minutes,
				lease_generation, locked_until, created_at, updated_at
			) VALUES (
				$1, 'duplicate', $2, $3, 'legacy-account', 'prepared',
				'post_upgrade_duplicate', 'pubcmd_upgrade_duplicate',
				$4, 1, 'draft-upgrade-source', 1, ARRAY['channel-1'], $5,
				$6, 'UTC', 0, 1, $7, $8, $8
			)
		`, workspaceID, rawKey, fingerprint, sourceID, now.Add(2*time.Hour),
			now.Add(2*time.Hour).Format(localDateTimeLayout), now.Add(-time.Minute),
			now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}

		content := newLegacyIdempotentContentGateway(
			legacyDownstreamKey,
			DuplicatedDraft{DraftID: "draft-upgrade-clone", DraftRevision: 4},
		)
		applyF07Migration(t, database, "000004_harden_idempotency_recovery.sql")

		var persistedKey, persistedDownstreamKey, rowText string
		if err := database.QueryRowContext(ctx, `
			SELECT idempotency_key, downstream_idempotency_key, operation::text
			FROM f07_idempotency_operations operation
			WHERE workspace_id = $1 AND operation_kind = 'duplicate'
		`, workspaceID).Scan(&persistedKey, &persistedDownstreamKey, &rowText); err != nil {
			t.Fatal(err)
		}
		if persistedKey != idempotencyKeyDigest(rawKey) ||
			persistedDownstreamKey != legacyDownstreamKey ||
			strings.Contains(rowText, rawKey) {
			t.Fatalf("key=%q downstream=%q row=%q", persistedKey, persistedDownstreamKey, rowText)
		}

		repository, err := NewPostgresRepository(database)
		if err != nil {
			t.Fatal(err)
		}
		service, err := NewService(
			repository,
			authorizerStub{allowed: true},
			content,
			WithClock(func() time.Time { return now }),
		)
		if err != nil {
			t.Fatal(err)
		}
		duplicate, err := service.DuplicatePost(ctx, DuplicatePostCommand{
			WorkspaceID: workspaceID, ActorID: "account-after-upgrade",
			IdempotencyKey: rawKey, PostID: sourceID, ExpectedRevision: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if duplicate.DraftID != "draft-upgrade-clone" || content.creationCount() != 1 {
			t.Fatalf("duplicate=%#v downstream creations=%d", duplicate, content.creationCount())
		}
		if keys := content.requestedKeys(); len(keys) != 1 || keys[0] != legacyDownstreamKey {
			t.Fatalf("F6 retry keys=%v want=%q", keys, legacyDownstreamKey)
		}
	})

	t.Run("completed operation with mutated post never fabricates a replay", func(t *testing.T) {
		database := newF07LegacyUpgradeDatabase(t, databaseURL)
		ctx := context.Background()
		applyF07Migration(t, database, "000001_create_scheduled_posts.sql")
		applyF07Migration(t, database, "000002_add_draft_revision_to_scheduling.sql")
		applyF07Migration(t, database, "000003_add_idempotency_operations.sql")

		now := time.Now().UTC().Truncate(time.Second)
		workspaceID := "workspace-upgrade-completed"
		rawKey := "legacy-completed-private-key"
		postID := "post_upgrade_completed"
		scheduledFor := now.Add(2 * time.Hour)
		schedule := resolvedSchedule{
			utc: scheduledFor, local: scheduledFor.Format(localDateTimeLayout),
			timeZone: "UTC", offsetMinutes: 0,
		}
		fingerprint, err := schedulePayloadFingerprint(
			"draft-original", []string{"channel-1"}, schedule,
		)
		if err != nil {
			t.Fatal(err)
		}
		insertLegacyWorkspace(t, database, workspaceID)
		insertLegacyScheduledPost(t, database, legacyScheduledPost{
			id: postID, workspaceID: workspaceID, draftID: "draft-mutated",
			status: StatusCancelled, revision: 3, scheduledFor: scheduledFor.Add(time.Hour),
		})
		if _, err := database.ExecContext(ctx, `
			INSERT INTO f07_idempotency_operations (
				workspace_id, operation_kind, idempotency_key, payload_fingerprint,
				actor_account_id, state, post_id, publication_command_id,
				lease_generation, locked_until, created_at, updated_at, completed_at
			) VALUES (
				$1, 'schedule', $2, $3, 'legacy-account', 'completed', $4,
				'pubcmd_upgrade_completed', 1, NULL, $5, $5, $5
			)
		`, workspaceID, rawKey, fingerprint, postID, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}

		applyF07Migration(t, database, "000004_harden_idempotency_recovery.sql")
		var snapshotStatus string
		var snapshot []byte
		if err := database.QueryRowContext(ctx, `
			SELECT response_snapshot_status, response_snapshot
			FROM f07_idempotency_operations
			WHERE workspace_id = $1 AND operation_kind = 'schedule'
		`, workspaceID).Scan(&snapshotStatus, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshotStatus != string(ResponseSnapshotLegacyUnavailable) || len(snapshot) != 0 {
			t.Fatalf("snapshot status=%q snapshot=%s", snapshotStatus, snapshot)
		}

		repository, err := NewPostgresRepository(database)
		if err != nil {
			t.Fatal(err)
		}
		service, err := NewService(
			repository,
			authorizerStub{allowed: true},
			&contentGatewayStub{},
			WithClock(func() time.Time { return now }),
		)
		if err != nil {
			t.Fatal(err)
		}
		handler := NewHTTPHandler(service, authenticatorStub{accountID: "account-after-upgrade"})
		response := performSchedulingRequestWithKey(
			handler,
			http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/scheduled-posts",
			`{"draft_id":"draft-original","channel_ids":["channel-1"],`+
				`"scheduled_at":{"local_date_time":"`+schedule.local+`","time_zone":"UTC"}}`,
			rawKey,
		)
		if response.Code != http.StatusConflict ||
			!strings.Contains(response.Body.String(), `"code":"idempotency_replay_unavailable"`) ||
			!strings.Contains(response.Body.String(), `"retryable":false`) ||
			strings.Contains(response.Body.String(), postID) ||
			strings.Contains(response.Body.String(), "draft-mutated") {
			t.Fatalf("legacy replay status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func newF07LegacyUpgradeDatabase(t *testing.T, databaseURL string) *sql.DB {
	t.Helper()
	ctx := context.Background()
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := fmt.Sprintf("f07_upgrade_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if _, err := database.ExecContext(ctx, `SET search_path TO `+schema); err != nil {
		database.Close()
		admin.Close()
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `CREATE TABLE f04_workspaces (id text PRIMARY KEY)`); err != nil {
		database.Close()
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		_ = admin.Close()
	})
	return database
}

func applyF07Migration(t *testing.T, database *sql.DB, name string) {
	t.Helper()
	migration, err := os.ReadFile("migrations/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(context.Background(), string(migration)); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}

func insertLegacyWorkspace(t *testing.T, database *sql.DB, workspaceID string) {
	t.Helper()
	if _, err := database.ExecContext(
		context.Background(),
		`INSERT INTO f04_workspaces (id) VALUES ($1)`,
		workspaceID,
	); err != nil {
		t.Fatal(err)
	}
}

type legacyScheduledPost struct {
	id           string
	workspaceID  string
	draftID      string
	status       PostStatus
	revision     int64
	scheduledFor time.Time
}

func insertLegacyScheduledPost(t *testing.T, database *sql.DB, post legacyScheduledPost) {
	t.Helper()
	activeCommandID := any("pubcmd_legacy_source")
	cancelledAt := any(nil)
	if post.status == StatusCancelled {
		activeCommandID = nil
		cancelledAt = post.scheduledFor.Add(-time.Minute)
	}
	if _, err := database.ExecContext(context.Background(), `
		INSERT INTO f07_scheduled_posts (
			id, workspace_id, draft_id, draft_revision, channel_ids, status,
			scheduled_for_utc, scheduled_local, scheduled_timezone,
			scheduled_utc_offset_minutes, revision, active_command_id,
			created_by_account_id, created_at, updated_at, cancelled_at
		) VALUES (
			$1, $2, $3, 1, ARRAY['channel-1'], $4, $5, $6, 'UTC', 0, $7, $8,
			'legacy-account', $9, $10, $11
		)
	`, post.id, post.workspaceID, post.draftID, string(post.status), post.scheduledFor,
		post.scheduledFor.Format(localDateTimeLayout), post.revision, activeCommandID,
		post.scheduledFor.Add(-2*time.Hour), post.scheduledFor.Add(-time.Minute),
		cancelledAt); err != nil {
		t.Fatal(err)
	}
}

type legacyIdempotentContentGateway struct {
	mutex     sync.Mutex
	drafts    map[string]DuplicatedDraft
	keys      []string
	creations int
}

func newLegacyIdempotentContentGateway(
	key string,
	draft DuplicatedDraft,
) *legacyIdempotentContentGateway {
	return &legacyIdempotentContentGateway{
		drafts:    map[string]DuplicatedDraft{key: draft},
		creations: 1,
	}
}

func (gateway *legacyIdempotentContentGateway) ValidateForScheduling(
	_ context.Context,
	_, _ string,
	draftID string,
	channelIDs []string,
) (ValidatedDraft, error) {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	for _, draft := range gateway.drafts {
		if draft.DraftID == draftID {
			return ValidatedDraft{
				DraftID: draft.DraftID, DraftRevision: draft.DraftRevision,
				ChannelIDs: append([]string(nil), channelIDs...),
			}, nil
		}
	}
	return ValidatedDraft{}, ErrDraftNotFound
}

func (gateway *legacyIdempotentContentGateway) DuplicateDraft(
	_ context.Context,
	_, _, sourceDraftID string,
	_ int64,
	idempotencyKey string,
) (DuplicatedDraft, error) {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	gateway.keys = append(gateway.keys, idempotencyKey)
	if draft, exists := gateway.drafts[idempotencyKey]; exists {
		return draft, nil
	}
	gateway.creations++
	draft := DuplicatedDraft{
		DraftID:       fmt.Sprintf("%s-upgrade-copy-%d", sourceDraftID, gateway.creations),
		DraftRevision: 1,
	}
	gateway.drafts[idempotencyKey] = draft
	return draft, nil
}

func (gateway *legacyIdempotentContentGateway) creationCount() int {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	return gateway.creations
}

func (gateway *legacyIdempotentContentGateway) requestedKeys() []string {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	return append([]string(nil), gateway.keys...)
}
