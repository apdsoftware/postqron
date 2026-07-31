package socialconnections

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
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
	newRuntime := func() (*PostgresRepository, *Service) {
		t.Helper()
		repository, runtimeErr := NewPostgresRepository(database)
		if runtimeErr != nil {
			t.Fatal(runtimeErr)
		}
		service, runtimeErr := NewService(Config{
			Repository: repository,
			Authorizer: authorizer,
			Cipher:     cipher,
			Quota:      newFakeChannelQuota(),
			Adapters: map[Provider]Adapter{
				ProviderFacebookPages:         facebook,
				ProviderInstagramProfessional: instagram,
			},
			Now: func() time.Time { return serviceTestNow },
		})
		if runtimeErr != nil {
			t.Fatal(runtimeErr)
		}
		return repository, service
	}
	repository, service := newRuntime()
	authorization, err := service.Begin(context.Background(), BeginRequest{
		WorkspaceID: workspaceID,
		ActorID:     actorID,
		Provider:    ProviderInstagramProfessional,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsedAuthorization, err := url.Parse(authorization.URL)
	if err != nil {
		t.Fatal(err)
	}
	repository, service = newRuntime()
	selection, err := service.Callback(context.Background(), CallbackRequest{
		State: parsedAuthorization.Query().Get("state"),
		Code:  "integration-code",
	})
	if err != nil {
		t.Fatal(err)
	}
	repository, service = newRuntime()
	connection, err := service.Select(context.Background(), SelectRequest{
		WorkspaceID: workspaceID,
		ActorID:     actorID,
		SelectionID: selection.ID,
		RemoteID:    remoteID,
	})
	if err != nil {
		t.Fatal(err)
	}
	listedAfterRestart, err := service.List(
		context.Background(),
		workspaceID,
		actorID,
	)
	if err != nil || len(listedAfterRestart) != 1 ||
		listedAfterRestart[0].ID != connection.ID {
		t.Fatalf("connections after process restart = %#v, error %v", listedAfterRestart, err)
	}
	if _, err = service.AccessToken(
		context.Background(),
		"other-"+workspaceID,
		connection.ID,
	); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("cross-workspace credential error = %v", err)
	}
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
	firstLease, claimed, err := repository.ClaimRefresh(
		context.Background(),
		workspaceID,
		connection.ID,
		serviceTestNow,
		serviceTestNow,
		time.Second,
	)
	if err != nil || !claimed || firstLease.RefreshLeaseID == "" {
		t.Fatalf("first PostgreSQL refresh lease = %#v, claimed %v, error %v", firstLease, claimed, err)
	}
	secondLease, claimed, err := repository.ClaimRefresh(
		context.Background(),
		workspaceID,
		connection.ID,
		serviceTestNow.Add(2*time.Second),
		serviceTestNow.Add(2*time.Second),
		time.Second,
	)
	if err != nil || !claimed || secondLease.RefreshLeaseID == "" ||
		secondLease.RefreshLeaseID == firstLease.RefreshLeaseID {
		t.Fatalf("second PostgreSQL refresh lease = %#v, claimed %v, error %v", secondLease, claimed, err)
	}
	if _, err = repository.CompleteRefresh(
		context.Background(),
		RefreshCommand{
			ConnectionID:           connection.ID,
			RefreshLeaseID:         firstLease.RefreshLeaseID,
			AccessTokenCiphertext:  firstLease.AccessTokenCiphertext,
			RefreshTokenCiphertext: firstLease.RefreshTokenCiphertext,
			Scopes:                 firstLease.Scopes,
			ExpiresAt:              firstLease.TokenExpiresAt,
			VerifiedAt:             serviceTestNow,
			Now:                    serviceTestNow,
		},
	); !errors.Is(err, ErrRefreshInProgress) {
		t.Fatalf("stale PostgreSQL refresh completion error = %v", err)
	}
	if err = repository.ReleaseRefresh(
		context.Background(),
		workspaceID,
		connection.ID,
		firstLease.RefreshLeaseID,
	); !errors.Is(err, ErrRefreshInProgress) {
		t.Fatalf("stale PostgreSQL refresh release error = %v", err)
	}
	if _, _, err = repository.MarkReconnectRequired(
		context.Background(),
		workspaceID,
		connection.ID,
		firstLease.RefreshLeaseID,
		"stale_refresh_failure",
		serviceTestNow.Add(2*time.Second),
		Event{},
	); !errors.Is(err, ErrRefreshInProgress) {
		t.Fatalf("stale PostgreSQL refresh reconnect error = %v", err)
	}
	if err = repository.ReleaseRefresh(
		context.Background(),
		workspaceID,
		connection.ID,
		secondLease.RefreshLeaseID,
	); err != nil {
		t.Fatal(err)
	}
	instagram.refreshErr = &ProviderFailure{
		Kind: FailurePermissionMissing,
		Code: "x_required_scope_missing",
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
	var eventCount int
	if err = database.QueryRowContext(context.Background(), `
		SELECT count(*)
		FROM f05_social_outbox
		WHERE connection_id = $1
			AND event_type = $2`,
		connection.ID,
		EventReconnectRequired,
	).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("reconnect event count = %d, want 1", eventCount)
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
	testPostgresF10ChannelQuota(t, database, suffix)
}

func TestPostgresRepositoryLinkedInRefreshAuthenticationFailureMarksReconnectRequired(
	t *testing.T,
) {
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
	service, err := NewService(Config{
		Repository: repository,
		Authorizer: authorizer,
		Cipher:     cipher,
		Quota:      newFakeChannelQuota(),
		Adapters: map[Provider]Adapter{
			ProviderLinkedIn: newLinkedInRefreshFailureAdapter(
				t,
				http.StatusBadRequest,
				`{"error":"invalid_request","error_description":"refresh token expired"}`,
			),
		},
		Now: func() time.Time { return serviceTestNow },
	})
	if err != nil {
		t.Fatal(err)
	}

	connection := postgresConnectLinkedIn(
		t,
		service,
		workspaceID,
		actorID,
		linkedInFixtureMemberID,
	)
	expired := serviceTestNow.Add(-time.Minute)
	if _, err = database.ExecContext(context.Background(), `
		UPDATE f05_social_connections
		SET token_expires_at = $2
		WHERE id = $1`,
		connection.ID,
		expired,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AccessToken(
		context.Background(),
		workspaceID,
		connection.ID,
	); !errors.Is(err, ErrReconnectRequired) {
		t.Fatalf("LinkedIn AccessToken() error = %v", err)
	}
	var (
		status              string
		reconnectReason     string
		accessTokenMissing  bool
		refreshTokenMissing bool
	)
	if err = database.QueryRowContext(context.Background(), `
		SELECT
			status,
			reconnect_reason,
			access_token_ciphertext IS NULL,
			refresh_token_ciphertext IS NULL
		FROM f05_social_connections
		WHERE id = $1`,
		connection.ID,
	).Scan(
		&status,
		&reconnectReason,
		&accessTokenMissing,
		&refreshTokenMissing,
	); err != nil {
		t.Fatal(err)
	}
	if status != string(StatusReconnectRequired) ||
		reconnectReason != string(FailureAuthentication) ||
		!accessTokenMissing ||
		!refreshTokenMissing {
		t.Fatalf(
			"LinkedIn reconnect row = status %q reason %q credentials missing %v/%v",
			status,
			reconnectReason,
			accessTokenMissing,
			refreshTokenMissing,
		)
	}
}

func TestPostgresRepositoryDynamicSessionLeaseAndCrashSafety(t *testing.T) {
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
	workspaceID := "dynamic-pg-workspace-" + suffix
	actorID := "dynamic-pg-owner-" + suffix
	connectionID := "dynamic-pg-connection-" + suffix
	selectionID := "dynamic-pg-selection-" + suffix
	remoteID := dynamicTestDID + "-" + suffix
	repository, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := NewAESGCMCipher(
		"dynamic-pg-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := OAuthBinding{
		Issuer:         "https://auth.example.com",
		ResourceServer: "https://pds.example.com",
		Subject:        dynamicTestDID,
	}
	now := serviceTestNow
	expired := now.Add(-time.Minute)
	credentialAAD := credentialAdditionalData(
		workspaceID,
		ProviderBluesky,
		remoteID,
	)
	access, err := cipher.Seal([]byte("dynamic-pg-access-1"), credentialAAD)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := cipher.Seal([]byte("dynamic-pg-refresh-1"), credentialAAD)
	if err != nil {
		t.Fatal(err)
	}
	session, err := cipher.Seal(
		[]byte("dynamic-pg-dpop-key|as-1|rs-1"),
		dynamicSessionAdditionalData(
			workspaceID,
			ProviderBluesky,
			remoteID,
			binding,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.SaveSelection(context.Background(), StoredSelection{
		ID:          selectionID,
		WorkspaceID: workspaceID,
		ActorID:     actorID,
		Provider:    ProviderBluesky,
		Resources: []StoredResource{{
			Candidate: Candidate{
				RemoteID:     remoteID,
				ResourceType: ResourceBlueskyAccount,
				AccountType:  AccountTypeProfile,
				DisplayName:  "PostgreSQL dynamic profile",
				Scopes:       []string{"atproto"},
			},
			AccessTokenCiphertext:  access,
			RefreshTokenCiphertext: refresh,
			OAuthSessionCiphertext: session,
			Binding:                binding,
			RefreshTokenMode:       RefreshTokenSingleUse,
			TokenExpiresAt:         &expired,
		}},
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err = repository.Connect(context.Background(), ConnectCommand{
		NewConnectionID: connectionID,
		WorkspaceID:     workspaceID,
		ActorID:         actorID,
		SelectionID:     selectionID,
		RemoteID:        remoteID,
		Now:             now,
		Event: Event{
			ID:            "dynamic-pg-event-connect-" + suffix,
			Type:          EventConnected,
			Version:       1,
			WorkspaceID:   workspaceID,
			ConnectionID:  connectionID,
			Provider:      ProviderBluesky,
			RemoteID:      remoteID,
			ActorID:       actorID,
			CorrelationID: "dynamic-pg-correlation-connect-" + suffix,
			OccurredAt:    now,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, needsRefresh, err := repository.ClaimSession(
		context.Background(),
		workspaceID,
		connectionID,
		now,
		now.Add(5*time.Minute),
		time.Second,
	)
	if err != nil || !needsRefresh {
		t.Fatalf("initial session claim = refresh %v, error %v", needsRefresh, err)
	}
	if _, _, err = repository.ClaimSession(
		context.Background(),
		workspaceID,
		connectionID,
		now,
		now.Add(5*time.Minute),
		time.Second,
	); !errors.Is(err, ErrAuthenticatedRequestInProgress) {
		t.Fatalf("concurrent PostgreSQL claim error = %v", err)
	}
	refreshedExpiry := now.Add(time.Hour)
	access2, _ := cipher.Seal([]byte("dynamic-pg-access-2"), credentialAAD)
	refresh2, _ := cipher.Seal([]byte("dynamic-pg-refresh-2"), credentialAAD)
	session2, _ := cipher.Seal(
		[]byte("dynamic-pg-dpop-key|as-2|rs-1"),
		dynamicSessionAdditionalData(
			workspaceID,
			ProviderBluesky,
			remoteID,
			binding,
		),
	)
	refreshCommand := SessionCommand{
		ConnectionID:           connectionID,
		SessionLeaseID:         claimed.SessionLeaseID,
		AccessTokenCiphertext:  access2,
		RefreshTokenCiphertext: refresh2,
		OAuthSessionCiphertext: session2,
		Scopes:                 []string{"atproto"},
		ExpiresAt:              &refreshedExpiry,
		UpdateCredential:       true,
		VerifiedAt:             now,
		Now:                    now,
		Event: &Event{
			ID:            "dynamic-pg-event-refresh-" + suffix,
			Type:          EventTokenRefreshed,
			Version:       1,
			WorkspaceID:   workspaceID,
			ConnectionID:  connectionID,
			Provider:      ProviderBluesky,
			RemoteID:      remoteID,
			CorrelationID: "dynamic-pg-correlation-refresh-" + suffix,
			OccurredAt:    now,
		},
	}
	if _, err = repository.CompleteSession(
		context.Background(),
		refreshCommand,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.CompleteSession(
		context.Background(),
		refreshCommand,
	); !errors.Is(err, ErrAuthenticatedRequestInProgress) {
		t.Fatalf("replayed session completion error = %v", err)
	}
	persisted, err := repository.GetCredential(
		context.Background(),
		workspaceID,
		connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	openedRefresh, err := cipher.Open(persisted.RefreshTokenCiphertext, credentialAAD)
	if err != nil || string(openedRefresh) != "dynamic-pg-refresh-2" {
		t.Fatalf("persisted rotated refresh = %q, error %v", openedRefresh, err)
	}
	openedSession, err := cipher.Open(
		persisted.OAuthSessionCiphertext,
		dynamicSessionAdditionalData(
			workspaceID,
			ProviderBluesky,
			remoteID,
			binding,
		),
	)
	if err != nil || string(openedSession) != "dynamic-pg-dpop-key|as-2|rs-1" {
		t.Fatalf("persisted session = %q, error %v", openedSession, err)
	}

	if _, err = database.ExecContext(context.Background(), `
		UPDATE f05_social_connections
		SET token_expires_at = $2
		WHERE id = $1`,
		connectionID,
		expired,
	); err != nil {
		t.Fatal(err)
	}
	if _, needsRefresh, err = repository.ClaimSession(
		context.Background(),
		workspaceID,
		connectionID,
		now,
		now.Add(5*time.Minute),
		time.Second,
	); err != nil || !needsRefresh {
		t.Fatalf("crash-window claim = refresh %v, error %v", needsRefresh, err)
	}
	if _, _, err = repository.ClaimSession(
		context.Background(),
		workspaceID,
		connectionID,
		now.Add(2*time.Second),
		now.Add(5*time.Minute),
		time.Second,
	); !errors.Is(err, ErrRefreshOutcomeUnknown) {
		t.Fatalf("expired single-use lease error = %v", err)
	}
}

func TestPostgresSocialAuthorizerEnforcesOwnerAndWorkspaceIsolation(t *testing.T) {
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
	workspaceID := "f05-auth-workspace-" + suffix
	otherWorkspaceID := "f05-auth-other-workspace-" + suffix
	ownerID := "f05-auth-owner-" + suffix
	memberID := "f05-auth-member-" + suffix
	otherOwnerID := "f05-auth-other-owner-" + suffix
	defer func() {
		_, _ = database.ExecContext(
			context.Background(),
			`DELETE FROM f04_workspaces WHERE id IN ($1, $2)`,
			workspaceID,
			otherWorkspaceID,
		)
	}()
	for _, fixture := range []struct {
		workspaceID string
		ownerID     string
	}{
		{workspaceID: workspaceID, ownerID: ownerID},
		{workspaceID: otherWorkspaceID, ownerID: otherOwnerID},
	} {
		if _, err = database.ExecContext(context.Background(), `
			INSERT INTO f04_workspaces (
				id, personal_account_id, name, status, created_at, updated_at
			) VALUES ($1, $2, $3, 'active', $4, $4)`,
			fixture.workspaceID,
			fixture.ownerID,
			"F5 OAuth authorization fixture",
			serviceTestNow,
		); err != nil {
			t.Fatal(err)
		}
		if _, err = database.ExecContext(context.Background(), `
			INSERT INTO f04_memberships (
				workspace_id, account_id, role, status, created_at, updated_at
			) VALUES ($1, $2, 'owner', 'active', $3, $3)`,
			fixture.workspaceID,
			fixture.ownerID,
			serviceTestNow,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = database.ExecContext(context.Background(), `
		INSERT INTO f04_memberships (
			workspace_id, account_id, role, status, created_at, updated_at
		) VALUES ($1, $2, 'member', 'active', $3, $3)`,
		workspaceID,
		memberID,
		serviceTestNow,
	); err != nil {
		t.Fatal(err)
	}

	authorizer := postgresSocialAuthorizer{database: database}
	if err = authorizer.Authorize(
		context.Background(),
		workspaceID,
		ownerID,
		PermissionManageChannels,
	); err != nil {
		t.Fatalf("owner manage authorization: %v", err)
	}
	if err = authorizer.Authorize(
		context.Background(),
		workspaceID,
		memberID,
		PermissionViewWorkspace,
	); err != nil {
		t.Fatalf("member view authorization: %v", err)
	}
	if err = authorizer.Authorize(
		context.Background(),
		workspaceID,
		memberID,
		PermissionManageChannels,
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("member manage authorization error = %v", err)
	}
	if err = authorizer.Authorize(
		context.Background(),
		otherWorkspaceID,
		ownerID,
		PermissionManageChannels,
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-workspace owner authorization error = %v", err)
	}
}

func testPostgresF10ChannelQuota(
	t *testing.T,
	database *sql.DB,
	suffix string,
) {
	t.Helper()
	workspaceID := "f05-quota-workspace-" + suffix
	defer func() {
		for _, statement := range []string{
			`DELETE FROM f10_usage_operations WHERE workspace_id = $1`,
			`DELETE FROM f10_usage_counters WHERE workspace_id = $1`,
			`DELETE FROM f10_workspace_billing WHERE workspace_id = $1`,
		} {
			_, _ = database.ExecContext(
				context.Background(),
				statement,
				workspaceID,
			)
		}
	}()
	var provisioned bool
	if err := database.QueryRowContext(
		context.Background(),
		`SELECT f10_provision_trial($1, $2)`,
		workspaceID,
		serviceTestNow,
	).Scan(&provisioned); err != nil {
		t.Fatal(err)
	}
	if !provisioned {
		t.Fatal("F10 fixture workspace was not provisioned")
	}
	quota, err := NewPostgresChannelQuota(
		database,
		func() time.Time { return serviceTestNow },
	)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 9; index++ {
		decision, reserveErr := quota.ReserveChannel(
			context.Background(),
			workspaceID,
			"f05:quota-reserve:"+strconv.Itoa(index),
		)
		if reserveErr != nil || !decision.Accepted {
			t.Fatalf("reserve %d = %#v, %v", index, decision, reserveErr)
		}
	}
	decision, err := quota.ReserveChannel(
		context.Background(),
		workspaceID,
		"f05:quota-reserve:over-limit",
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Accepted || decision.Retryable {
		t.Fatalf("over-limit decision = %#v", decision)
	}
	decision, err = quota.ReleaseChannel(
		context.Background(),
		workspaceID,
		"f05:quota-release:one",
	)
	if err != nil || !decision.Accepted {
		t.Fatalf("release decision = %#v, %v", decision, err)
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

func postgresConnectLinkedIn(
	t *testing.T,
	service *Service,
	workspaceID, actorID, remoteID string,
) Connection {
	t.Helper()
	authorization, err := service.Begin(context.Background(), BeginRequest{
		WorkspaceID: workspaceID,
		ActorID:     actorID,
		Provider:    ProviderLinkedIn,
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
		Code:  "provider-code",
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
