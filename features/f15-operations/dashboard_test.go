package operations

import (
	"testing"
	"time"
)

func TestProjectServiceStatus(t *testing.T) {
	now := time.Unix(1_750_000_000, 0).UTC()
	tests := []struct {
		name   string
		signal ServiceSignal
		want   ServiceStatus
	}{
		{
			name:   "absent signal is unknown",
			signal: ServiceSignal{},
			want:   StatusUnknown,
		},
		{
			name: "present but zero checked_at is unknown",
			signal: ServiceSignal{
				Present:   true,
				Reachable: true,
			},
			want: StatusUnknown,
		},
		{
			name: "stale signal is unknown even when reachable",
			signal: ServiceSignal{
				Present:   true,
				Reachable: true,
				CheckedAt: now.Add(-time.Minute),
			},
			want: StatusUnknown,
		},
		{
			name: "unreachable is outage",
			signal: ServiceSignal{
				Present:   true,
				Reachable: false,
				CheckedAt: now,
			},
			want: StatusOutage,
		},
		{
			name: "critical breach is outage even when reachable",
			signal: ServiceSignal{
				Present:   true,
				Reachable: true,
				Critical:  true,
				CheckedAt: now,
			},
			want: StatusOutage,
		},
		{
			name: "warning without critical is degraded",
			signal: ServiceSignal{
				Present:   true,
				Reachable: true,
				Warning:   true,
				CheckedAt: now,
			},
			want: StatusDegraded,
		},
		{
			name: "reachable without warning or critical is operational",
			signal: ServiceSignal{
				Present:   true,
				Reachable: true,
				CheckedAt: now,
			},
			want: StatusOperational,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ProjectServiceStatus(test.signal, now, 30*time.Second)
			if got != test.want {
				t.Fatalf("ProjectServiceStatus() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestProjectServiceStatusZeroFreshnessDisablesStaleness(t *testing.T) {
	now := time.Unix(1_750_000_000, 0).UTC()
	got := ProjectServiceStatus(ServiceSignal{
		Present:   true,
		Reachable: true,
		CheckedAt: now.Add(-24 * time.Hour),
	}, now, 0)
	if got != StatusOperational {
		t.Fatalf("ProjectServiceStatus() = %q, want %q", got, StatusOperational)
	}
}
