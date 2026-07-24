package scheduling

import (
	"errors"
	"testing"
)

func TestResolveScheduleRejectsNonexistentDSTTime(t *testing.T) {
	_, err := resolveSchedule(ScheduleInput{
		LocalDateTime: "2026-03-29T02:30:00",
		TimeZone:      "Europe/Rome",
	})
	assertFieldCode(t, err, "local_time_nonexistent")
}

func TestResolveScheduleRequiresOffsetForAmbiguousDSTTime(t *testing.T) {
	input := ScheduleInput{
		LocalDateTime: "2026-10-25T02:30:00",
		TimeZone:      "Europe/Rome",
	}
	_, err := resolveSchedule(input)
	assertFieldCode(t, err, "local_time_ambiguous")

	summerOffset := 120
	input.OffsetMinutes = &summerOffset
	first, err := resolveSchedule(input)
	if err != nil {
		t.Fatal(err)
	}
	winterOffset := 60
	input.OffsetMinutes = &winterOffset
	second, err := resolveSchedule(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.utc.Equal(second.utc) || second.utc.Sub(first.utc).Hours() != 1 {
		t.Fatalf("ambiguous instants = %s and %s", first.utc, second.utc)
	}
	if first.local != second.local ||
		first.offsetMinutes != summerOffset ||
		second.offsetMinutes != winterOffset {
		t.Fatalf("resolved schedules = %#v and %#v", first, second)
	}
}

func TestResolveScheduleRejectsMismatchedOffsetAndLocalZone(t *testing.T) {
	offset := 0
	_, err := resolveSchedule(ScheduleInput{
		LocalDateTime: "2026-07-25T12:00:00",
		TimeZone:      "America/New_York",
		OffsetMinutes: &offset,
	})
	assertFieldCode(t, err, "utc_offset_mismatch")
}

func assertFieldCode(t *testing.T, err error, code string) {
	t.Helper()
	var fieldError *FieldError
	if !errors.As(err, &fieldError) || fieldError.Code != code {
		t.Fatalf("error = %#v, want field code %q", err, code)
	}
}
