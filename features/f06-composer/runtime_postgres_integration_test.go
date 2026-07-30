package composer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	workspaceID := "workspace-runtime-" + suffix
	accountID := "account-runtime-" + suffix
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
		_, _ = database.Exec(
			`DELETE FROM f06_composer_media WHERE workspace_id = $1`,
			workspaceID,
		)
		_, _ = database.Exec(
			`DELETE FROM f06_composer_drafts WHERE workspace_id = $1`,
			workspaceID,
		)
		_, _ = database.Exec(
			`DELETE FROM f04_memberships WHERE workspace_id = $1`,
			workspaceID,
		)
		_, _ = database.Exec(
			`DELETE FROM f04_workspaces WHERE id = $1`,
			workspaceID,
		)
	})

	module, err := NewPostgresModule(database, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("test/fixtures/capabilities.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := module.Configure(map[string]string{
		configCapabilitiesJSON: string(fixture),
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

	create := authenticatedRuntimeRequest(
		http.MethodPost,
		"/api/v1/workspaces/"+workspaceID+"/drafts",
		`{"content":{"text":"ready","link":"","media":[],"thread":[],
			"destinations":[{"id":"text","channel_id":"channel-1",
			"channel_type":"fixture_text_channel","capability_id":"fixture:text",
			"format":"text"}]}}`,
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
	mediaHandler := NewMediaHTTPHandler(
		mediaStore,
		module.authorizer,
		runtimeComposerAuthenticator{},
	)
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
	if err := mediaStore.Attach(
		context.Background(),
		workspaceID,
		created.Draft.ID,
		[]string{inspected.ID},
	); err != nil {
		t.Fatal(err)
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
