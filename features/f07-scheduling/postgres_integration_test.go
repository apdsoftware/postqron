package scheduling

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresRepositoryKeepsPostAndCommandAtomic(t *testing.T) {
	databaseURL := os.Getenv("F07_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F07_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	now := time.Now().UTC()
	post := ScheduledPost{
		ID:               "post_integration_" + suffix,
		WorkspaceID:      "workspace-integration-" + suffix,
		DraftID:          "draft-integration",
		DraftRevision:    9,
		ChannelIDs:       []string{"channel-integration"},
		Status:           StatusScheduled,
		ScheduledForUTC:  now.Add(time.Hour),
		ScheduledLocal:   now.Add(time.Hour).Format(localDateTimeLayout),
		TimeZone:         "UTC",
		UTCOffsetMinutes: 0,
		Revision:         1,
		ActiveCommandID:  "pubcmd_integration_" + suffix,
		CreatedBy:        "account-integration",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	command := commandFor(post, post.ActiveCommandID, now)
	created, err := repository.Create(ctx, post, command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.ExecContext(
			context.Background(),
			"DELETE FROM f07_publication_commands WHERE post_id = $1",
			created.ID,
		)
		_, _ = database.ExecContext(
			context.Background(),
			"DELETE FROM f07_scheduled_posts WHERE id = $1",
			created.ID,
		)
	})

	oldCommandID := created.ActiveCommandID
	replacement := clonePost(created)
	replacement.Revision = 2
	replacement.ScheduledForUTC = replacement.ScheduledForUTC.Add(time.Hour)
	replacement.ScheduledLocal = replacement.ScheduledForUTC.Format(localDateTimeLayout)
	replacement.ActiveCommandID = "pubcmd_replacement_" + suffix
	replacement.UpdatedAt = now.Add(time.Minute)
	replaced, err := repository.Replace(
		ctx,
		replacement,
		1,
		commandFor(replacement, replacement.ActiveCommandID, replacement.UpdatedAt),
	)
	if err != nil {
		t.Fatal(err)
	}
	commands, err := repository.ListPublicationCommands(ctx, post.WorkspaceID, post.ID)
	if err != nil || len(commands) != 2 {
		t.Fatalf("commands=%#v err=%v", commands, err)
	}
	if commands[0].ID != oldCommandID ||
		commands[0].DraftRevision != created.DraftRevision ||
		commands[0].State != CommandInvalidated ||
		commands[1].State != CommandPending ||
		commands[1].ID != replaced.ActiveCommandID ||
		commands[1].DraftRevision != replaced.DraftRevision {
		t.Fatalf("commands=%#v", commands)
	}

	brokenPost := post
	brokenPost.ID = "post_broken_" + suffix
	brokenPost.ActiveCommandID = "not-a-valid-command-id"
	brokenCommand := commandFor(
		brokenPost,
		brokenPost.ActiveCommandID,
		now,
	)
	if _, err := repository.Create(ctx, brokenPost, brokenCommand); err == nil {
		t.Fatal("invalid command should fail the transaction")
	}
	if _, err := repository.Get(
		ctx,
		brokenPost.WorkspaceID,
		brokenPost.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("post survived failed command insert: %v", err)
	}
}

func TestPostgresIdempotencyConcurrencyAmbiguousCommitAndDuplicateRecovery(t *testing.T) {
	databaseURL := os.Getenv("F07_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F07_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	base, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}

	newPostgresService := func(
		t *testing.T,
		repository Repository,
		now time.Time,
	) (*Service, *contentGatewayStub) {
		t.Helper()
		content := &contentGatewayStub{}
		service, serviceErr := NewService(
			repository,
			authorizerStub{allowed: true},
			content,
			WithClock(func() time.Time { return now }),
		)
		if serviceErr != nil {
			t.Fatal(serviceErr)
		}
		return service, content
	}
	cleanupWorkspace := func(t *testing.T, workspaceID string) {
		t.Helper()
		if _, err := database.ExecContext(ctx, `
			INSERT INTO f04_workspaces (
				id, personal_account_id, name, status, created_at, updated_at
			) VALUES ($1, $2, 'F7 integration', 'active', $3, $3)
			ON CONFLICT (id) DO NOTHING
		`, workspaceID, "account-for-"+workspaceID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = database.ExecContext(context.Background(),
				"DELETE FROM f07_publication_commands WHERE workspace_id = $1", workspaceID)
			_, _ = database.ExecContext(context.Background(),
				"DELETE FROM f07_scheduled_posts WHERE workspace_id = $1", workspaceID)
			_, _ = database.ExecContext(context.Background(),
				"DELETE FROM f07_idempotency_operations WHERE workspace_id = $1", workspaceID)
			_, _ = database.ExecContext(context.Background(),
				"DELETE FROM f04_workspaces WHERE id = $1", workspaceID)
		})
	}

	t.Run("concurrent schedule converges to one post and mismatch stays bound", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		workspaceID := "workspace-f07-concurrency-" + fmt.Sprint(now.UnixNano())
		cleanupWorkspace(t, workspaceID)
		service, _ := newPostgresService(t, base, now)
		command := SchedulePostCommand{
			WorkspaceID:    workspaceID,
			ActorID:        "account-1",
			IdempotencyKey: "schedule-concurrent-1",
			DraftID:        "draft-1",
			ChannelIDs:     []string{"channel-1"},
			Schedule: ScheduleInput{
				LocalDateTime: now.Add(2 * time.Hour).Format(localDateTimeLayout),
				TimeZone:      "UTC",
			},
		}
		const contenders = 16
		ids := make(chan string, contenders)
		errorsFound := make(chan error, contenders)
		var waitGroup sync.WaitGroup
		waitGroup.Add(contenders)
		for index := 0; index < contenders; index++ {
			go func() {
				defer waitGroup.Done()
				for attempt := 0; attempt < 100; attempt++ {
					post, scheduleErr := service.SchedulePost(ctx, command)
					if scheduleErr == nil {
						ids <- post.ID
						return
					}
					if !errors.Is(scheduleErr, ErrOperationInProgress) {
						errorsFound <- scheduleErr
						return
					}
					time.Sleep(time.Millisecond)
				}
				errorsFound <- errors.New("idempotency retry limit exceeded")
			}()
		}
		waitGroup.Wait()
		close(ids)
		close(errorsFound)
		for concurrencyErr := range errorsFound {
			t.Error(concurrencyErr)
		}
		var postID string
		for id := range ids {
			if postID == "" {
				postID = id
			}
			if id != postID {
				t.Fatalf("idempotent results diverged: %q != %q", id, postID)
			}
		}
		var posts, commands, operations int
		if err := database.QueryRowContext(ctx,
			"SELECT count(*) FROM f07_scheduled_posts WHERE workspace_id = $1", workspaceID,
		).Scan(&posts); err != nil {
			t.Fatal(err)
		}
		if err := database.QueryRowContext(ctx,
			"SELECT count(*) FROM f07_publication_commands WHERE workspace_id = $1", workspaceID,
		).Scan(&commands); err != nil {
			t.Fatal(err)
		}
		if err := database.QueryRowContext(ctx,
			"SELECT count(*) FROM f07_idempotency_operations WHERE workspace_id = $1 AND state = 'completed'", workspaceID,
		).Scan(&operations); err != nil {
			t.Fatal(err)
		}
		if posts != 1 || commands != 1 || operations != 1 {
			t.Fatalf("posts=%d commands=%d completed operations=%d", posts, commands, operations)
		}
		mismatch := command
		mismatch.DraftID = "draft-2"
		if _, err := service.SchedulePost(ctx, mismatch); !errors.Is(err, ErrIdempotencyMismatch) {
			t.Fatalf("payload mismatch error=%v", err)
		}
	})

	t.Run("committed schedule is replayed after ambiguous response", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		workspaceID := "workspace-f07-ambiguous-" + fmt.Sprint(now.UnixNano())
		cleanupWorkspace(t, workspaceID)
		faults := &repositoryFaults{Repository: base}
		faults.failScheduleAfterCommit.Store(true)
		service, _ := newPostgresService(t, faults, now)
		command := SchedulePostCommand{
			WorkspaceID: workspaceID, ActorID: "account-1",
			IdempotencyKey: "test-key-1", DraftID: "draft-1",
			ChannelIDs: []string{"channel-1"},
			Schedule: ScheduleInput{
				LocalDateTime: now.Add(2 * time.Hour).Format(localDateTimeLayout),
				TimeZone:      "UTC",
			},
		}
		if _, err := service.SchedulePost(ctx, command); err == nil {
			t.Fatal("expected ambiguous response error")
		}
		var postID string
		if err := database.QueryRowContext(ctx, `
			SELECT post_id FROM f07_idempotency_operations
			WHERE workspace_id = $1 AND operation_kind = 'schedule' AND idempotency_key = $2
		`, workspaceID, idempotencyKeyDigest(command.IdempotencyKey)).Scan(&postID); err != nil {
			t.Fatal(err)
		}
		original, err := base.Get(ctx, workspaceID, postID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.ReschedulePost(ctx, ReschedulePostCommand{
			WorkspaceID: workspaceID, ActorID: "account-1", PostID: postID,
			ExpectedRevision: 1,
			Schedule: ScheduleInput{
				LocalDateTime: now.Add(4 * time.Hour).Format(localDateTimeLayout),
				TimeZone:      "UTC",
			},
		}); err != nil {
			t.Fatal(err)
		}
		replay, err := service.SchedulePost(ctx, command)
		if err != nil || !replay.IdempotencyReplayed ||
			!sameScheduledPostResponse(t, replay, original) {
			t.Fatalf("replay=%#v err=%v", replay, err)
		}
	})

	t.Run("committed duplicate is replayed after ambiguous response", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		workspaceID := "workspace-f07-duplicate-ambiguous-" + fmt.Sprint(now.UnixNano())
		cleanupWorkspace(t, workspaceID)
		faults := &repositoryFaults{Repository: base}
		service, content := newPostgresService(t, faults, now)
		source, err := service.SchedulePost(ctx, SchedulePostCommand{
			WorkspaceID: workspaceID, ActorID: "account-1",
			IdempotencyKey: "schedule-source-1", DraftID: "draft-source",
			ChannelIDs: []string{"channel-1"},
			Schedule: ScheduleInput{
				LocalDateTime: now.Add(3 * time.Hour).Format(localDateTimeLayout),
				TimeZone:      "UTC",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		faults.failDuplicateAfterCommit.Store(true)
		command := DuplicatePostCommand{
			WorkspaceID: workspaceID, ActorID: "account-1",
			IdempotencyKey: "test-key-1", PostID: source.ID,
			ExpectedRevision: source.Revision,
		}
		if _, err := service.DuplicatePost(ctx, command); err == nil {
			t.Fatal("expected ambiguous duplicate response")
		}
		var duplicateID string
		if err := database.QueryRowContext(ctx, `
			SELECT post_id FROM f07_idempotency_operations
			WHERE workspace_id = $1 AND operation_kind = 'duplicate' AND idempotency_key = $2
		`, workspaceID, idempotencyKeyDigest(command.IdempotencyKey)).Scan(&duplicateID); err != nil {
			t.Fatal(err)
		}
		original, err := base.Get(ctx, workspaceID, duplicateID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := base.Cancel(ctx, workspaceID, duplicateID, 1, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		replay, err := service.DuplicatePost(ctx, command)
		if err != nil || !replay.IdempotencyReplayed || content.duplicates != 1 ||
			!sameScheduledPostResponse(t, replay, original) {
			t.Fatalf("replay=%#v err=%v clone calls=%d", replay, err, content.duplicates)
		}
	})

	t.Run("clone recovery point survives failure before post commit", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		workspaceID := "workspace-f07-duplicate-recovery-" + fmt.Sprint(now.UnixNano())
		cleanupWorkspace(t, workspaceID)
		faults := &repositoryFaults{Repository: base}
		service, content := newPostgresService(t, faults, now)
		source, err := service.SchedulePost(ctx, SchedulePostCommand{
			WorkspaceID: workspaceID, ActorID: "account-1",
			IdempotencyKey: "schedule-source-1", DraftID: "draft-source",
			ChannelIDs: []string{"channel-1"},
			Schedule: ScheduleInput{
				LocalDateTime: now.Add(3 * time.Hour).Format(localDateTimeLayout),
				TimeZone:      "UTC",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		faults.failDuplicateBeforeCommit.Store(true)
		duplicateCommand := DuplicatePostCommand{
			WorkspaceID: workspaceID, ActorID: "account-1",
			IdempotencyKey: "duplicate-recovery-1", PostID: source.ID,
			ExpectedRevision: source.Revision,
		}
		if _, err := service.DuplicatePost(ctx, duplicateCommand); err == nil {
			t.Fatal("expected injected failure after clone")
		}
		var state, cloneDraftID string
		if err := database.QueryRowContext(ctx, `
			SELECT state, COALESCE(clone_draft_id, '')
			FROM f07_idempotency_operations
			WHERE workspace_id = $1 AND operation_kind = 'duplicate' AND idempotency_key = $2
		`, workspaceID, idempotencyKeyDigest(duplicateCommand.IdempotencyKey)).Scan(&state, &cloneDraftID); err != nil {
			t.Fatal(err)
		}
		if state != string(OperationCloneCreated) || cloneDraftID == "" || content.duplicates != 1 {
			t.Fatalf("state=%q clone=%q clone calls=%d", state, cloneDraftID, content.duplicates)
		}
		duplicate, err := service.DuplicatePost(ctx, duplicateCommand)
		if err != nil || duplicate.DraftID != cloneDraftID || content.duplicates != 1 {
			t.Fatalf("duplicate=%#v err=%v clone calls=%d", duplicate, err, content.duplicates)
		}
	})

	t.Run("duplicate prepare rejects a source changed after read", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		workspaceID := "workspace-f07-prepare-race-" + fmt.Sprint(now.UnixNano())
		cleanupWorkspace(t, workspaceID)
		service, _ := newPostgresService(t, base, now)
		source, err := service.SchedulePost(ctx, SchedulePostCommand{
			WorkspaceID: workspaceID, ActorID: "account-1",
			IdempotencyKey: "prepare-source", DraftID: "draft-source",
			ChannelIDs: []string{"channel-1"},
			Schedule: ScheduleInput{
				LocalDateTime: now.Add(3 * time.Hour).Format(localDateTimeLayout),
				TimeZone:      "UTC",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		candidate := IdempotencyOperation{
			WorkspaceID: workspaceID, Kind: OperationDuplicate,
			IdempotencyKey:       idempotencyKeyDigest("prepare-race"),
			PayloadFingerprint:   strings.Repeat("b", 64),
			PostID:               "post_prepare_race_" + fmt.Sprint(now.UnixNano()),
			PublicationCommandID: "pubcmd_prepare_race_" + fmt.Sprint(now.UnixNano()),
		}
		operation, err := base.ReserveOperation(ctx, candidate, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := base.Cancel(ctx, workspaceID, source.ID, source.Revision, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		schedule := resolvedSchedule{
			utc: source.ScheduledForUTC, local: source.ScheduledLocal,
			timeZone: source.TimeZone, offsetMinutes: source.UTCOffsetMinutes,
		}
		if _, err := base.PrepareDuplicateOperation(ctx, operation, source, schedule, now); !errors.Is(err, ErrConflict) {
			t.Fatalf("prepare conflict=%v", err)
		}
		var state string
		if err := database.QueryRowContext(ctx, `
			SELECT state FROM f07_idempotency_operations
			WHERE workspace_id = $1 AND operation_kind = 'duplicate'
			  AND idempotency_key = $2
		`, workspaceID, candidate.IdempotencyKey).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state != string(OperationReserved) {
			t.Fatalf("state after prepare conflict=%q", state)
		}
	})

	for _, mutation := range []string{"edit", "cancel"} {
		t.Run("duplicate completion rejects concurrent source "+mutation, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			workspaceID := "workspace-f07-source-race-" + mutation + "-" + fmt.Sprint(now.UnixNano())
			cleanupWorkspace(t, workspaceID)
			faults := &repositoryFaults{Repository: base}
			service, content := newPostgresService(t, faults, now)
			source, err := service.SchedulePost(ctx, SchedulePostCommand{
				WorkspaceID: workspaceID, ActorID: "account-1",
				IdempotencyKey: "source-race-" + mutation, DraftID: "draft-source",
				ChannelIDs: []string{"channel-1"},
				Schedule: ScheduleInput{
					LocalDateTime: now.Add(3 * time.Hour).Format(localDateTimeLayout),
					TimeZone:      "UTC",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			var mutationErr error
			var once sync.Once
			faults.beforeDuplicateComplete = func() {
				once.Do(func() {
					if mutation == "cancel" {
						_, mutationErr = base.Cancel(ctx, workspaceID, source.ID, 1, now.Add(time.Minute))
						return
					}
					replacement := clonePost(source)
					replacement.DraftID = "draft-edited"
					replacement.DraftRevision++
					replacement.Revision++
					replacement.ActiveCommandID = "pubcmd_race_edit_" + fmt.Sprint(now.UnixNano())
					replacement.UpdatedAt = now.Add(time.Minute)
					_, mutationErr = base.Replace(
						ctx,
						replacement,
						1,
						commandFor(replacement, replacement.ActiveCommandID, replacement.UpdatedAt),
					)
				})
			}
			duplicateCommand := DuplicatePostCommand{
				WorkspaceID: workspaceID, ActorID: "account-1",
				IdempotencyKey: "duplicate-race-" + mutation,
				PostID:         source.ID, ExpectedRevision: 1,
			}
			if _, err := service.DuplicatePost(ctx, duplicateCommand); !errors.Is(err, ErrConflict) {
				t.Fatalf("duplicate error=%v", err)
			}
			if mutationErr != nil {
				t.Fatalf("source mutation error=%v", mutationErr)
			}
			var state, cloneDraftID, reservedPostID string
			if err := database.QueryRowContext(ctx, `
				SELECT state, COALESCE(clone_draft_id, ''), post_id
				FROM f07_idempotency_operations
				WHERE workspace_id = $1 AND operation_kind = 'duplicate'
				  AND idempotency_key = $2
			`, workspaceID, idempotencyKeyDigest(duplicateCommand.IdempotencyKey)).Scan(
				&state,
				&cloneDraftID,
				&reservedPostID,
			); err != nil {
				t.Fatal(err)
			}
			var scheduledDuplicateCount int
			if err := database.QueryRowContext(ctx, `
				SELECT count(*) FROM f07_scheduled_posts
				WHERE workspace_id = $1 AND id = $2
			`, workspaceID, reservedPostID).Scan(&scheduledDuplicateCount); err != nil {
				t.Fatal(err)
			}
			if state != string(OperationCloneCreated) || cloneDraftID == "" ||
				scheduledDuplicateCount != 0 || content.duplicates != 1 {
				t.Fatalf("state=%q clone=%q scheduled=%d clone calls=%d", state, cloneDraftID, scheduledDuplicateCount, content.duplicates)
			}
			if _, err := service.DuplicatePost(ctx, duplicateCommand); !errors.Is(err, ErrConflict) || content.duplicates != 1 {
				t.Fatalf("recovery conflict=%v clone calls=%d", err, content.duplicates)
			}
		})
	}

	t.Run("privacy minimization and workspace cascade remove reservation identifiers", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		workspaceID := "workspace-f07-privacy-" + fmt.Sprint(now.UnixNano())
		cleanupWorkspace(t, workspaceID)
		service, _ := newPostgresService(t, base, now)
		rawKey := "customer@example.test-private-key"
		actorID := "account-personal-identifier"
		if _, err := service.SchedulePost(ctx, SchedulePostCommand{
			WorkspaceID: workspaceID, ActorID: actorID, IdempotencyKey: rawKey,
			DraftID: "draft-privacy", ChannelIDs: []string{"channel-1"},
			Schedule: ScheduleInput{
				LocalDateTime: now.Add(2 * time.Hour).Format(localDateTimeLayout),
				TimeZone:      "UTC",
			},
		}); err != nil {
			t.Fatal(err)
		}
		var actorColumnCount int
		if err := database.QueryRowContext(ctx, `
			SELECT count(*) FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'f07_idempotency_operations'
			  AND column_name = 'actor_account_id'
		`).Scan(&actorColumnCount); err != nil {
			t.Fatal(err)
		}
		var persistedKey, operationText string
		if err := database.QueryRowContext(ctx, `
			SELECT idempotency_key, operation::text
			FROM f07_idempotency_operations operation
			WHERE workspace_id = $1 AND operation_kind = 'schedule'
		`, workspaceID).Scan(&persistedKey, &operationText); err != nil {
			t.Fatal(err)
		}
		if actorColumnCount != 0 || persistedKey != idempotencyKeyDigest(rawKey) ||
			strings.Contains(operationText, rawKey) || strings.Contains(operationText, actorID) {
			t.Fatalf("actor columns=%d key=%q operation=%q", actorColumnCount, persistedKey, operationText)
		}
		if _, err := database.ExecContext(ctx, `
			UPDATE f07_scheduled_posts
			SET created_by_account_id = $2
			WHERE created_by_account_id = $1
		`, actorID, "deleted:test"); err != nil {
			t.Fatal(err)
		}
		transaction, err := database.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = transaction.ExecContext(ctx, `DELETE FROM f07_publication_commands WHERE workspace_id = $1`, workspaceID); err == nil {
			_, err = transaction.ExecContext(ctx, `DELETE FROM f07_scheduled_posts WHERE workspace_id = $1`, workspaceID)
		}
		if err == nil {
			_, err = transaction.ExecContext(ctx, `DELETE FROM f04_workspaces WHERE id = $1`, workspaceID)
		}
		if err != nil {
			_ = transaction.Rollback()
			t.Fatal(err)
		}
		if err := transaction.Commit(); err != nil {
			t.Fatal(err)
		}
		var remaining int
		if err := database.QueryRowContext(ctx, `
			SELECT count(*) FROM f07_idempotency_operations WHERE workspace_id = $1
		`, workspaceID).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 0 {
			t.Fatalf("workspace idempotency rows after erasure=%d", remaining)
		}
	})

	t.Run("lease generation fences stale postgres owner", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		workspaceID := "workspace-f07-fencing-" + fmt.Sprint(now.UnixNano())
		cleanupWorkspace(t, workspaceID)
		candidate := IdempotencyOperation{
			WorkspaceID: workspaceID, Kind: OperationDuplicate,
			IdempotencyKey:       idempotencyKeyDigest("duplicate-fencing-1"),
			PayloadFingerprint:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			PostID:               "post_fencing_" + fmt.Sprint(now.UnixNano()),
			PublicationCommandID: "pubcmd_fencing_" + fmt.Sprint(now.UnixNano()),
		}
		stale, err := base.ReserveOperation(ctx, candidate, now)
		if err != nil {
			t.Fatal(err)
		}
		current, err := base.ReserveOperation(ctx, candidate, now.Add(operationLease))
		if err != nil {
			t.Fatal(err)
		}
		source := ScheduledPost{
			ID: "post_source_" + fmt.Sprint(now.UnixNano()), WorkspaceID: workspaceID,
			DraftID: "draft-source", DraftRevision: 1,
			ChannelIDs: []string{"channel-1"}, Status: StatusScheduled,
			ScheduledForUTC: now.Add(time.Hour),
			ScheduledLocal:  now.Add(time.Hour).Format(localDateTimeLayout),
			TimeZone:        "UTC", Revision: 1,
			ActiveCommandID: "pubcmd_source_" + fmt.Sprint(now.UnixNano()),
			CreatedBy:       "account-1", CreatedAt: now, UpdatedAt: now,
		}
		if _, err := base.Create(ctx, source, commandFor(source, source.ActiveCommandID, now)); err != nil {
			t.Fatal(err)
		}
		schedule := resolvedSchedule{
			utc: now.Add(time.Hour), local: now.Add(time.Hour).Format(localDateTimeLayout),
			timeZone: "UTC",
		}
		if _, err := base.PrepareDuplicateOperation(ctx, stale, source, schedule, now); !errors.Is(err, ErrOperationInProgress) {
			t.Fatalf("stale owner error=%v", err)
		}
		if _, err := base.PrepareDuplicateOperation(ctx, current, source, schedule, now); err != nil {
			t.Fatalf("current owner error=%v", err)
		}
	})
}

func sameScheduledPostResponse(t *testing.T, left, right ScheduledPost) bool {
	t.Helper()
	leftJSON, err := json.Marshal(scheduledPostView(left))
	if err != nil {
		t.Fatal(err)
	}
	rightJSON, err := json.Marshal(scheduledPostView(right))
	if err != nil {
		t.Fatal(err)
	}
	return string(leftJSON) == string(rightJSON)
}
