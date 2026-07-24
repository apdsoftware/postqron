package analytics

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

type RetryPolicy struct {
	BaseDelay       time.Duration
	MaxDelay        time.Duration
	Lease           time.Duration
	RefreshInterval time.Duration
	MaxAttempts     int
}

func (policy RetryPolicy) Validate() error {
	if policy.BaseDelay <= 0 ||
		policy.MaxDelay < policy.BaseDelay ||
		policy.Lease <= 0 ||
		policy.RefreshInterval <= 0 ||
		policy.MaxAttempts < 1 {
		return fmt.Errorf("%w: invalid retry policy", ErrInvalidArgument)
	}
	return nil
}

// Delay applies exponential backoff but never schedules before Retry-After.
// Provider delays are intentionally not capped.
func (policy RetryPolicy) Delay(attempt int, retryAfter time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Duration(float64(policy.BaseDelay) * math.Pow(2, float64(attempt-1)))
	if delay < 0 || delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	if retryAfter > delay {
		return retryAfter
	}
	return delay
}

type ProviderError struct {
	Code       string
	Detail     string
	Retryable  bool
	RetryAfter time.Duration
}

func (err *ProviderError) Error() string {
	if err == nil {
		return ""
	}
	if strings.TrimSpace(err.Detail) == "" {
		return err.Code
	}
	return err.Code + ": " + err.Detail
}

var safeCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

func safeErrorCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !safeCodePattern.MatchString(value) {
		return "provider_error"
	}
	return value
}

func metricsFor(channelType ChannelType) ([]MetricName, bool) {
	switch channelType {
	case ChannelFacebookPage:
		return []MetricName{
			MetricReach,
			MetricLikes,
			MetricComments,
			MetricShares,
			MetricViews,
		}, true
	case ChannelInstagramProfessional:
		return []MetricName{
			MetricReach,
			MetricLikes,
			MetricComments,
			MetricShares,
			MetricSaved,
			MetricViews,
			MetricPlays,
		}, true
	default:
		return nil, false
	}
}

func requiredPermission(channelType ChannelType) (string, bool) {
	switch channelType {
	case ChannelFacebookPage:
		return "read_insights", true
	case ChannelInstagramProfessional:
		return "instagram_business_manage_insights", true
	default:
		return "", false
	}
}
