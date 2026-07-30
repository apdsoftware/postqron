package publishingruntime

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestProductionMetaBootstrapRegistersReviewedFacebookExecutor(t *testing.T) {
	clearMetaBootstrapEnvironment(t)
	t.Setenv("POSTQRON_F08_META_AUTO_ENABLED", "true")
	t.Setenv("POSTQRON_F08_FACEBOOK_PAGES_ENABLED", "true")
	t.Setenv("POSTQRON_F05_ENABLED", "true")
	t.Setenv("POSTQRON_F05_META_ENABLED", "true")
	t.Setenv("POSTQRON_F05_META_GRAPH_VERSION", "v25.0")
	t.Setenv("POSTQRON_F05_CIPHER_KEY_ID", "worker-meta-test")
	t.Setenv(
		"POSTQRON_F05_CIPHER_KEY_BASE64",
		base64.StdEncoding.EncodeToString(
			[]byte("0123456789abcdef0123456789abcdef"),
		),
	)
	t.Setenv("POSTQRON_F05_FACEBOOK_CLIENT_ID", "facebook-client")
	t.Setenv("POSTQRON_F05_FACEBOOK_CLIENT_SECRET", "facebook-secret")
	t.Setenv(
		"POSTQRON_F05_FACEBOOK_REDIRECT_URL",
		"https://api.example.test/api/v1/social-authorizations/callback",
	)
	t.Setenv("POSTQRON_F05_FACEBOOK_LOGIN_CONFIG_ID", "login-config")
	t.Setenv("POSTQRON_F05_FACEBOOK_APP_REVIEW_APPROVED", "true")
	t.Setenv("POSTQRON_F05_FACEBOOK_RUNTIME_AUDIT_VERIFIED", "true")

	database, err := sql.Open(
		"pgx",
		"postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	config, err := NewMetaRegistrationConfig(database, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if config.Executor == nil || config.GraphVersion != "v25.0" ||
		len(config.AutoProviders) != 1 ||
		config.AutoProviders[0] != socialconnections.ProviderFacebookPages {
		t.Fatalf("config=%+v", config)
	}
	registry, err := newRuntimeAdapterRegistry(config)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := registry.ResolvePublisher(
		context.Background(),
		string(socialconnections.ProviderFacebookPages),
	)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := publisher.Capabilities()
	if capabilities.Reconciliation || !capabilities.FailClosedOnAmbiguous ||
		capabilities.MediaFormats == "" {
		t.Fatalf("capabilities=%+v", capabilities)
	}
	if _, err = registry.ResolvePublisher(
		context.Background(),
		string(socialconnections.ProviderInstagramProfessional),
	); !errors.Is(err, publishing.ErrProviderUnavailable) {
		t.Fatalf("unconfigured Instagram resolution=%v", err)
	}
}

func TestProductionMetaBootstrapGatesMissingDependenciesAndIssue343(
	t *testing.T,
) {
	clearMetaBootstrapEnvironment(t)
	database, err := sql.Open(
		"pgx",
		"postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	t.Setenv("POSTQRON_F08_META_AUTO_ENABLED", "true")
	if _, err = NewMetaRegistrationConfig(database, time.Now); err == nil ||
		!strings.Contains(err.Error(), "F5 credential and Meta gates") {
		t.Fatalf("missing F5 gate error=%v", err)
	}

	clearMetaBootstrapEnvironment(t)
	t.Setenv("POSTQRON_F08_META_NOTIFICATIONS_ENABLED", "true")
	if _, err = NewMetaRegistrationConfig(database, time.Now); err == nil ||
		!strings.Contains(err.Error(), "issue 343") {
		t.Fatalf("notification gate error=%v", err)
	}
}

func clearMetaBootstrapEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"POSTQRON_F08_META_AUTO_ENABLED",
		"POSTQRON_F08_FACEBOOK_PAGES_ENABLED",
		"POSTQRON_F08_INSTAGRAM_PROFESSIONAL_ENABLED",
		"POSTQRON_F08_THREADS_ENABLED",
		"POSTQRON_F08_META_NOTIFICATIONS_ENABLED",
		"POSTQRON_F05_ENABLED",
		"POSTQRON_F05_META_ENABLED",
		"POSTQRON_F05_META_GRAPH_VERSION",
		"POSTQRON_F05_CIPHER_KEY_ID",
		"POSTQRON_F05_CIPHER_KEY_BASE64",
		"POSTQRON_F05_FACEBOOK_CLIENT_ID",
		"POSTQRON_F05_FACEBOOK_CLIENT_SECRET",
		"POSTQRON_F05_FACEBOOK_REDIRECT_URL",
		"POSTQRON_F05_FACEBOOK_LOGIN_CONFIG_ID",
		"POSTQRON_F05_FACEBOOK_APP_REVIEW_APPROVED",
		"POSTQRON_F05_FACEBOOK_RUNTIME_AUDIT_VERIFIED",
		"POSTQRON_F05_INSTAGRAM_CLIENT_ID",
		"POSTQRON_F05_INSTAGRAM_CLIENT_SECRET",
		"POSTQRON_F05_INSTAGRAM_REDIRECT_URL",
		"POSTQRON_F05_INSTAGRAM_APP_REVIEW_APPROVED",
		"POSTQRON_F05_INSTAGRAM_RUNTIME_AUDIT_VERIFIED",
		"POSTQRON_F08_THREADS_GRAPH_VERSION",
		"POSTQRON_F08_THREADS_CLIENT_ID",
		"POSTQRON_F08_THREADS_REDIRECT_URL",
		"POSTQRON_F08_THREADS_APP_REVIEW_APPROVED",
		"POSTQRON_F08_THREADS_RUNTIME_AUDIT_VERIFIED",
	} {
		t.Setenv(key, "")
	}
}
