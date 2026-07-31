package socialconnections

import (
	"encoding/base64"
	"testing"
)

func TestThreadsRuntimeRequiresCredentialsReviewAndAuditIndependently(
	t *testing.T,
) {
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	base := map[string]string{
		configEnabled:         "true",
		configCipherKeyID:     "threads-fixture-key",
		configCipherKey:       key,
		configThreadsEnabled:  "true",
		configThreadsID:       "threads-client",
		configThreadsSecret:   "fixture-threads-secret",
		configThreadsRedirect: "https://app.example.test/social/callback",
	}

	module := runtimeModuleFixture()
	missingCredentials := cloneThreadsRuntimeValues(base)
	delete(missingCredentials, configThreadsSecret)
	if err := module.Configure(missingCredentials); err != nil {
		t.Fatal(err)
	}
	assertThreadsRuntimeState(t, module, ProviderNotConfigured, false)

	module = runtimeModuleFixture()
	if err := module.Configure(base); err != nil {
		t.Fatal(err)
	}
	assertThreadsRuntimeState(t, module, ProviderReviewRequired, false)

	reviewed := cloneThreadsRuntimeValues(base)
	reviewed[configThreadsReviewed] = "true"
	module = runtimeModuleFixture()
	if err := module.Configure(reviewed); err != nil {
		t.Fatal(err)
	}
	assertThreadsRuntimeState(t, module, ProviderAuditRequired, false)

	audited := cloneThreadsRuntimeValues(reviewed)
	audited[configThreadsAudited] = "true"
	module = runtimeModuleFixture()
	if err := module.Configure(audited); err != nil {
		t.Fatal(err)
	}
	assertThreadsRuntimeState(t, module, ProviderReady, true)
	entry := threadsRuntimeCatalogEntry(t, module)
	if entry.Capabilities != (AdapterCapabilities{
		Authorization:     true,
		ResourceSelection: true,
		TokenRefresh:      true,
	}) {
		t.Fatalf("Threads runtime capabilities = %#v", entry.Capabilities)
	}
}

func TestThreadsRuntimeFeatureFlagRequiresExactTrue(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	module := runtimeModuleFixture()
	if err := module.Configure(map[string]string{
		configEnabled:         "true",
		configCipherKeyID:     "threads-fixture-key",
		configCipherKey:       key,
		configThreadsEnabled:  "TRUE",
		configThreadsID:       "threads-client",
		configThreadsSecret:   "fixture-threads-secret",
		configThreadsRedirect: "https://app.example.test/social/callback",
		configThreadsReviewed: "true",
		configThreadsAudited:  "true",
	}); err != nil {
		t.Fatal(err)
	}
	assertThreadsRuntimeState(t, module, ProviderNotConfigured, false)
}

func assertThreadsRuntimeState(
	t *testing.T,
	module *Module,
	state ProviderConfigurationState,
	available bool,
) {
	t.Helper()
	entry := threadsRuntimeCatalogEntry(t, module)
	expectedStatus := ProviderUnavailable
	if available {
		expectedStatus = ProviderAvailable
	}
	if entry.Status != expectedStatus ||
		entry.ConfigurationState != state ||
		entry.Retryable {
		t.Fatalf("Threads runtime entry = %#v", entry)
	}
}

func threadsRuntimeCatalogEntry(
	t *testing.T,
	module *Module,
) ProviderCatalogEntry {
	t.Helper()
	for _, entry := range module.service.Bootstrap().Catalog {
		if entry.Provider == ProviderThreads {
			return entry
		}
	}
	t.Fatal("Threads catalog entry is missing")
	return ProviderCatalogEntry{}
}

func cloneThreadsRuntimeValues(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
