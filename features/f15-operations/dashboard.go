package operations

import "time"

// ServiceStatus is the vocabulary admin-facing dashboards use to describe an
// operational dependency. It intentionally has no state that could be
// confused with "operational" when no real signal was ever collected.
type ServiceStatus string

const (
	StatusOperational ServiceStatus = "operational"
	StatusDegraded    ServiceStatus = "degraded"
	StatusOutage      ServiceStatus = "outage"
	StatusUnknown     ServiceStatus = "unknown"
)

// ServiceSignal is the minimal, real observation collected for one
// operational dependency. Present must be false whenever no observation was
// actually made; a zero-value ServiceSignal must never be mistaken for a
// healthy one.
type ServiceSignal struct {
	Present   bool
	Reachable bool
	Warning   bool
	Critical  bool
	CheckedAt time.Time
}

// ProjectServiceStatus turns a raw signal into the admin dashboard
// vocabulary. It reports unknown whenever the signal was never collected or
// has expired past freshness, and never reports operational for a
// dependency that is unreachable or breaching a critical threshold.
func ProjectServiceStatus(
	signal ServiceSignal,
	now time.Time,
	freshness time.Duration,
) ServiceStatus {
	if !signal.Present || signal.CheckedAt.IsZero() {
		return StatusUnknown
	}
	if freshness > 0 && now.Sub(signal.CheckedAt) > freshness {
		return StatusUnknown
	}
	switch {
	case !signal.Reachable || signal.Critical:
		return StatusOutage
	case signal.Warning:
		return StatusDegraded
	default:
		return StatusOperational
	}
}
