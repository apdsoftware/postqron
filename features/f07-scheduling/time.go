package scheduling

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const localDateTimeLayout = "2006-01-02T15:04:05"

type resolvedSchedule struct {
	utc           time.Time
	local         string
	timeZone      string
	offsetMinutes int
}

func resolveSchedule(input ScheduleInput) (resolvedSchedule, error) {
	localValue := strings.TrimSpace(input.LocalDateTime)
	parsed, err := time.ParseInLocation(localDateTimeLayout, localValue, time.UTC)
	if err != nil {
		parsed, err = time.ParseInLocation("2006-01-02T15:04", localValue, time.UTC)
	}
	if err != nil {
		return resolvedSchedule{}, invalidField(
			"scheduled_at.local_date_time",
			"local_date_time",
			"local_date_time_invalid",
			"Local date and time must use YYYY-MM-DDTHH:MM[:SS].",
		)
	}

	zoneName := strings.TrimSpace(input.TimeZone)
	if zoneName == "" || zoneName == "Local" {
		return resolvedSchedule{}, invalidField(
			"scheduled_at.time_zone",
			"iana_time_zone",
			"time_zone_invalid",
			"An explicit IANA time zone is required.",
		)
	}
	location, err := time.LoadLocation(zoneName)
	if err != nil {
		return resolvedSchedule{}, invalidField(
			"scheduled_at.time_zone",
			"iana_time_zone",
			"time_zone_invalid",
			"Time zone must be a valid IANA identifier.",
		)
	}

	candidates := localCandidates(parsed, location)
	if len(candidates) == 0 {
		return resolvedSchedule{}, invalidField(
			"scheduled_at.local_date_time",
			"existing_local_time",
			"local_time_nonexistent",
			"Local time does not exist because of a daylight-saving transition.",
		)
	}
	if input.OffsetMinutes == nil && len(candidates) > 1 {
		return resolvedSchedule{}, invalidField(
			"scheduled_at.utc_offset_minutes",
			"required_for_ambiguous_time",
			"local_time_ambiguous",
			"UTC offset is required because this local time occurs twice.",
		)
	}

	selected := candidates[0]
	if input.OffsetMinutes != nil {
		found := false
		for _, candidate := range candidates {
			if candidate.offsetMinutes == *input.OffsetMinutes {
				selected = candidate
				found = true
				break
			}
		}
		if !found {
			return resolvedSchedule{}, invalidField(
				"scheduled_at.utc_offset_minutes",
				"matches_time_zone",
				"utc_offset_mismatch",
				"UTC offset does not match the selected IANA time zone at that local time.",
			)
		}
	}

	return resolvedSchedule{
		utc:           selected.utc.UTC(),
		local:         parsed.Format(localDateTimeLayout),
		timeZone:      zoneName,
		offsetMinutes: selected.offsetMinutes,
	}, nil
}

type localCandidate struct {
	utc           time.Time
	offsetMinutes int
}

func localCandidates(wall time.Time, location *time.Location) []localCandidate {
	// Collect every offset active near the wall time, including non-hour DST
	// transitions. Each candidate is then verified by a round trip.
	offsets := make(map[int]struct{})
	for instant := wall.Add(-36 * time.Hour); !instant.After(wall.Add(36 * time.Hour)); instant = instant.Add(30 * time.Minute) {
		_, offsetSeconds := instant.In(location).Zone()
		offsets[offsetSeconds] = struct{}{}
	}

	candidates := make([]localCandidate, 0, len(offsets))
	for offsetSeconds := range offsets {
		utc := wall.Add(-time.Duration(offsetSeconds) * time.Second).UTC()
		roundTrip := utc.In(location)
		_, actualOffset := roundTrip.Zone()
		if actualOffset != offsetSeconds || !sameWallTime(wall, roundTrip) {
			continue
		}
		candidates = append(candidates, localCandidate{
			utc:           utc,
			offsetMinutes: offsetSeconds / 60,
		})
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].utc.Before(candidates[right].utc)
	})
	return candidates
}

func sameWallTime(left, right time.Time) bool {
	return left.Year() == right.Year() &&
		left.Month() == right.Month() &&
		left.Day() == right.Day() &&
		left.Hour() == right.Hour() &&
		left.Minute() == right.Minute() &&
		left.Second() == right.Second()
}

func requireFuture(schedule resolvedSchedule, now time.Time) error {
	if !schedule.utc.After(now.UTC()) {
		return invalidField(
			"scheduled_at",
			"future",
			"scheduled_time_not_future",
			fmt.Sprintf("Scheduled instant must be after %s.", now.UTC().Format(time.RFC3339)),
		)
	}
	return nil
}
