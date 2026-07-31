package socialconnections

import "testing"

func TestRuntimeProviderValuePrefersInjectedConfiguration(t *testing.T) {
	t.Setenv("POSTQRON_F05_FIXTURE_VALUE", "environment")
	if value := runtimeProviderValue(
		map[string]string{"social.fixture": "injected"},
		"social.fixture",
		"POSTQRON_F05_FIXTURE_VALUE",
	); value != "injected" {
		t.Fatalf("runtime provider value = %q", value)
	}
	if value := runtimeProviderValue(
		map[string]string{},
		"social.fixture",
		"POSTQRON_F05_FIXTURE_VALUE",
	); value != "environment" {
		t.Fatalf("runtime provider environment value = %q", value)
	}
}
