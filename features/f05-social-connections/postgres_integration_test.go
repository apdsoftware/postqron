package socialconnections

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresRepositoryConnectionLifecycle(t *testing.T) {
	databaseURL := os.Getenv("F05_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F05_DATABASE_URL is not set")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	workspaceID := "integration-workspace-" + suffix
	actorID := "integration-owner-" + suffix
	remoteID := "integration-ig-" + suffix
	repository, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &fakeAuthorizer{permissions: map[Permission]bool{
		PermissionViewWorkspace:  true,
		PermissionManageChannels: true,
	}}
	cipher, err := NewAESGCMCipher(
		"integration-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	expired := serviceTestNow.Add(-time.Minute)
	refreshedExpiry := serviceTestNow.Add(50 * 24 * time.Hour)
	instagram := &fakeAdapter{
		config: OAuthConfig{
			ClientID:         "instagram-client",
			AuthorizationURL: "https://www.instagram.com/oauth/authorize",
			RedirectURL:      "https://app.example.test/social/callback",
			Scopes: append(
				[]string(nil),
				requiredScopes[ProviderInstagramProfessional]...,
			),
		},
		grant: Credential{
			AccessToken: "integration-grant-token",
			Scopes: append(
				[]string(nil),
				requiredScopes[ProviderInstagramProfessional]...,
			),
		},
		resources: []DiscoveredResource{instagramResource(
			remoteID,
			"postqron-integration",
			"integration-access-token",
			&expired,
		)},
		refreshResult: Credential{
			AccessToken: "integration-refreshed-token",
			ExpiresAt:   &refreshedExpiry,
			Scopes: append(
				[]string(nil),
				requiredScopes[ProviderInstagramProfessional]...,
			),
		},
	}
	facebook := &fakeAdapter{
		config: OAuthConfig{
			ClientID:         "facebook-client",
			AuthorizationURL: "https://www.facebook.com/v25.0/dialog/oauth",
			RedirectURL:      "https://app.example.test/social/callback",
			Scopes: append(
				[]string(nil),
				requiredScopes[ProviderFacebookPages]...,
			),
		},
	}
	service, err := NewService(Config{
		Repository: repository,
		Authorizer: authorizer,
		Cipher:     cipher,
		Adapters: map[Provider]Adapter{
			ProviderFacebookPages:         facebook,
			ProviderInstagramProfessional: instagram,
		},
		Now: func() time.Time { return serviceTestNow },
	})
	if err != nil {
		t.Fatal(err)
	}

	connection := postgresConnectInstagram(
		t,
		service,
		workspaceID,
		actorID,
		remoteID,
	)
	var ciphertext []byte
	var keyID, status string
	if err = database.QueryRowContext(context.Background(), `
		SELECT access_token_key_id, access_token_ciphertext, status
		FROM f05_social_connections
		WHERE id = $1`,
		connection.ID,
	).Scan(&keyID, &ciphertext, &status); err != nil {
		t.Fatal(err)
	}
	if keyID != "integration-key" || status != string(StatusConnected) {
		t.Fatalf("stored key/status = %q/%q", keyID, status)
	}
	if bytes.Contains(ciphertext, []byte("integration-access-token")) {
		t.Fatal("PostgreSQL contains plaintext access token")
	}

	token, err := service.AccessToken(
		context.Background(),
		workspaceID,
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if token != "integration-refreshed-token" {
		t.Fatalf("refreshed token = %q", token)
	}

	if _, err = database.ExecContext(context.Background(), `
		UPDATE f05_social_connections
		SET token_expires_at = $2
		WHERE id = $1`,
		connection.ID,
		expired,
	); err != nil {
		t.Fatal(err)
	}
	instagram.refreshErr = &ProviderFailure{
		Kind: FailureAuthentication,
		Code: "meta_error_190",
	}
	if _, err = service.AccessToken(
		context.Background(),
		workspaceID,
		connection.ID,
	); !errors.Is(err, ErrReconnectRequired) {
		t.Fatalf("revoked AccessToken() error = %v", err)
	}
	var accessTokenMissing, refreshTokenMissing bool
	if err = database.QueryRowContext(context.Background(), `
		SELECT
			status,
			access_token_ciphertext IS NULL,
			refresh_token_ciphertext IS NULL
		FROM f05_social_connections
		WHERE id = $1`,
		connection.ID,
	).Scan(&status, &accessTokenMissing, &refreshTokenMissing); err != nil {
		t.Fatal(err)
	}
	if status != string(StatusReconnectRequired) ||
		!accessTokenMissing ||
		!refreshTokenMissing {
		t.Fatalf(
			"reconnect row = status %q, credentials missing %v/%v",
			status,
			accessTokenMissing,
			refreshTokenMissing,
		)
	}
	if _, err = service.AccessToken(
		context.Background(),
		workspaceID,
		connection.ID,
	); !errors.Is(err, ErrReconnectRequired) {
		t.Fatalf("repeated AccessToken() error = %v", err)
	}
	refreshCalls, _, _ := instagram.counts()
	if refreshCalls != 2 {
		t.Fatalf("refresh calls = %d, want refresh plus one failed check", refreshCalls)
	}

	instagram.refreshErr = nil
	instagram.resources = []DiscoveredResource{instagramResource(
		remoteID,
		"postqron-integration",
		"integration-reconnected-token",
		&refreshedExpiry,
	)}
	reconnected := postgresConnectInstagram(
		t,
		service,
		workspaceID,
		actorID,
		remoteID,
	)
	if reconnected.ID != connection.ID ||
		reconnected.Status != StatusConnected {
		t.Fatalf("reconnected connection = %#v", reconnected)
	}
	result, err := service.Revoke(
		context.Background(),
		workspaceID,
		actorID,
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ProviderRevoked || result.Connection.Status != StatusRevoked {
		t.Fatalf("revocation result = %#v", result)
	}
	var eventCount int
	if err = database.QueryRowContext(context.Background(), `
		SELECT count(*)
		FROM f05_social_outbox
		WHERE connection_id = $1`,
		connection.ID,
	).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 5 {
		t.Fatalf("outbox event count = %d, want 5", eventCount)
	}
}

func postgresConnectInstagram(
	t *testing.T,
	service *Service,
	workspaceID, actorID, remoteID string,
) Connection {
	t.Helper()
	authorization, err := service.Begin(context.Background(), BeginRequest{
		WorkspaceID: workspaceID,
		ActorID:     actorID,
		Provider:    ProviderInstagramProfessional,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorization.URL)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := service.Callback(context.Background(), CallbackRequest{
		State: parsed.Query().Get("state"),
		Code:  "integration-code",
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := service.Select(context.Background(), SelectRequest{
		WorkspaceID: workspaceID,
		ActorID:     actorID,
		SelectionID: selection.ID,
		RemoteID:    remoteID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return connection
}
