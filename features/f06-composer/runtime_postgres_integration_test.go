package composer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
)

func TestPostgresRuntimeIntegrationEnforcesWorkspaceRBACAndMediaInspection(
	t *testing.T,
) {
	databaseURL := os.Getenv("F06_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F06_DATABASE_URL is not configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	workspaceID, accountID := createRuntimeWorkspace(t, database, "rbac")
	insertConnectedRuntimeChannel(
		t,
		database,
		runtimeChannel{
			ID:          "channel-facebook-text-" + workspaceID,
			WorkspaceID: workspaceID,
			Provider:    "facebook_pages",
			Resource:    "facebook_page",
			AccountType: "page",
			DisplayName: "Facebook page",
			ActorID:     accountID,
		},
	)

	module := newRuntimeModule(t, database)
	if err := module.Configure(map[string]string{
		configCapabilitiesJSON: runtimeCapabilityCatalog(t),
		configAllowedOrigins:   "https://postqron.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := module.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler, ok := module.Handler(composerRuntimeHandlerName)
	if !ok {
		t.Fatal("runtime handler missing")
	}

	createPayload, err := json.Marshal(map[string]any{
		"content": DraftContent{
			Text: "ready",
			Destinations: []Destination{{
				ID:           "text",
				ChannelID:    "channel-facebook-text-" + workspaceID,
				ChannelType:  "ignored",
				CapabilityID: "ignored",
				Format:       FormatText,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	create := authenticatedRuntimeRequest(
		http.MethodPost,
		"/api/v1/workspaces/"+workspaceID+"/drafts",
		string(createPayload),
		accountID,
	)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", createResponse.Code, createResponse.Body.String())
	}
	var created DraftView
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Validation.Valid || created.Draft.Revision != 1 {
		t.Fatalf("created draft = %#v", created)
	}
	destination := created.Draft.Content.Destinations[0]
	if destination.ChannelType != "facebook_page_text" ||
		destination.CapabilityID != "runtime:facebook:text" {
		t.Fatalf("destination was not canonicalized: %#v", destination)
	}

	forbidden := authenticatedRuntimeRequest(
		http.MethodGet,
		"/api/v1/workspaces/"+workspaceID+"/drafts",
		"",
		"not-a-member",
	)
	forbiddenResponse := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenResponse, forbidden)
	if forbiddenResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"non-member status = %d body=%s",
			forbiddenResponse.Code,
			forbiddenResponse.Body.String(),
		)
	}

	png := testPNG(t)
	uploadDeclaration, err := json.Marshal(MediaUploadRequest{
		FileName: "pixel.png", ContentType: "image/png", SizeBytes: int64(len(png)),
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizeUpload := authenticatedRuntimeRequest(
		http.MethodPost,
		"/api/v1/workspaces/"+workspaceID+"/composer/media",
		string(uploadDeclaration),
		accountID,
	)
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, authorizeUpload)
	if uploadResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf(
			"unconfigured storage = %d %s",
			uploadResponse.Code,
			uploadResponse.Body.String(),
		)
	}
	var unavailable struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &unavailable); err != nil {
		t.Fatal(err)
	}
	if unavailable.Error.Code != "media_storage_unavailable" ||
		!unavailable.Error.Retryable {
		t.Fatalf("unconfigured storage response = %s", uploadResponse.Body.String())
	}

	objects := newFakeObjectStore()
	mediaHandler := runtimeMediaHandler(t, database, module, objects)
	authorizeUpload = authenticatedRuntimeRequest(
		http.MethodPost,
		"/api/v1/workspaces/"+workspaceID+"/composer/media",
		string(uploadDeclaration),
		accountID,
	)
	uploadResponse = httptest.NewRecorder()
	mediaHandler.ServeHTTP(uploadResponse, authorizeUpload)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("authorize upload = %d %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var upload MediaUpload
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &upload); err != nil {
		t.Fatal(err)
	}
	if upload.UploadURL == "" || upload.CompleteURL == "" ||
		objects.uploadSize != int64(len(png)) ||
		objects.uploadType != "image/png" {
		t.Fatalf("upload authorization = %#v", upload)
	}
	objects.putAuthorized(png, "image/png")
	complete := authenticatedRuntimeRequest(
		http.MethodPost,
		upload.CompleteURL,
		"",
		accountID,
	)
	completeResponse := httptest.NewRecorder()
	mediaHandler.ServeHTTP(completeResponse, complete)
	if completeResponse.Code != http.StatusOK {
		t.Fatalf(
			"complete upload = %d %s",
			completeResponse.Code,
			completeResponse.Body.String(),
		)
	}
	var inspected Media
	if err := json.Unmarshal(completeResponse.Body.Bytes(), &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.InspectionStatus != InspectionReady ||
		inspected.ContentType != "image/png" ||
		inspected.URL == "" {
		t.Fatalf("inspected media = %#v", inspected)
	}
	download := authenticatedRuntimeRequest(
		http.MethodGet,
		inspected.URL,
		"",
		accountID,
	)
	downloadResponse := httptest.NewRecorder()
	mediaHandler.ServeHTTP(downloadResponse, download)
	if downloadResponse.Code != http.StatusOK ||
		!bytes.Contains(downloadResponse.Body.Bytes(), []byte("signature=test")) {
		t.Fatalf(
			"download authorization = %d %s",
			downloadResponse.Code,
			downloadResponse.Body.String(),
		)
	}
	updatePayload, err := json.Marshal(map[string]any{
		"expected_revision": 1,
		"autosave_key":      "runtime-media-attach",
		"content": DraftContent{
			Text:         created.Draft.Content.Text,
			Link:         created.Draft.Content.Link,
			Media:        []Media{{ID: inspected.ID}},
			Thread:       created.Draft.Content.Thread,
			Destinations: created.Draft.Content.Destinations,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	update := authenticatedRuntimeRequest(
		http.MethodPut,
		"/api/v1/workspaces/"+workspaceID+"/drafts/"+created.Draft.ID,
		string(updatePayload),
		accountID,
	)
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf(
			"atomic media draft update = %d %s",
			updateResponse.Code,
			updateResponse.Body.String(),
		)
	}
	objects.mutex.Lock()
	retained := objects.objects[objects.uploadKey].retained
	objects.mutex.Unlock()
	if !retained {
		t.Fatal("attached object did not move to retained lifecycle state")
	}
	var byteColumnExists bool
	if err := database.QueryRow(`
		SELECT EXISTS (
			SELECT 1
			  FROM information_schema.columns
			 WHERE table_name = 'f06_composer_media'
			   AND column_name IN ('content', 'bytes', 'data')
		)`).Scan(&byteColumnExists); err != nil {
		t.Fatal(err)
	}
	if byteColumnExists {
		t.Fatal("composer media table contains an object-byte column")
	}
}

func TestPostgresDestinationResolverRejectsUnknownCrossWorkspaceAndDisconnectedChannels(
	t *testing.T,
) {
	databaseURL := os.Getenv("F06_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F06_DATABASE_URL is not configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	workspaceID, accountID := createRuntimeWorkspace(t, database, "resolver-a")
	otherWorkspaceID, _ := createRuntimeWorkspace(t, database, "resolver-b")
	insertConnectedRuntimeChannel(
		t,
		database,
		runtimeChannel{
			ID:          "channel-cross-" + otherWorkspaceID,
			WorkspaceID: otherWorkspaceID,
			Provider:    "facebook_pages",
			Resource:    "facebook_page",
			AccountType: "page",
			DisplayName: "Other workspace channel",
			ActorID:     accountID,
		},
	)
	insertDisconnectedRuntimeChannel(
		t,
		database,
		runtimeChannel{
			ID:          "channel-disconnected-" + workspaceID,
			WorkspaceID: workspaceID,
			Provider:    "instagram_professional",
			Resource:    "instagram_professional",
			AccountType: "business",
			DisplayName: "Disconnected IG",
			ActorID:     accountID,
		},
	)
	insertConnectedRuntimeChannel(
		t,
		database,
		runtimeChannel{
			ID:          "channel-invalid-linkedin-" + workspaceID,
			WorkspaceID: workspaceID,
			Provider:    "linkedin",
			Resource:    "linkedin_page",
			AccountType: "profile",
			DisplayName: "Broken LinkedIn channel",
			ActorID:     accountID,
		},
	)
	catalog, err := ParseCapabilityCatalog(runtimeCapabilityCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewPostgresDestinationResolver(database, catalog)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		NewMemoryRepository(),
		authorizerStub{allowed: true},
		WithCapabilityCatalog(catalog),
		WithDestinationResolver(resolver),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		channelID string
		code      string
	}{
		{name: "unknown", channelID: "channel-missing", code: "channel_unknown"},
		{
			name:      "cross-workspace",
			channelID: "channel-cross-" + otherWorkspaceID,
			code:      "channel_workspace_mismatch",
		},
		{
			name:      "disconnected",
			channelID: "channel-disconnected-" + workspaceID,
			code:      "channel_disconnected",
		},
		{
			name:      "unsupported-resource",
			channelID: "channel-invalid-linkedin-" + workspaceID,
			code:      "channel_resource_unsupported",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.CreateDraft(context.Background(), CreateDraftCommand{
				WorkspaceID: workspaceID,
				ActorID:     accountID,
				Content: DraftContent{
					Text: "test",
					Destinations: []Destination{{
						ID:        "dest-1",
						ChannelID: test.channelID,
						Format:    FormatText,
					}},
				},
			})
			var fieldError *FieldRuleError
			if !errors.As(err, &fieldError) || fieldError.Code != test.code {
				t.Fatalf("resolver error = %#v", err)
			}
		})
	}
}

func TestPostgresDestinationResolverDisambiguatesLinkedInProfileAndPage(
	t *testing.T,
) {
	databaseURL := os.Getenv("F06_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F06_DATABASE_URL is not configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	workspaceID, accountID := createRuntimeWorkspace(t, database, "linkedin")
	insertConnectedRuntimeChannel(
		t,
		database,
		runtimeChannel{
			ID:          "channel-linkedin-profile-" + workspaceID,
			WorkspaceID: workspaceID,
			Provider:    "linkedin",
			Resource:    "linkedin_profile",
			AccountType: "profile",
			DisplayName: "LinkedIn profile",
			ActorID:     accountID,
		},
	)
	insertConnectedRuntimeChannel(
		t,
		database,
		runtimeChannel{
			ID:          "channel-linkedin-page-" + workspaceID,
			WorkspaceID: workspaceID,
			Provider:    "linkedin",
			Resource:    "linkedin_page",
			AccountType: "organization",
			DisplayName: "LinkedIn page",
			ActorID:     accountID,
		},
	)

	module := newRuntimeModule(t, database)
	if err := module.Configure(map[string]string{
		configCapabilitiesJSON: runtimeCapabilityCatalog(t),
		configAllowedOrigins:   "https://postqron.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := module.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler, ok := module.Handler(composerRuntimeHandlerName)
	if !ok {
		t.Fatal("runtime handler missing")
	}

	tests := []struct {
		name          string
		channelID     string
		expectedType  ChannelType
		expectedCapID string
	}{
		{
			name:          "profile",
			channelID:     "channel-linkedin-profile-" + workspaceID,
			expectedType:  "linkedin_profile_text",
			expectedCapID: "runtime:linkedin:profile:text",
		},
		{
			name:          "page",
			channelID:     "channel-linkedin-page-" + workspaceID,
			expectedType:  "linkedin_page_text",
			expectedCapID: "runtime:linkedin:page:text",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{
				"content": DraftContent{
					Text: "linkedin ready",
					Destinations: []Destination{{
						ID:           "linkedin-text",
						ChannelID:    test.channelID,
						ChannelType:  "ignored",
						CapabilityID: "ignored",
						Format:       FormatText,
					}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			request := authenticatedRuntimeRequest(
				http.MethodPost,
				"/api/v1/workspaces/"+workspaceID+"/drafts",
				string(payload),
				accountID,
			)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusCreated {
				t.Fatalf("create = %d %s", response.Code, response.Body.String())
			}
			var created DraftView
			if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			destination := created.Draft.Content.Destinations[0]
			if destination.ChannelType != test.expectedType ||
				destination.CapabilityID != test.expectedCapID {
				t.Fatalf("linkedin destination = %#v", destination)
			}
		})
	}
}

func TestPostgresRuntimeIntegrationValidatesMP4FromUploadedBytesAcrossVideoFamilies(
	t *testing.T,
) {
	databaseURL := os.Getenv("F06_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F06_DATABASE_URL is not configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	workspaceID, accountID := createRuntimeWorkspace(t, database, "mp4")
	insertConnectedRuntimeChannel(
		t,
		database,
		runtimeChannel{
			ID:          "channel-facebook-video-" + workspaceID,
			WorkspaceID: workspaceID,
			Provider:    "facebook_pages",
			Resource:    "facebook_page",
			AccountType: "page",
			DisplayName: "Facebook video",
			ActorID:     accountID,
		},
	)
	insertConnectedRuntimeChannel(
		t,
		database,
		runtimeChannel{
			ID:          "channel-instagram-video-" + workspaceID,
			WorkspaceID: workspaceID,
			Provider:    "instagram_professional",
			Resource:    "instagram_professional",
			AccountType: "creator",
			DisplayName: "Instagram creator",
			ActorID:     accountID,
		},
	)

	module := newRuntimeModule(t, database)
	if err := module.Configure(map[string]string{
		configCapabilitiesJSON: runtimeCapabilityCatalog(t),
		configAllowedOrigins:   "https://postqron.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := module.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler, ok := module.Handler(composerRuntimeHandlerName)
	if !ok {
		t.Fatal("runtime handler missing")
	}
	objects := newFakeObjectStore()
	mediaHandler := runtimeMediaHandler(t, database, module, objects)

	mp4 := testMP4()
	uploadDeclaration, err := json.Marshal(MediaUploadRequest{
		FileName: "clip.mp4", ContentType: "video/mp4", SizeBytes: int64(len(mp4)),
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizeUpload := authenticatedRuntimeRequest(
		http.MethodPost,
		"/api/v1/workspaces/"+workspaceID+"/composer/media",
		string(uploadDeclaration),
		accountID,
	)
	uploadResponse := httptest.NewRecorder()
	mediaHandler.ServeHTTP(uploadResponse, authorizeUpload)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("authorize MP4 upload = %d %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var upload MediaUpload
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &upload); err != nil {
		t.Fatal(err)
	}
	objects.putAuthorized(mp4, "video/mp4")
	complete := authenticatedRuntimeRequest(http.MethodPost, upload.CompleteURL, "", accountID)
	completeResponse := httptest.NewRecorder()
	mediaHandler.ServeHTTP(completeResponse, complete)
	if completeResponse.Code != http.StatusOK {
		t.Fatalf("complete MP4 upload = %d %s", completeResponse.Code, completeResponse.Body.String())
	}
	var inspected Media
	if err := json.Unmarshal(completeResponse.Body.Bytes(), &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Width != 1080 ||
		inspected.Height != 1920 ||
		inspected.DurationSeconds != 30 ||
		inspected.VideoCodec != "h264" ||
		inspected.AudioCodec != "aac" ||
		!inspected.HasAudio {
		t.Fatalf("inspected MP4 metadata = %#v", inspected)
	}

	createPayload, err := json.Marshal(map[string]any{
		"content": DraftContent{
			Text:  "clip",
			Media: []Media{{ID: inspected.ID}},
			Destinations: []Destination{
				{
					ID:        "facebook-video",
					ChannelID: "channel-facebook-video-" + workspaceID,
					Format:    FormatVideo,
				},
				{
					ID:        "instagram-video",
					ChannelID: "channel-instagram-video-" + workspaceID,
					Format:    FormatVideo,
				},
				{
					ID:        "instagram-reel",
					ChannelID: "channel-instagram-video-" + workspaceID,
					Format:    FormatShortVideo,
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	create := authenticatedRuntimeRequest(
		http.MethodPost,
		"/api/v1/workspaces/"+workspaceID+"/drafts",
		string(createPayload),
		accountID,
	)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create MP4 draft = %d %s", createResponse.Code, createResponse.Body.String())
	}
	var created DraftView
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Validation.Valid || len(created.Validation.Destinations) != 3 {
		t.Fatalf("validation = %#v", created.Validation)
	}
	for _, destination := range created.Validation.Destinations {
		if !destination.Valid {
			t.Fatalf("destination validation failed: %#v", destination)
		}
	}
}

type runtimeChannel struct {
	ID          string
	WorkspaceID string
	Provider    string
	Resource    string
	AccountType string
	DisplayName string
	ActorID     string
}

func authenticatedRuntimeRequest(
	method, target, body, accountID string,
) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request.WithContext(featureruntime.WithAuthenticatedAccount(
		request.Context(),
		accountID,
	))
}

func createRuntimeWorkspace(t *testing.T, database *sql.DB, suffix string) (string, string) {
	t.Helper()
	stamp := suffix + "-" + time.Now().UTC().Format("20060102150405.000000000")
	workspaceID := "workspace-runtime-" + stamp
	accountID := "account-runtime-" + stamp
	now := time.Now().UTC()
	if _, err := database.Exec(`
		INSERT INTO f04_workspaces (
			id, personal_account_id, name, status, created_at, updated_at
		) VALUES ($1, $2, 'F6 runtime test', 'active', $3, $3)`,
		workspaceID,
		accountID,
		now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO f04_memberships (
			workspace_id, account_id, role, status, created_at, updated_at
		) VALUES ($1, $2, 'owner', 'active', $3, $3)`,
		workspaceID,
		accountID,
		now,
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.Exec(`DELETE FROM f06_composer_media WHERE workspace_id = $1`, workspaceID)
		_, _ = database.Exec(`DELETE FROM f06_composer_drafts WHERE workspace_id = $1`, workspaceID)
		_, _ = database.Exec(`DELETE FROM f05_social_outbox WHERE workspace_id = $1`, workspaceID)
		_, _ = database.Exec(`DELETE FROM f05_social_connections WHERE workspace_id = $1`, workspaceID)
		_, _ = database.Exec(`DELETE FROM f04_memberships WHERE workspace_id = $1`, workspaceID)
		_, _ = database.Exec(`DELETE FROM f04_workspaces WHERE id = $1`, workspaceID)
	})
	return workspaceID, accountID
}

func insertConnectedRuntimeChannel(t *testing.T, database *sql.DB, channel runtimeChannel) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := database.Exec(`
		INSERT INTO f05_social_connections (
			id, workspace_id, provider, remote_id, resource_type, account_type,
			display_name, scopes, status, access_token_key_id,
			access_token_ciphertext, connected_by_actor_id, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, '["publish"]'::jsonb, 'connected',
			'test-key', $8, $9, $10, $10
		)`,
		channel.ID,
		channel.WorkspaceID,
		channel.Provider,
		"remote-"+channel.ID,
		channel.Resource,
		channel.AccountType,
		channel.DisplayName,
		bytes.Repeat([]byte{0x42}, 32),
		channel.ActorID,
		now,
	); err != nil {
		t.Fatal(err)
	}
}

func insertDisconnectedRuntimeChannel(
	t *testing.T,
	database *sql.DB,
	channel runtimeChannel,
) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := database.Exec(`
		INSERT INTO f05_social_connections (
			id, workspace_id, provider, remote_id, resource_type, account_type,
			display_name, scopes, status, reconnect_reason,
			connected_by_actor_id, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, '["publish"]'::jsonb,
			'reconnect_required', 'token_expired', $8, $9, $9
		)`,
		channel.ID,
		channel.WorkspaceID,
		channel.Provider,
		"remote-"+channel.ID,
		channel.Resource,
		channel.AccountType,
		channel.DisplayName,
		channel.ActorID,
		now,
	); err != nil {
		t.Fatal(err)
	}
}

func newRuntimeModule(t *testing.T, database *sql.DB) *Module {
	t.Helper()
	module, err := NewPostgresModule(database, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func runtimeMediaHandler(
	t *testing.T,
	database *sql.DB,
	module *Module,
	objects *fakeObjectStore,
) http.Handler {
	t.Helper()
	mediaStore, err := NewPostgresMediaStore(
		database,
		objects,
		StreamMediaInspector{},
		time.Now,
		defaultMaximumUploadBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	module.repository.BindMediaStore(mediaStore)
	module.service.media = mediaStore
	return NewMediaHTTPHandler(
		mediaStore,
		module.authorizer,
		runtimeComposerAuthenticator{},
	)
}

func runtimeCapabilityCatalog(t *testing.T) string {
	t.Helper()
	const raw = `{
	  "version": "runtime-provider-v1",
	  "status": "test_fixture",
	  "capabilities": [
	    {
	      "id": "runtime:facebook:text",
	      "provider": "facebook_pages",
	      "channel_type": "facebook_page_text",
	      "format": "text",
	      "available": true,
	      "text": {"allowed": true, "required": true, "max_characters": 280},
	      "link": {"allowed": true, "maximum_urls": 1, "require_https": true, "require_public_host": true},
	      "media": {"allowed": false},
	      "thread": {"allowed": false}
	    },
	    {
	      "id": "runtime:facebook:video",
	      "provider": "facebook_pages",
	      "channel_type": "facebook_page_video",
	      "format": "video",
	      "available": true,
	      "text": {"allowed": true, "max_characters": 5000},
	      "link": {"allowed": false},
	      "media": {
	        "allowed": true,
	        "minimum_items": 1,
	        "maximum_items": 1,
	        "allowed_kinds": ["video"],
	        "allowed_content_types": ["video/mp4"],
	        "maximum_bytes_each": 536870912,
	        "allowed_video_codecs": ["h264"],
	        "allowed_audio_codecs": ["aac"],
	        "require_audio": true
	      },
	      "thread": {"allowed": false}
	    },
	    {
	      "id": "runtime:instagram:video",
	      "provider": "instagram_professional",
	      "channel_type": "instagram_professional_video",
	      "format": "video",
	      "available": true,
	      "text": {"allowed": true, "max_characters": 2200},
	      "link": {"allowed": false},
	      "media": {
	        "allowed": true,
	        "minimum_items": 1,
	        "maximum_items": 1,
	        "allowed_kinds": ["video"],
	        "allowed_content_types": ["video/mp4"],
	        "maximum_bytes_each": 104857600,
	        "allowed_video_codecs": ["h264"],
	        "allowed_audio_codecs": ["aac"],
	        "require_audio": true
	      },
	      "thread": {"allowed": false}
	    },
	    {
	      "id": "runtime:instagram:reel",
	      "provider": "instagram_professional",
	      "channel_type": "instagram_professional_reel",
	      "format": "short_video",
	      "available": true,
	      "text": {"allowed": true, "max_characters": 2200},
	      "link": {"allowed": false},
	      "media": {
	        "allowed": true,
	        "minimum_items": 1,
	        "maximum_items": 1,
	        "allowed_kinds": ["video"],
	        "allowed_content_types": ["video/mp4"],
	        "maximum_bytes_each": 104857600,
	        "minimum_aspect_ratio": 0.5625,
	        "maximum_aspect_ratio": 0.5625,
	        "maximum_duration_seconds": 90,
	        "allowed_video_codecs": ["h264"],
	        "allowed_audio_codecs": ["aac"],
	        "require_audio": true
	      },
	      "thread": {"allowed": false}
	    },
	    {
	      "id": "runtime:linkedin:profile:text",
	      "provider": "linkedin",
	      "channel_type": "linkedin_profile_text",
	      "format": "text",
	      "available": true,
	      "text": {"allowed": true, "required": true, "max_characters": 3000},
	      "link": {"allowed": true, "maximum_urls": 1, "require_https": true, "require_public_host": true},
	      "media": {"allowed": false},
	      "thread": {"allowed": false}
	    },
	    {
	      "id": "runtime:linkedin:page:text",
	      "provider": "linkedin",
	      "channel_type": "linkedin_page_text",
	      "format": "text",
	      "available": true,
	      "text": {"allowed": true, "required": true, "max_characters": 3000},
	      "link": {"allowed": true, "maximum_urls": 1, "require_https": true, "require_public_host": true},
	      "media": {"allowed": false},
	      "thread": {"allowed": false}
	    }
	  ]
	}`
	if _, err := ParseCapabilityCatalog(raw); err != nil {
		t.Fatal(err)
	}
	return raw
}
