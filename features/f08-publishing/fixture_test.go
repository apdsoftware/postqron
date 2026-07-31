package publishing

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestAdapterCapabilityFixtureCoversSafeExecutionModes(t *testing.T) {
	source, err := os.ReadFile("testdata/adapter-capabilities.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Provider     string              `json:"provider"`
		Capabilities AdapterCapabilities `json:"capabilities"`
	}
	if err := json.Unmarshal(source, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 4 {
		t.Fatalf("fixture count=%d", len(fixtures))
	}
	var native, reconciliation, failClosed, notification bool
	for _, fixture := range fixtures {
		if fixture.Provider == "" || fixture.Capabilities.Version == "" {
			t.Fatalf("invalid fixture %#v", fixture)
		}
		native = native || fixture.Capabilities.NativeIdempotency
		reconciliation = reconciliation || fixture.Capabilities.Reconciliation
		failClosed = failClosed || fixture.Capabilities.AmbiguousFailClosed
		notification = notification ||
			fixture.Capabilities.NotificationIdempotency
	}
	if !native || !reconciliation || !failClosed || !notification {
		t.Fatalf(
			"fixture coverage native=%v reconciliation=%v fail_closed=%v notification=%v",
			native,
			reconciliation,
			failClosed,
			notification,
		)
	}
}

func TestRegistryAcceptsAmbiguousFailClosedAdapter(t *testing.T) {
	registry := NewAdapterRegistry()
	provider := newFakeProvider()
	provider.capabilities.NativeIdempotency = false
	provider.capabilities.Reconciliation = false
	provider.capabilities.AmbiguousFailClosed = true
	if err := registry.RegisterPublisher("fail_closed", provider); err != nil {
		t.Fatalf("fail-closed adapter registration error=%v", err)
	}
}

func TestRegistryRejectsAdapterWithoutReplaySafety(t *testing.T) {
	registry := NewAdapterRegistry()
	provider := newFakeProvider()
	provider.capabilities.NativeIdempotency = false
	provider.capabilities.Reconciliation = false
	if err := registry.RegisterPublisher("unsafe", provider); !errors.Is(err, ErrUnsafeAdapter) {
		t.Fatalf("unsafe adapter registration error=%v", err)
	}
}
