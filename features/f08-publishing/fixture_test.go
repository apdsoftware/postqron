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
	if len(fixtures) != 3 {
		t.Fatalf("fixture count=%d", len(fixtures))
	}
	var native, reconciliation, notification bool
	for _, fixture := range fixtures {
		if fixture.Provider == "" || fixture.Capabilities.Version == "" {
			t.Fatalf("invalid fixture %#v", fixture)
		}
		native = native || fixture.Capabilities.NativeIdempotency
		reconciliation = reconciliation || fixture.Capabilities.Reconciliation
		notification = notification ||
			fixture.Capabilities.NotificationIdempotency
	}
	if !native || !reconciliation || !notification {
		t.Fatalf(
			"fixture coverage native=%v reconciliation=%v notification=%v",
			native,
			reconciliation,
			notification,
		)
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
