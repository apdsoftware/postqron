package smartqueue

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	localDateTimeLayout = "2006-01-02T15:04:05"
	localTimeLayout     = "15:04"
	maxWindows          = 64
)

type normalizedWindow struct {
	weekday time.Weekday
	start   int
	end     int
	source  RecurringWindow
}

func normalizeQueueDefinition(
	name, zone string,
	intervalMinutes, horizonDays int,
	windows []RecurringWindow,
) (string, string, []RecurringWindow, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return "", "", nil, invalidField(
			"name", "required_max_120", "name_invalid",
			"Queue name must contain between 1 and 120 characters.",
		)
	}
	zone = strings.TrimSpace(zone)
	if zone == "" || zone == "Local" {
		return "", "", nil, invalidField(
			"time_zone", "iana_time_zone", "time_zone_invalid",
			"An explicit IANA time zone is required.",
		)
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return "", "", nil, invalidField(
			"time_zone", "iana_time_zone", "time_zone_invalid",
			"Time zone must be a valid IANA identifier.",
		)
	}
	if intervalMinutes < 5 || intervalMinutes > 1440 {
		return "", "", nil, invalidField(
			"interval_minutes", "between_5_and_1440", "interval_invalid",
			"Interval must be between 5 and 1440 minutes.",
		)
	}
	if horizonDays < 1 || horizonDays > 366 {
		return "", "", nil, invalidField(
			"horizon_days", "between_1_and_366", "horizon_invalid",
			"Horizon must be between 1 and 366 days.",
		)
	}
	normalized, err := normalizeWindows(windows)
	if err != nil {
		return "", "", nil, err
	}
	result := make([]RecurringWindow, len(normalized))
	for index := range normalized {
		result[index] = normalized[index].source
	}
	return name, zone, result, nil
}

func normalizeWindows(windows []RecurringWindow) ([]normalizedWindow, error) {
	if len(windows) == 0 || len(windows) > maxWindows {
		return nil, invalidField(
			"windows", "between_1_and_64", "windows_invalid",
			"Queue must contain between 1 and 64 recurring windows.",
		)
	}
	result := make([]normalizedWindow, 0, len(windows))
	for index, window := range windows {
		weekday, ok := parseWeekday(window.Weekday)
		if !ok {
			return nil, invalidField(
				fmt.Sprintf("windows[%d].weekday", index),
				"weekday", "weekday_invalid",
				"Weekday must be a lowercase English weekday name.",
			)
		}
		start, canonicalStart, err := parseMinute(window.StartTime)
		if err != nil {
			return nil, invalidField(
				fmt.Sprintf("windows[%d].start_time", index),
				"hh_mm", "start_time_invalid", "Start time must use HH:MM.",
			)
		}
		end, canonicalEnd, err := parseMinute(window.EndTime)
		if err != nil || end <= start {
			return nil, invalidField(
				fmt.Sprintf("windows[%d].end_time", index),
				"hh_mm_after_start", "end_time_invalid",
				"End time must use HH:MM and be after start time on the same day.",
			)
		}
		result = append(result, normalizedWindow{
			weekday: weekday,
			start:   start,
			end:     end,
			source: RecurringWindow{
				Weekday: window.Weekday, StartTime: canonicalStart, EndTime: canonicalEnd,
			},
		})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].weekday != result[right].weekday {
			return mondayIndex(result[left].weekday) < mondayIndex(result[right].weekday)
		}
		if result[left].start != result[right].start {
			return result[left].start < result[right].start
		}
		return result[left].end < result[right].end
	})
	for index := 1; index < len(result); index++ {
		previous, current := result[index-1], result[index]
		if previous.weekday == current.weekday && current.start < previous.end {
			return nil, invalidField(
				"windows", "non_overlapping", "windows_overlap",
				"Recurring windows on the same weekday must not overlap.",
			)
		}
	}
	return result, nil
}

