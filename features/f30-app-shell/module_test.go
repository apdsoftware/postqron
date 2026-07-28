package appshell

import (
	"os"
	"testing"
)

func TestNormalizeOriginsRestrictsCredentialedAppRequests(t *testing.T) {
	origins, err := normalizeOrigins(
		"https://postqron.com, https://postqron.com/,http://localhost:3000",
	)
	if err != nil {
		t.Fatalf("normalizeOrigins() error = %v", err)
	}
	if len(origins) != 2 ||
		origins[0] != "http://localhost:3000" ||
		origins[1] != "https://postqron.com" {
		t.Fatalf("origins = %#v", origins)
	}
	for _, invalid := range []string{
		"*",
		"https://user:password@postqron.com",
		"https://postqron.com/path",
		"javascript:alert(1)",
	} {
		if _, err := normalizeOrigin(invalid); err == nil {
			t.Fatalf("normalizeOrigin(%q) succeeded", invalid)
		}
	}
}

func TestConfiguredProvidersRequireValidSharedEncryptionKey(t *testing.T) {
	t.Setenv("POSTQRON_AUTH_GOOGLE_CLIENT_ID", "google-client")
	t.Setenv("POSTQRON_AUTH_GOOGLE_CLIENT_SECRET", "google-secret")
	t.Setenv("POSTQRON_AUTH_GOOGLE_REDIRECT_URL", "https://app.postqron.test/api/v1/auth/callback")

	if providers := configuredProviders(); len(providers) != 0 {
		t.Fatalf("providers without encryption key = %#v, want empty", providers)
	}

	t.Setenv("POSTQRON_AUTH_ENCRYPTION_KEY_B64", "not-base64")
	if providers := configuredProviders(); len(providers) != 0 {
		t.Fatalf("providers with invalid encryption key = %#v, want empty", providers)
	}

	t.Setenv("POSTQRON_AUTH_ENCRYPTION_KEY_B64", "c2hvcnQ=")
	if providers := configuredProviders(); len(providers) != 0 {
		t.Fatalf("providers with short encryption key = %#v, want empty", providers)
	}
}

func TestConfiguredProvidersExcludeInvalidRedirectsWithoutHidingOtherProviders(t *testing.T) {
	t.Setenv("POSTQRON_AUTH_ENCRYPTION_KEY_B64", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv("POSTQRON_AUTH_GOOGLE_CLIENT_ID", "google-client")
	t.Setenv("POSTQRON_AUTH_GOOGLE_CLIENT_SECRET", "google-secret")
	t.Setenv("POSTQRON_AUTH_GOOGLE_REDIRECT_URL", "http://app.postqron.test/api/v1/auth/callback")
	t.Setenv("POSTQRON_AUTH_FACEBOOK_CLIENT_ID", "facebook-client")
	t.Setenv("POSTQRON_AUTH_FACEBOOK_CLIENT_SECRET", "facebook-secret")
	t.Setenv("POSTQRON_AUTH_FACEBOOK_REDIRECT_URL", "https://app.postqron.test/api/v1/auth/callback")
	t.Setenv("POSTQRON_AUTH_FACEBOOK_GRAPH_VERSION", "v22.0")

	providers := configuredProviders()
	if len(providers) != 1 || providers[0] != "facebook" {
		t.Fatalf("providers = %#v, want only facebook", providers)
	}
}

func TestConfiguredProvidersValidateProviderSpecificInputsIndependently(t *testing.T) {
	t.Setenv("POSTQRON_AUTH_ENCRYPTION_KEY_B64", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv("POSTQRON_AUTH_GOOGLE_CLIENT_ID", "google-client")
	t.Setenv("POSTQRON_AUTH_GOOGLE_CLIENT_SECRET", "google-secret")
	t.Setenv("POSTQRON_AUTH_GOOGLE_REDIRECT_URL", "https://app.postqron.test/api/v1/auth/callback")
	t.Setenv("POSTQRON_AUTH_FACEBOOK_CLIENT_ID", "facebook-client")
	t.Setenv("POSTQRON_AUTH_FACEBOOK_CLIENT_SECRET", "facebook-secret")
	t.Setenv("POSTQRON_AUTH_FACEBOOK_REDIRECT_URL", "https://app.postqron.test/api/v1/auth/callback")
	t.Setenv("POSTQRON_AUTH_FACEBOOK_GRAPH_VERSION", "")
	t.Setenv("POSTQRON_AUTH_LINKEDIN_CLIENT_ID", "linkedin-client")
	t.Setenv("POSTQRON_AUTH_LINKEDIN_CLIENT_SECRET", "linkedin-secret")
	t.Setenv("POSTQRON_AUTH_LINKEDIN_REDIRECT_URL", "https://app.postqron.test/linkedin/callback")

	providers := configuredProviders()
	if len(providers) != 2 || providers[0] != "google" || providers[1] != "linkedin" {
		t.Fatalf("providers = %#v, want google and linkedin", providers)
	}
}

func TestValidHTTPSAbsoluteURL(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{value: "https://app.postqron.test/api/v1/auth/callback", want: true},
		{value: "https://app.postqron.test", want: false},
		{value: "http://app.postqron.test/api/v1/auth/callback", want: false},
		{value: "https://user:pass@app.postqron.test/api/v1/auth/callback", want: false},
	} {
		if got := validHTTPSAbsoluteURL(tc.value); got != tc.want {
			t.Fatalf("validHTTPSAbsoluteURL(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
