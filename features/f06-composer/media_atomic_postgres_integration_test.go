package composer

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPostgresDraftMediaAtomicityIntegration(t *testing.T) {
	databaseURL := os.Getenv("F06_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F06_DATABASE_URL is not configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	t.Run("retain failure leaves no draft mutation", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "retain-failure")
		objects, media, repository, service := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			1,
		)
		inspected, objectKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"retain-failure.png",
		)
		objects.retainErr = errors.New("injected retain failure")
		_, err := service.CreateDraft(context.Background(), CreateDraftCommand{
			WorkspaceID: workspaceID,
			ActorID:     "account-atomic",
			Content:     DraftContent{Media: []Media{{ID: inspected.ID}}},
		})
		if err == nil {
			t.Fatal("create succeeded after injected retain failure")
		}
		assertDraftCount(t, database, workspaceID, 0)
		assertAtomicMediaLifecycle(
			t,
			database,
			objects,
			workspaceID,
			inspected.ID,
			objectKey,
			"",
			"temporary",
			true,
		)
		if repository == nil {
			t.Fatal("atomic repository is nil")
		}
	})

	t.Run("database failure after retain compensates object", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "database-failure")
		objects, media, _, service := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			2,
		)
		inspected, objectKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"database-failure.png",
		)
		for _, statement := range []string{
			`CREATE OR REPLACE FUNCTION f06_test_fail_atomic_revision()
			 RETURNS trigger LANGUAGE plpgsql AS $$
			 BEGIN
			   IF NEW.workspace_id LIKE 'workspace-atomic-database-failure-%' THEN
			     RAISE EXCEPTION 'injected revision failure';
			   END IF;
			   RETURN NEW;
			 END
			 $$`,
			`DROP TRIGGER IF EXISTS f06_test_fail_atomic_revision
			 ON f06_composer_draft_revisions`,
			`CREATE TRIGGER f06_test_fail_atomic_revision
			 BEFORE INSERT ON f06_composer_draft_revisions
			 FOR EACH ROW EXECUTE FUNCTION f06_test_fail_atomic_revision()`,
		} {
			if _, err := database.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			_, _ = database.Exec(
				`DROP TRIGGER IF EXISTS f06_test_fail_atomic_revision
				 ON f06_composer_draft_revisions`,
			)
			_, _ = database.Exec(
				`DROP FUNCTION IF EXISTS f06_test_fail_atomic_revision()`,
			)
		})
		_, err := service.CreateDraft(context.Background(), CreateDraftCommand{
			WorkspaceID: workspaceID,
			ActorID:     "account-atomic",
			Content:     DraftContent{Media: []Media{{ID: inspected.ID}}},
		})
		if err == nil {
			t.Fatal("draft create succeeded after injected revision failure")
		}
		assertDraftCount(t, database, workspaceID, 0)
		assertAtomicMediaLifecycle(
			t,
			database,
			objects,
			workspaceID,
			inspected.ID,
			objectKey,
			"",
			"temporary",
			true,
		)
	})

	t.Run("replacement removal and autosave replay", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "replace-remove")
		objects, media, _, service := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			3,
		)
		first, firstKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"first.png",
		)
		second, secondKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"second.png",
		)
		created, err := service.CreateDraft(
			context.Background(),
			CreateDraftCommand{
				WorkspaceID: workspaceID,
				ActorID:     "account-atomic",
				Content:     DraftContent{Media: []Media{{ID: first.ID}}},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		assertAtomicMediaLifecycle(
			t,
			database,
			objects,
			workspaceID,
			first.ID,
			firstKey,
			created.Draft.ID,
			"retained",
			false,
		)
		replace := UpdateDraftCommand{
			WorkspaceID:      workspaceID,
			ActorID:          "account-atomic",
			DraftID:          created.Draft.ID,
			ExpectedRevision: 1,
			AutosaveKey:      "replace-autosave",
			Content:          DraftContent{Media: []Media{{ID: second.ID}}},
		}
		replaced, err := service.UpdateDraft(context.Background(), replace)
		if err != nil {
			t.Fatal(err)
		}
		if replaced.Draft.Revision != 2 ||
			len(replaced.Draft.Content.Media) != 1 ||
			replaced.Draft.Content.Media[0].ID != second.ID {
			t.Fatalf("replaced draft = %#v", replaced.Draft)
		}
		assertAtomicMediaLifecycle(
			t,
			database,
			objects,
			workspaceID,
			first.ID,
			firstKey,
			"",
			"temporary",
			true,
		)
		assertAtomicMediaLifecycle(
			t,
			database,
			objects,
			workspaceID,
			second.ID,
			secondKey,
			created.Draft.ID,
			"retained",
			false,
		)
		retainCalls, temporaryCalls := objectLifecycleCallCounts(objects)
		replayed, err := service.UpdateDraft(context.Background(), replace)
		if err != nil {
			t.Fatal(err)
		}
		if replayed.Draft.Revision != 2 ||
			replayed.Draft.Content.Media[0].ID != second.ID {
			t.Fatalf("autosave replay = %#v", replayed.Draft)
		}
		afterRetain, afterTemporary := objectLifecycleCallCounts(objects)
		if afterRetain != retainCalls || afterTemporary != temporaryCalls {
			t.Fatalf(
				"autosave replay repeated lifecycle side effects: retain %d->%d temporary %d->%d",
				retainCalls,
				afterRetain,
				temporaryCalls,
				afterTemporary,
			)
		}
		var revisionCount int
		if err := database.QueryRow(`
			SELECT count(*)
			  FROM f06_composer_draft_revisions
			 WHERE draft_id = $1`,
			created.Draft.ID,
		).Scan(&revisionCount); err != nil {
			t.Fatal(err)
		}
		if revisionCount != 2 {
			t.Fatalf("autosave replay revision count = %d, want 2", revisionCount)
		}
		removed, err := service.UpdateDraft(
			context.Background(),
			UpdateDraftCommand{
				WorkspaceID:      workspaceID,
				ActorID:          "account-atomic",
				DraftID:          created.Draft.ID,
				ExpectedRevision: 2,
				AutosaveKey:      "remove-autosave",
				Content:          DraftContent{},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if removed.Draft.Revision != 3 || len(removed.Draft.Content.Media) != 0 {
			t.Fatalf("removed draft = %#v", removed.Draft)
		}
		assertAtomicMediaLifecycle(
			t,
			database,
			objects,
			workspaceID,
			second.ID,
			secondKey,
			"",
			"temporary",
			true,
		)
	})

	t.Run("temporary lifecycle failure is safe and retryable", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "temporary-retry")
		objects, media, repository, service := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			4,
		)
		inspected, objectKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"temporary-retry.png",
		)
		created, err := service.CreateDraft(
			context.Background(),
			CreateDraftCommand{
				WorkspaceID: workspaceID,
				ActorID:     "account-atomic",
				Content:     DraftContent{Media: []Media{{ID: inspected.ID}}},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		objects.temporaryErr = errors.New("injected temporary lifecycle failure")
		removed, err := service.UpdateDraft(
			context.Background(),
			UpdateDraftCommand{
				WorkspaceID:      workspaceID,
				ActorID:          "account-atomic",
				DraftID:          created.Draft.ID,
				ExpectedRevision: 1,
				AutosaveKey:      "temporary-retry",
				Content:          DraftContent{},
			},
		)
		if err != nil {
			t.Fatalf("safe removal returned an error after commit: %v", err)
		}
		if len(removed.Draft.Content.Media) != 0 {
			t.Fatalf("removed draft still references media: %#v", removed.Draft.Content.Media)
		}
		var attached sql.NullString
		var expires sql.NullTime
		var lifecycle string
		var pending bool
		if err := database.QueryRow(`
			SELECT attached_draft_id, expires_at,
			       lifecycle_state, lifecycle_sync_pending
			  FROM f06_composer_media
			 WHERE workspace_id = $1 AND id = $2`,
			workspaceID,
			inspected.ID,
		).Scan(&attached, &expires, &lifecycle, &pending); err != nil {
			t.Fatal(err)
		}
		if attached.Valid || !expires.Valid ||
			lifecycle != "temporary" || !pending {
			t.Fatalf(
				"failed temporary sync metadata = attached=%#v expires=%#v lifecycle=%q pending=%v",
				attached,
				expires,
				lifecycle,
				pending,
			)
		}
		objects.mutex.Lock()
		if !objects.objects[objectKey].retained {
			t.Fatal("failed temporary tag did not leave the object safely retained")
		}
		objects.temporaryErr = nil
		objects.mutex.Unlock()
		if err := media.ReconcileLifecycle(context.Background(), workspaceID); err != nil {
			t.Fatal(err)
		}
		assertAtomicMediaLifecycle(
			t,
			database,
			objects,
			workspaceID,
			inspected.ID,
			objectKey,
			"",
			"temporary",
			true,
		)
		stored, err := repository.Get(
			context.Background(),
			workspaceID,
			created.Draft.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(stored.Content.Media) != 0 {
			t.Fatalf("retry changed committed draft: %#v", stored.Content.Media)
		}
	})

	t.Run("delete reservation wins against concurrent attach", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "delete-race")
		objects, media, _, service := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			5,
		)
		inspected, objectKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"delete-race.png",
		)
		deleteStarted := make(chan string, 1)
		deleteRelease := make(chan struct{})
		objects.deleteStart = deleteStarted
		objects.deleteWait = deleteRelease
		errCh := make(chan error, 1)
		go func() {
			errCh <- media.Delete(context.Background(), workspaceID, inspected.ID)
		}()
		select {
		case key := <-deleteStarted:
			if key != objectKey {
				t.Fatalf("delete started for %q, want %q", key, objectKey)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for reserved delete")
		}
		var deletingAt sql.NullTime
		if err := database.QueryRow(`
			SELECT deleting_at
			  FROM f06_composer_media
			 WHERE workspace_id = $1 AND id = $2`,
			workspaceID,
			inspected.ID,
		).Scan(&deletingAt); err != nil {
			t.Fatal(err)
		}
		if !deletingAt.Valid {
			t.Fatal("delete did not reserve metadata before object deletion")
		}
		objects.mutex.Lock()
		_, exists := objects.objects[objectKey]
		objects.mutex.Unlock()
		if !exists {
			t.Fatal("object was deleted before metadata reservation became visible")
		}
		_, err := service.CreateDraft(context.Background(), CreateDraftCommand{
			WorkspaceID: workspaceID,
			ActorID:     "account-atomic",
			Content:     DraftContent{Media: []Media{{ID: inspected.ID}}},
		})
		var fieldError *FieldRuleError
		if !errors.As(err, &fieldError) || fieldError.Code != "media_not_ready" {
			t.Fatalf("attach during delete error = %#v", err)
		}
		close(deleteRelease)
		if err := <-errCh; err != nil {
			t.Fatalf("reserved delete failed: %v", err)
		}
		assertDraftCount(t, database, workspaceID, 0)
		var remaining int
		if err := database.QueryRow(`
			SELECT count(*)
			  FROM f06_composer_media
			 WHERE workspace_id = $1 AND id = $2`,
			workspaceID,
			inspected.ID,
		).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 0 {
			t.Fatalf("deleted media row count = %d, want 0", remaining)
		}
	})

	t.Run("expired delete reservation recovers after crash before object delete", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "delete-crash-before")
		objects, media, _, _ := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			6,
		)
		inspected, objectKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"delete-crash-before.png",
		)
		expireDeleteReservation(
			t,
			database,
			workspaceID,
			inspected.ID,
			now.Add(-mediaDeleteLease-time.Second),
		)
		if err := media.Delete(context.Background(), workspaceID, inspected.ID); err != nil {
			t.Fatalf("delete recovery after crash before object delete = %v", err)
		}
		assertDeletedAtomicMedia(t, database, objects, workspaceID, inspected.ID, objectKey)
	})

	t.Run("expired delete reservation recovers after crash after object delete", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "delete-crash-after")
		objects, media, _, _ := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			7,
		)
		inspected, objectKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"delete-crash-after.png",
		)
		expireDeleteReservation(
			t,
			database,
			workspaceID,
			inspected.ID,
			now.Add(-mediaDeleteLease-time.Second),
		)
		objects.mutex.Lock()
		delete(objects.objects, objectKey)
		objects.mutex.Unlock()
		if err := media.Delete(context.Background(), workspaceID, inspected.ID); err != nil {
			t.Fatalf("delete recovery after crash after object delete = %v", err)
		}
		assertDeletedAtomicMedia(t, database, objects, workspaceID, inspected.ID, objectKey)
	})

	t.Run("expired delete reservation lease stays single-writer under concurrency", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "delete-recover-race")
		objects, media, _, _ := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			8,
		)
		inspected, objectKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"delete-recover-race.png",
		)
		expireDeleteReservation(
			t,
			database,
			workspaceID,
			inspected.ID,
			now.Add(-mediaDeleteLease-time.Second),
		)
		deleteStarted := make(chan string, 1)
		deleteRelease := make(chan struct{})
		objects.deleteStart = deleteStarted
		objects.deleteWait = deleteRelease
		firstErrCh := make(chan error, 1)
		secondErrCh := make(chan error, 1)
		go func() {
			firstErrCh <- media.Delete(context.Background(), workspaceID, inspected.ID)
		}()
		select {
		case key := <-deleteStarted:
			if key != objectKey {
				t.Fatalf("concurrent recovery delete key = %q, want %q", key, objectKey)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for recovered delete to start")
		}
		go func() {
			secondErrCh <- media.Delete(context.Background(), workspaceID, inspected.ID)
		}()
		secondErr := <-secondErrCh
		if !errors.Is(secondErr, ErrConflict) {
			t.Fatalf("second concurrent recovery delete = %v, want conflict", secondErr)
		}
		close(deleteRelease)
		if err := <-firstErrCh; err != nil {
			t.Fatalf("first concurrent recovery delete = %v", err)
		}
		assertDeletedAtomicMedia(t, database, objects, workspaceID, inspected.ID, objectKey)
	})

	t.Run("ambiguous delete error keeps tombstone closed until periodic recovery", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "delete-ambiguous-periodic")
		objects, media, _, service := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			9,
		)
		inspected, objectKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"delete-ambiguous-periodic.png",
		)
		objects.deleteErr = errors.New("injected ambiguous delete failure")
		objects.deleteApply = true
		if err := media.Delete(context.Background(), workspaceID, inspected.ID); err == nil {
			t.Fatal("delete succeeded after injected ambiguous object error")
		}
		assertDeleteReservationState(
			t,
			database,
			workspaceID,
			inspected.ID,
			true,
		)
		assertObjectExists(t, objects, objectKey, false)
		if _, err := media.Get(
			context.Background(),
			workspaceID,
			inspected.ID,
		); !errors.Is(err, ErrNotFound) {
			t.Fatalf("get after ambiguous delete = %v, want not found", err)
		}
		if _, err := media.Download(
			context.Background(),
			workspaceID,
			inspected.ID,
		); !errors.Is(err, ErrNotFound) {
			t.Fatalf("download after ambiguous delete = %v, want not found", err)
		}
		_, err := service.CreateDraft(context.Background(), CreateDraftCommand{
			WorkspaceID: workspaceID,
			ActorID:     "account-atomic",
			Content:     DraftContent{Media: []Media{{ID: inspected.ID}}},
		})
		var fieldError *FieldRuleError
		if !errors.As(err, &fieldError) || fieldError.Code != "media_not_ready" {
			t.Fatalf("attach after ambiguous delete = %#v", err)
		}
		expireDeleteReservation(
			t,
			database,
			workspaceID,
			inspected.ID,
			now.Add(-mediaDeleteLease-time.Second),
		)
		if err := media.ReconcileLifecycle(context.Background(), workspaceID); err != nil {
			t.Fatalf("periodic delete recovery after ambiguous error = %v", err)
		}
		assertDeletedAtomicMedia(t, database, objects, workspaceID, inspected.ID, objectKey)
	})

	t.Run("ambiguous delete error finalizes on retry after lease expiry", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "delete-ambiguous-retry")
		objects, media, _, _ := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			10,
		)
		inspected, objectKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"delete-ambiguous-retry.png",
		)
		objects.deleteErr = errors.New("injected ambiguous delete failure")
		objects.deleteApply = true
		if err := media.Delete(context.Background(), workspaceID, inspected.ID); err == nil {
			t.Fatal("delete succeeded after injected ambiguous object error")
		}
		expireDeleteReservation(
			t,
			database,
			workspaceID,
			inspected.ID,
			now.Add(-mediaDeleteLease-time.Second),
		)
		if err := media.Delete(context.Background(), workspaceID, inspected.ID); err != nil {
			t.Fatalf("retry delete after ambiguous object error = %v", err)
		}
		assertDeletedAtomicMedia(t, database, objects, workspaceID, inspected.ID, objectKey)
	})

	t.Run("non-applied delete error retries object deletion after lease expiry", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "delete-not-applied")
		objects, media, _, _ := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			11,
		)
		inspected, objectKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"delete-not-applied.png",
		)
		objects.deleteErr = errors.New("injected delete failure")
		if err := media.Delete(context.Background(), workspaceID, inspected.ID); err == nil {
			t.Fatal("delete succeeded after injected object failure")
		}
		assertDeleteReservationState(
			t,
			database,
			workspaceID,
			inspected.ID,
			true,
		)
		assertObjectExists(t, objects, objectKey, true)
		expireDeleteReservation(
			t,
			database,
			workspaceID,
			inspected.ID,
			now.Add(-mediaDeleteLease-time.Second),
		)
		objects.deleteErr = nil
		if err := media.ReconcileLifecycle(context.Background(), workspaceID); err != nil {
			t.Fatalf("periodic retry after non-applied delete error = %v", err)
		}
		assertDeletedAtomicMedia(t, database, objects, workspaceID, inspected.ID, objectKey)
	})

	t.Run("duplicate draft clones independent media lifecycle", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "duplicate-independent")
		objects, media, _, service := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			12,
		)
		inspected, sourceObjectKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"duplicate-independent.png",
		)
		created, err := service.CreateDraft(context.Background(), CreateDraftCommand{
			WorkspaceID: workspaceID,
			ActorID:     "account-atomic",
			Content: DraftContent{
				Media:  []Media{{ID: inspected.ID}},
				Thread: []ThreadItem{{Text: "thread", MediaIDs: []string{inspected.ID}}},
				Destinations: []Destination{{
					ID:           "image",
					ChannelID:    "channel-1",
					ChannelType:  "fixture_image_channel",
					CapabilityID: "fixture:image",
					Format:       FormatImage,
					MediaIDs:     &[]string{inspected.ID},
					ThreadOverride: &[]ThreadItem{{
						Text:     "override",
						MediaIDs: []string{inspected.ID},
					}},
				}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		boundary, err := service.SchedulingBoundary()
		if err != nil {
			t.Fatal(err)
		}
		duplicated, err := boundary.DuplicateDraft(context.Background(), DuplicateDraftCommand{
			WorkspaceID:    workspaceID,
			ActorID:        "account-atomic",
			SourceDraftID:  created.Draft.ID,
			SourceRevision: 1,
			IdempotencyKey: "atomic-clone-1",
		})
		if err != nil {
			t.Fatal(err)
		}
		cloned, err := service.GetDraft(
			context.Background(),
			workspaceID,
			"account-atomic",
			duplicated.DraftID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(cloned.Draft.Content.Media) != 1 {
			t.Fatalf("cloned draft media = %#v", cloned.Draft.Content.Media)
		}
		if len(cloned.Draft.Content.Thread) != 1 ||
			len(cloned.Draft.Content.Destinations) != 1 {
			t.Fatalf("cloned draft nested content = %#v", cloned.Draft.Content)
		}
		if cloned.Draft.Content.Media[0].ID == inspected.ID ||
			cloned.Draft.Content.Media[0].URL ==
				mediaBasePath(workspaceID, inspected.ID)+"/download" {
			t.Fatalf("clone reused source metadata: %#v", cloned.Draft.Content.Media[0])
		}
		if cloned.Draft.Content.Thread[0].MediaIDs[0] != cloned.Draft.Content.Media[0].ID {
			t.Fatalf("thread media ids not remapped: %#v", cloned.Draft.Content.Thread)
		}
		if cloned.Draft.Content.Destinations[0].MediaIDs == nil ||
			(*cloned.Draft.Content.Destinations[0].MediaIDs)[0] != cloned.Draft.Content.Media[0].ID {
			t.Fatalf("destination media ids not remapped: %#v", cloned.Draft.Content.Destinations[0])
		}
		if cloned.Draft.Content.Destinations[0].ThreadOverride == nil ||
			(*cloned.Draft.Content.Destinations[0].ThreadOverride)[0].MediaIDs[0] !=
				cloned.Draft.Content.Media[0].ID {
			t.Fatalf("thread override media ids not remapped: %#v", cloned.Draft.Content.Destinations[0])
		}
		var cloneObjectKey, cloneLifecycle string
		var cloneAttached string
		var cloneMetadataID, cloneMetadataURL string
		if err := database.QueryRowContext(context.Background(), `
			SELECT object_key, attached_draft_id, lifecycle_state,
			       inspected_metadata->>'id', inspected_metadata->>'url'
			  FROM f06_composer_media
			 WHERE workspace_id = $1
			   AND id = $2`,
			workspaceID,
			cloned.Draft.Content.Media[0].ID,
		).Scan(
			&cloneObjectKey,
			&cloneAttached,
			&cloneLifecycle,
			&cloneMetadataID,
			&cloneMetadataURL,
		); err != nil {
			t.Fatal(err)
		}
		if cloneObjectKey == sourceObjectKey || cloneAttached != duplicated.DraftID ||
			cloneLifecycle != "retained" ||
			cloneMetadataID != cloned.Draft.Content.Media[0].ID ||
			cloneMetadataURL != cloned.Draft.Content.Media[0].URL {
			t.Fatalf(
				"clone media lifecycle = key %q attached %q state %q metadata_id %q metadata_url %q",
				cloneObjectKey,
				cloneAttached,
				cloneLifecycle,
				cloneMetadataID,
				cloneMetadataURL,
			)
		}
		if err := service.DeleteDraft(
			context.Background(),
			workspaceID,
			"account-atomic",
			created.Draft.ID,
			1,
		); err != nil {
			t.Fatal(err)
		}
		assertAtomicMediaLifecycle(
			t,
			database,
			objects,
			workspaceID,
			cloned.Draft.Content.Media[0].ID,
			cloneObjectKey,
			duplicated.DraftID,
			"retained",
			false,
		)
	})

	t.Run("validate scheduling fails when live object is missing", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "preflight-missing")
		objects, media, _, service := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			13,
		)
		inspected, objectKey := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"preflight-missing.png",
		)
		created, err := service.CreateDraft(context.Background(), CreateDraftCommand{
			WorkspaceID: workspaceID,
			ActorID:     "account-atomic",
			Content: DraftContent{
				Media: []Media{{ID: inspected.ID}},
				Destinations: []Destination{{
					ID:           "image",
					ChannelID:    "channel-1",
					ChannelType:  "fixture_image_channel",
					CapabilityID: "fixture:image",
					Format:       FormatImage,
				}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		objects.mutex.Lock()
		delete(objects.objects, objectKey)
		objects.mutex.Unlock()
		boundary, err := service.SchedulingBoundary()
		if err != nil {
			t.Fatal(err)
		}
		_, err = boundary.ValidateForScheduling(context.Background(), SchedulingValidationCommand{
			WorkspaceID: workspaceID,
			ActorID:     "account-atomic",
			DraftID:     created.Draft.ID,
			ChannelIDs:  []string{"channel-1"},
		})
		var failure *ValidationFailure
		if !errors.As(err, &failure) {
			t.Fatalf("missing object validation error = %#v", err)
		}
		if len(failure.Report.Errors) == 0 || failure.Report.Errors[0].Code != "media_not_ready" {
			t.Fatalf("missing object validation report = %#v", failure.Report)
		}
	})

	t.Run("validate scheduling maps live object outage to dependency unavailable", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "preflight-outage")
		objects, media, _, service := atomicMediaTestService(
			t,
			database,
			now,
			workspaceID,
			14,
		)
		inspected, _ := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"preflight-outage.png",
		)
		created, err := service.CreateDraft(context.Background(), CreateDraftCommand{
			WorkspaceID: workspaceID,
			ActorID:     "account-atomic",
			Content: DraftContent{
				Media: []Media{{ID: inspected.ID}},
				Destinations: []Destination{{
					ID:           "image",
					ChannelID:    "channel-1",
					ChannelType:  "fixture_image_channel",
					CapabilityID: "fixture:image",
					Format:       FormatImage,
				}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		objects.openErr = errors.New("object store unavailable")
		boundary, err := service.SchedulingBoundary()
		if err != nil {
			t.Fatal(err)
		}
		_, err = boundary.ValidateForScheduling(context.Background(), SchedulingValidationCommand{
			WorkspaceID: workspaceID,
			ActorID:     "account-atomic",
			DraftID:     created.Draft.ID,
			ChannelIDs:  []string{"channel-1"},
		})
		if !errors.Is(err, ErrDependencyUnavailable) {
			t.Fatalf("live object outage error = %#v", err)
		}
	})

	t.Run("stale owner cleans local cloned media without deleting canonical draft", func(t *testing.T) {
		workspaceID := atomicMediaWorkspaceID(t, "stale-owner-cleanup")
		nowValue := now
		var nowMutex sync.Mutex
		currentTime := func() time.Time {
			nowMutex.Lock()
			defer nowMutex.Unlock()
			return nowValue
		}
		setTime := func(next time.Time) {
			nowMutex.Lock()
			nowValue = next
			nowMutex.Unlock()
		}
		objects := newFakeObjectStore()
		media, err := NewPostgresMediaStore(
			database,
			objects,
			StreamMediaInspector{},
			currentTime,
			defaultMaximumUploadBytes,
		)
		if err != nil {
			t.Fatal(err)
		}
		baseRepository, err := NewPostgresRepository(database)
		if err != nil {
			t.Fatal(err)
		}
		baseRepository.BindMediaStore(media)
		wrapper := &blockingCreateRepository{
			base:    baseRepository,
			started: make(chan struct{}),
			release: make(chan struct{}),
		}
		service, err := NewService(
			wrapper,
			authorizerStub{allowed: true},
			WithCapabilityCatalog(fixtureCatalog(t)),
			WithDestinationResolver(schedulingDestinationResolverStub{
				resolved: map[string]ResolvedDestination{
					"channel-1": {
						ChannelType:  "fixture_image_channel",
						CapabilityID: "fixture:image",
						Format:       FormatImage,
					},
				},
			}),
			WithClock(currentTime),
			WithRandom(func() func([]byte) error {
				var sequence uint64
				return func(destination []byte) error {
					sequence++
					for index := range destination {
						destination[index] = 15
					}
					binary.BigEndian.PutUint64(
						destination[len(destination)-8:],
						uint64(time.Now().UTC().UnixNano())+sequence,
					)
					return nil
				}
			}()),
			WithMediaResolver(media),
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = database.Exec(
				`DELETE FROM f06_composer_duplicate_operations WHERE workspace_id = $1`,
				workspaceID,
			)
			_, _ = database.Exec(
				`DELETE FROM f06_composer_media WHERE workspace_id = $1`,
				workspaceID,
			)
			_, _ = database.Exec(
				`DELETE FROM f06_composer_drafts WHERE workspace_id = $1`,
				workspaceID,
			)
		})
		inspected, _ := createReadyAtomicMedia(
			t,
			media,
			objects,
			workspaceID,
			"stale-owner-cleanup.png",
		)
		created, err := service.CreateDraft(context.Background(), CreateDraftCommand{
			WorkspaceID: workspaceID,
			ActorID:     "account-atomic",
			Content: DraftContent{
				Media: []Media{{ID: inspected.ID}},
				Destinations: []Destination{{
					ID:           "image",
					ChannelID:    "channel-1",
					ChannelType:  "fixture_image_channel",
					CapabilityID: "fixture:image",
					Format:       FormatImage,
				}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		boundary, err := service.SchedulingBoundary()
		if err != nil {
			t.Fatal(err)
		}
		results := make(chan DuplicatedDraft, 1)
		errorsCh := make(chan error, 2)
		go func() {
			result, err := boundary.DuplicateDraft(context.Background(), DuplicateDraftCommand{
				WorkspaceID:    workspaceID,
				ActorID:        "account-atomic",
				SourceDraftID:  created.Draft.ID,
				SourceRevision: 1,
				IdempotencyKey: "test-idempotency-stale-owner",
			})
			if err != nil {
				errorsCh <- err
				return
			}
			results <- result
		}()
		<-wrapper.started
		setTime(now.Add(duplicateOperationLease + time.Second))
		second, err := boundary.DuplicateDraft(context.Background(), DuplicateDraftCommand{
			WorkspaceID:    workspaceID,
			ActorID:        "account-atomic",
			SourceDraftID:  created.Draft.ID,
			SourceRevision: 1,
			IdempotencyKey: " test-idempotency-stale-owner ",
		})
		if err != nil {
			t.Fatal(err)
		}
		close(wrapper.release)
		select {
		case err := <-errorsCh:
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("stale owner error = %v", err)
			}
		case replay := <-results:
			if !replay.Replayed || replay.DraftID != second.DraftID {
				t.Fatalf("stale owner replay = %#v, canonical = %#v", replay, second)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for stale owner result")
		}
		cloned, err := service.GetDraft(
			context.Background(),
			workspaceID,
			"account-atomic",
			second.DraftID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(cloned.Draft.Content.Media) != 1 {
			t.Fatalf("canonical cloned draft = %#v", cloned.Draft.Content)
		}
		var unattachedCount int
		if err := database.QueryRowContext(context.Background(), `
			SELECT count(*)
			  FROM f06_composer_media
			 WHERE workspace_id = $1
			   AND attached_draft_id IS NULL
			   AND id <> $2`,
			workspaceID,
			inspected.ID,
		).Scan(&unattachedCount); err != nil {
			t.Fatal(err)
		}
		if unattachedCount != 0 {
			t.Fatalf("orphan cloned media rows = %d", unattachedCount)
		}
		objects.mutex.Lock()
		objectCount := len(objects.objects)
		objects.mutex.Unlock()
		if objectCount != 2 {
			t.Fatalf("unexpected object count = %d, want 2", objectCount)
		}
	})
}

type blockingCreateRepository struct {
	base    *PostgresRepository
	started chan struct{}
	release chan struct{}
	mutex   sync.Mutex
	blocked bool
}

func (repository *blockingCreateRepository) Create(
	ctx context.Context,
	draft Draft,
) (Draft, error) {
	if len(draft.ID) >= len("draft_dup_") && draft.ID[:len("draft_dup_")] == "draft_dup_" {
		repository.mutex.Lock()
		shouldBlock := !repository.blocked
		if shouldBlock {
			repository.blocked = true
		}
		repository.mutex.Unlock()
		if shouldBlock {
			close(repository.started)
			<-repository.release
		}
	}
	return repository.base.Create(ctx, draft)
}

func (repository *blockingCreateRepository) Get(
	ctx context.Context,
	workspaceID, draftID string,
) (Draft, error) {
	return repository.base.Get(ctx, workspaceID, draftID)
}

func (repository *blockingCreateRepository) List(
	ctx context.Context,
	workspaceID string,
) ([]Draft, error) {
	return repository.base.List(ctx, workspaceID)
}

func (repository *blockingCreateRepository) Update(
	ctx context.Context,
	draft Draft,
	expectedRevision int64,
	autosaveKey string,
) (Draft, error) {
	return repository.base.Update(ctx, draft, expectedRevision, autosaveKey)
}

func (repository *blockingCreateRepository) Delete(
	ctx context.Context,
	workspaceID, draftID string,
	expectedRevision int64,
) error {
	return repository.base.Delete(ctx, workspaceID, draftID, expectedRevision)
}

func (repository *blockingCreateRepository) ListRevisions(
	ctx context.Context,
	workspaceID, draftID string,
) ([]DraftRevision, error) {
	return repository.base.ListRevisions(ctx, workspaceID, draftID)
}

func (repository *blockingCreateRepository) GetRevision(
	ctx context.Context,
	workspaceID, draftID string,
	revision int64,
) (DraftRevision, error) {
	return repository.base.GetRevision(ctx, workspaceID, draftID, revision)
}

func (repository *blockingCreateRepository) ReserveDuplicateOperation(
	ctx context.Context,
	operation duplicateOperation,
	now time.Time,
) (duplicateOperation, bool, error) {
	return repository.base.ReserveDuplicateOperation(ctx, operation, now)
}

func (repository *blockingCreateRepository) CompleteDuplicateOperation(
	ctx context.Context,
	operation duplicateOperation,
	cloneDraftID string,
	cloneDraftRevision int64,
	completedAt time.Time,
) error {
	return repository.base.CompleteDuplicateOperation(
		ctx,
		operation,
		cloneDraftID,
		cloneDraftRevision,
		completedAt,
	)
}

func (repository *blockingCreateRepository) AbandonDuplicateOperation(
	ctx context.Context,
	operation duplicateOperation,
) (bool, error) {
	return repository.base.AbandonDuplicateOperation(ctx, operation)
}

func (repository *blockingCreateRepository) ResetDanglingCompletedDuplicateOperation(
	ctx context.Context,
	operation duplicateOperation,
	now time.Time,
) (duplicateOperation, bool, error) {
	return repository.base.ResetDanglingCompletedDuplicateOperation(ctx, operation, now)
}

func atomicMediaTestService(
	t *testing.T,
	database *sql.DB,
	now time.Time,
	workspaceID string,
	randomByte byte,
) (*fakeObjectStore, *PostgresMediaStore, *PostgresRepository, *Service) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = database.Exec(
			`DELETE FROM f06_composer_duplicate_operations WHERE workspace_id = $1`,
			workspaceID,
		)
		_, _ = database.Exec(
			`DELETE FROM f06_composer_media WHERE workspace_id = $1`,
			workspaceID,
		)
		_, _ = database.Exec(
			`DELETE FROM f06_composer_drafts WHERE workspace_id = $1`,
			workspaceID,
		)
	})
	objects := newFakeObjectStore()
	media, err := NewPostgresMediaStore(
		database,
		objects,
		StreamMediaInspector{},
		func() time.Time { return now },
		defaultMaximumUploadBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		repository,
		authorizerStub{allowed: true},
		WithCapabilityCatalog(fixtureCatalog(t)),
		WithDestinationResolver(schedulingDestinationResolverStub{
			resolved: map[string]ResolvedDestination{
				"channel-1": {
					ChannelType:  "fixture_image_channel",
					CapabilityID: "fixture:image",
					Format:       FormatImage,
				},
			},
		}),
		WithClock(func() time.Time { return now }),
		WithRandom(func() func([]byte) error {
			var sequence uint64
			return func(destination []byte) error {
				sequence++
				for index := range destination {
					destination[index] = randomByte
				}
				binary.BigEndian.PutUint64(destination[len(destination)-8:], sequence)
				return nil
			}
		}()),
		WithMediaResolver(media),
	)
	if err != nil {
		t.Fatal(err)
	}
	return objects, media, repository, service
}

func createReadyAtomicMedia(
	t *testing.T,
	media *PostgresMediaStore,
	objects *fakeObjectStore,
	workspaceID, fileName string,
) (Media, string) {
	t.Helper()
	png := testPNG(t)
	upload, err := media.CreateUpload(
		context.Background(),
		workspaceID,
		"account-atomic",
		MediaUploadRequest{
			FileName:    fileName,
			ContentType: "image/png",
			SizeBytes:   int64(len(png)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	objects.mutex.Lock()
	objectKey := objects.uploadKey
	objects.mutex.Unlock()
	objects.putAuthorized(png, "image/png")
	inspected, err := media.CompleteUpload(
		context.Background(),
		workspaceID,
		upload.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return inspected, objectKey
}

func expireDeleteReservation(
	t *testing.T,
	database *sql.DB,
	workspaceID, mediaID string,
	reservedAt time.Time,
) {
	t.Helper()
	if _, err := database.Exec(`
		UPDATE f06_composer_media
		   SET deleting_at = $3
		 WHERE workspace_id = $1
		   AND id = $2`,
		workspaceID,
		mediaID,
		reservedAt.UTC(),
	); err != nil {
		t.Fatal(err)
	}
}

func assertDeletedAtomicMedia(
	t *testing.T,
	database *sql.DB,
	objects *fakeObjectStore,
	workspaceID, mediaID, objectKey string,
) {
	t.Helper()
	var remaining int
	if err := database.QueryRow(`
		SELECT count(*)
		  FROM f06_composer_media
		 WHERE workspace_id = $1 AND id = $2`,
		workspaceID,
		mediaID,
	).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("remaining media rows = %d, want 0", remaining)
	}
	objects.mutex.Lock()
	_, exists := objects.objects[objectKey]
	objects.mutex.Unlock()
	if exists {
		t.Fatalf("object %q still exists after delete recovery", objectKey)
	}
}

func assertDeleteReservationState(
	t *testing.T,
	database *sql.DB,
	workspaceID, mediaID string,
	expectReservation bool,
) {
	t.Helper()
	var deletingAt sql.NullTime
	if err := database.QueryRow(`
		SELECT deleting_at
		  FROM f06_composer_media
		 WHERE workspace_id = $1 AND id = $2`,
		workspaceID,
		mediaID,
	).Scan(&deletingAt); err != nil {
		t.Fatal(err)
	}
	if deletingAt.Valid != expectReservation {
		t.Fatalf(
			"delete reservation valid = %v, want %v",
			deletingAt.Valid,
			expectReservation,
		)
	}
}

func assertObjectExists(
	t *testing.T,
	objects *fakeObjectStore,
	objectKey string,
	expected bool,
) {
	t.Helper()
	objects.mutex.Lock()
	_, exists := objects.objects[objectKey]
	objects.mutex.Unlock()
	if exists != expected {
		t.Fatalf("object %q exists = %v, want %v", objectKey, exists, expected)
	}
}

func assertDraftCount(
	t *testing.T,
	database *sql.DB,
	workspaceID string,
	expected int,
) {
	t.Helper()
	var count int
	if err := database.QueryRow(
		`SELECT count(*) FROM f06_composer_drafts WHERE workspace_id = $1`,
		workspaceID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("draft count = %d, want %d", count, expected)
	}
}

func assertAtomicMediaLifecycle(
	t *testing.T,
	database *sql.DB,
	objects *fakeObjectStore,
	workspaceID, mediaID, objectKey, attachedDraft, lifecycle string,
	expectExpiry bool,
) {
	t.Helper()
	var attached sql.NullString
	var expires sql.NullTime
	var storedLifecycle string
	var syncPending bool
	if err := database.QueryRow(`
		SELECT attached_draft_id, expires_at, lifecycle_state, lifecycle_sync_pending
		  FROM f06_composer_media
		 WHERE workspace_id = $1 AND id = $2`,
		workspaceID,
		mediaID,
	).Scan(&attached, &expires, &storedLifecycle, &syncPending); err != nil {
		t.Fatal(err)
	}
	if attached.String != attachedDraft || attached.Valid != (attachedDraft != "") {
		t.Fatalf("attached draft = %#v, want %q", attached, attachedDraft)
	}
	if storedLifecycle != lifecycle || syncPending {
		t.Fatalf(
			"lifecycle = %q pending=%v, want %q pending=false",
			storedLifecycle,
			syncPending,
			lifecycle,
		)
	}
	if expires.Valid != expectExpiry {
		t.Fatalf("expiry valid = %v, want %v", expires.Valid, expectExpiry)
	}
	objects.mutex.Lock()
	object := objects.objects[objectKey]
	objects.mutex.Unlock()
	if object.retained != (lifecycle == "retained") {
		t.Fatalf(
			"object retained = %v, want lifecycle %q",
			object.retained,
			lifecycle,
		)
	}
}

func objectLifecycleCallCounts(objects *fakeObjectStore) (int, int) {
	objects.mutex.Lock()
	defer objects.mutex.Unlock()
	return len(objects.retainCalls), len(objects.tempCalls)
}

func atomicMediaWorkspaceID(t *testing.T, label string) string {
	t.Helper()
	return "workspace-atomic-" + label + "-" +
		time.Now().UTC().Format("20060102150405.000000000")
}