func firstAvailableSlot(
	queue Queue,
	notBefore, until time.Time,
	occupied map[int64]struct{},
) (Slot, error) {
	location, err := time.LoadLocation(queue.TimeZone)
	if err != nil {
		return Slot{}, fmt.Errorf("%w: stored queue time zone: %v", ErrInvalidArgument, err)
	}
	windows, err := normalizeWindows(queue.Windows)
	if err != nil {
		return Slot{}, err
	}
	notBefore, until = notBefore.UTC(), until.UTC()
	if until.Before(notBefore) {
		return Slot{}, ErrNoSlotAvailable
	}
	firstLocal := notBefore.In(location)
	lastLocal := until.In(location)
	firstDate := time.Date(firstLocal.Year(), firstLocal.Month(), firstLocal.Day(), 0, 0, 0, 0, time.UTC)
	lastDate := time.Date(lastLocal.Year(), lastLocal.Month(), lastLocal.Day(), 0, 0, 0, 0, time.UTC)

	slots := make([]Slot, 0)
	for date := firstDate; !date.After(lastDate); date = date.AddDate(0, 0, 1) {
		for _, window := range windows {
			if date.Weekday() != window.weekday {
				continue
			}
			for minute := window.start; minute < window.end; minute += queue.IntervalMinutes {
				wall := time.Date(
					date.Year(), date.Month(), date.Day(),
					minute/60, minute%60, 0, 0, time.UTC,
				)
				for _, candidate := range localCandidates(wall, location) {
					if candidate.utc.Before(notBefore) || candidate.utc.After(until) {
						continue
					}
					if _, busy := occupied[candidate.utc.UnixNano()]; busy {
						continue
					}
					slots = append(slots, Slot{
						StartsAtUTC:      candidate.utc,
						LocalDateTime:    wall.Format(localDateTimeLayout),
						TimeZone:         queue.TimeZone,
						UTCOffsetMinutes: candidate.offsetMinutes,
					})
				}
			}
		}
	}
	sort.Slice(slots, func(left, right int) bool {
		if !slots[left].StartsAtUTC.Equal(slots[right].StartsAtUTC) {
			return slots[left].StartsAtUTC.Before(slots[right].StartsAtUTC)
		}
		if slots[left].LocalDateTime != slots[right].LocalDateTime {
			return slots[left].LocalDateTime < slots[right].LocalDateTime
		}
		return slots[left].UTCOffsetMinutes > slots[right].UTCOffsetMinutes
	})
	if len(slots) > 0 {
		return slots[0], nil
	}
	return Slot{}, ErrNoSlotAvailable
}

type localCandidate struct {
	utc           time.Time
	offsetMinutes int
}

func localCandidates(wall time.Time, location *time.Location) []localCandidate {
	offsets := make(map[int]struct{})
	for instant := wall.Add(-36 * time.Hour); !instant.After(wall.Add(36 * time.Hour)); instant = instant.Add(30 * time.Minute) {
		_, seconds := instant.In(location).Zone()
		offsets[seconds] = struct{}{}
	}
	candidates := make([]localCandidate, 0, len(offsets))
	for seconds := range offsets {
		utc := wall.Add(-time.Duration(seconds) * time.Second).UTC()
		roundTrip := utc.In(location)
		_, actual := roundTrip.Zone()
		if actual == seconds && sameWallTime(wall, roundTrip) {
			candidates = append(candidates, localCandidate{
				utc: utc, offsetMinutes: seconds / 60,
			})
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].utc.Before(candidates[right].utc)
	})
	return candidates
}

func sameWallTime(left, right time.Time) bool {
	return left.Year() == right.Year() && left.Month() == right.Month() &&
		left.Day() == right.Day() && left.Hour() == right.Hour() &&
		left.Minute() == right.Minute() && left.Second() == right.Second()
}

func parseWeekday(value Weekday) (time.Weekday, bool) {
	switch value {
	case Monday:
		return time.Monday, true
	case Tuesday:
		return time.Tuesday, true
	case Wednesday:
		return time.Wednesday, true
	case Thursday:
		return time.Thursday, true
	case Friday:
		return time.Friday, true
	case Saturday:
		return time.Saturday, true
	case Sunday:
		return time.Sunday, true
	default:
		return 0, false
	}
}

func mondayIndex(weekday time.Weekday) int {
	return (int(weekday) + 6) % 7
}

func parseMinute(value string) (int, string, error) {
	parsed, err := time.Parse(localTimeLayout, strings.TrimSpace(value))
	if err != nil {
		return 0, "", err
	}
	return parsed.Hour()*60 + parsed.Minute(), parsed.Format(localTimeLayout), nil
}
