package prelaunch

import "testing"

func TestResolveModeFailClosed(t *testing.T) {
	for _, value := range []string{"", "TRUE", " false ", "0"} {
		mode := ResolveMode(value, "production")
		if !mode.Enabled || mode.Source != ModeFailClosed {
			t.Fatalf("ResolveMode(%q) = %#v, want fail closed", value, mode)
		}
	}
	if mode := ResolveMode("", ""); !mode.Enabled ||
		mode.Source != ModeFailClosed {
		t.Fatalf("missing runtime environment = %#v, want fail closed", mode)
	}
}

func TestResolveModeExplicitAndDevelopment(t *testing.T) {
	tests := []struct {
		value       string
		environment string
		want        Mode
	}{
		{"true", "production", Mode{true, ModeExplicitTrue}},
		{"false", "production", Mode{false, ModeExplicitFalse}},
		{"", "development", Mode{false, ModeNonProductionDefault}},
	}
	for _, test := range tests {
		if got := ResolveMode(test.value, test.environment); got != test.want {
			t.Fatalf("ResolveMode(%q, %q) = %#v, want %#v",
				test.value, test.environment, got, test.want)
		}
	}
}
