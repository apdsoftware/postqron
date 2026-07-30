package scheduling

import (
	"context"
	"database/sql"
	"errors"
	"os"
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
		commands[0].State != CommandInvalidated ||
		commands[1].State != CommandPending ||
		commands[1].ID != replaced.ActiveCommandID {
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
