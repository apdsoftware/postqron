package smartqueue

import (
	"errors"
	"testing"
	"time"
)

func TestFirstAvailableSlotSkipsNonexistentDSTTimes(t *testing.T) {
	queue := Queue{
		TimeZone: "Europe/Rome", IntervalMinutes: 30,
		Windows: []RecurringWindow{{
			Weekday: Sunday, StartTime: "02:00", EndTime: "04:00",
		}},
	}
	slot, err := firstAvailableSlot(
		queue,
		time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 29, 3, 0, 0, 0, time.UTC),
		map[int64]struct{}{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if slot.LocalDateTime != "2026-03-29T03:00:00" ||
		!slot.StartsAtUTC.Equal(time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC)) ||
		slot.UTCOffsetMinutes != 120 {
		t.Fatalf("slot = %#v", slot)
	}
}

func TestFirstAvailableSlotOrdersAmbiguousDSTInstantsByUTC(t *testing.T) {
	queue := Queue{
		TimeZone: "America/New_York", IntervalMinutes: 30,
		Windows: []RecurringWindow{{
			Weekday: Sunday, StartTime: "01:00", EndTime: "02:00",
		}},
	}
	firstOccurrence := time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)
	slot, err := firstAvailableSlot(
		queue,
		firstOccurrence,
		time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC),
		map[int64]struct{}{firstOccurrence.UnixNano(): {}},
	)
	if err != nil {
		t.Fatal(err)
	}
	// 01:30 EDT (05:30Z) is earlier than the repeated 01:00 EST (06:00Z).
	if slot.LocalDateTime != "2026-11-01T01:30:00" ||
		!slot.StartsAtUTC.Equal(time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)) ||
		slot.UTCOffsetMinutes != -240 {
		t.Fatalf("slot = %#v", slot)
	}
}

func TestFirstAvailableSlotHonorsSearchLimit(t *testing.T) {
	queue := Queue{
		TimeZone: "UTC", IntervalMinutes: 30,
		Windows: []RecurringWindow{{
			Weekday: Tuesday, StartTime: "10:00", EndTime: "11:00",
		}},
	}
	_, err := firstAvailableSlot(
		queue,
		time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 27, 23, 59, 0, 0, time.UTC),
		map[int64]struct{}{},
	)
	if !errors.Is(err, ErrNoSlotAvailable) {
		t.Fatalf("error = %v", err)
	}
}

func TestQueueDefinitionRequiresIANAZoneAndNonOverlappingWindows(t *testing.T) {
	_, _, _, err := normalizeQueueDefinition(
		"Editorial", "Local", 30, 30,
		[]RecurringWindow{{Weekday: Monday, StartTime: "09:00", EndTime: "10:00"}},
	)
	var fieldError *FieldError
	if !errors.As(err, &fieldError) || fieldError.Code != "time_zone_invalid" {
		t.Fatalf("error = %#v", err)
	}
	_, _, _, err = normalizeQueueDefinition(
		"Editorial", "Europe/Rome", 30, 30,
		[]RecurringWindow{
			{Weekday: Monday, StartTime: "09:00", EndTime: "11:00"},
			{Weekday: Monday, StartTime: "10:30", EndTime: "12:00"},
		},
	)
	if !errors.As(err, &fieldError) || fieldError.Code != "windows_overlap" {
		t.Fatalf("error = %#v", err)
	}
}
