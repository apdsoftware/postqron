package video

import (
	"encoding/json"
	"net/http"
	"strings"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
)

type TikTokResponseClassifier struct{}

func (TikTokResponseClassifier) ClassifyProviderResponse(
	evidence socialconnections.ProviderResponseEvidence,
) (socialconnections.ProviderResponseClassification, bool) {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(evidence.Body, &envelope) != nil {
		return socialconnections.ProviderResponseClassification{}, false
	}
	code := strings.TrimSpace(envelope.Error.Code)
	switch {
	case evidence.StatusCode == http.StatusTooManyRequests ||
		code == "rate_limit_exceeded":
		return classification(
			socialconnections.ExecutorFailureRateLimit, evidence, false,
		), true
	case evidence.StatusCode == http.StatusUnauthorized ||
		code == "access_token_invalid" ||
		code == "scope_not_authorized" ||
		code == "token_not_authorized_for_specified_publish_id":
		return classification(
			socialconnections.ExecutorFailureReconnect, evidence, true,
		), true
	case evidence.StatusCode >= 500 && safeReadMethod(evidence.Method):
		return classification(
			socialconnections.ExecutorFailureTemporary, evidence, false,
		), true
	case evidence.StatusCode >= 400 && evidence.StatusCode < 500:
		return classification(
			socialconnections.ExecutorFailurePermanent, evidence, false,
		), true
	default:
		return socialconnections.ProviderResponseClassification{}, false
	}
}

type YouTubeResponseClassifier struct{}

func (YouTubeResponseClassifier) ClassifyProviderResponse(
	evidence socialconnections.ProviderResponseEvidence,
) (socialconnections.ProviderResponseClassification, bool) {
	var envelope struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if json.Unmarshal(evidence.Body, &envelope) != nil {
		return socialconnections.ProviderResponseClassification{}, false
	}
	reasons := make(map[string]struct{}, len(envelope.Error.Errors))
	for _, item := range envelope.Error.Errors {
		reasons[strings.TrimSpace(item.Reason)] = struct{}{}
	}
	has := func(values ...string) bool {
		for _, value := range values {
			if _, found := reasons[value]; found {
				return true
			}
		}
		return false
	}
	switch {
	case evidence.StatusCode == http.StatusTooManyRequests ||
		has("quotaExceeded", "rateLimitExceeded", "userRateLimitExceeded"):
		return classification(
			socialconnections.ExecutorFailureRateLimit, evidence, false,
		), true
	case evidence.StatusCode == http.StatusUnauthorized ||
		has("authError", "insufficientPermissions"):
		return classification(
			socialconnections.ExecutorFailureReconnect, evidence, true,
		), true
	case evidence.StatusCode >= 500 && safeReadMethod(evidence.Method):
		return classification(
			socialconnections.ExecutorFailureTemporary, evidence, false,
		), true
	case evidence.StatusCode >= 400 && evidence.StatusCode < 500:
		return classification(
			socialconnections.ExecutorFailurePermanent, evidence, false,
		), true
	default:
		return socialconnections.ProviderResponseClassification{}, false
	}
}

func classification(
	kind socialconnections.ExecutorFailureKind,
	evidence socialconnections.ProviderResponseEvidence,
	reconnect bool,
) socialconnections.ProviderResponseClassification {
	return socialconnections.ProviderResponseClassification{
		Kind: kind,
		RetryAfter: responseRetryAfter(
			socialconnections.PublishingResponse{Header: evidence.Header}, 0,
		),
		Reconnect: reconnect,
	}
}

func safeReadMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

func ResponseClassifiers() map[socialconnections.Provider]socialconnections.ProviderResponseClassifier {
	return map[socialconnections.Provider]socialconnections.ProviderResponseClassifier{
		socialconnections.ProviderTikTok:  TikTokResponseClassifier{},
		socialconnections.ProviderYouTube: YouTubeResponseClassifier{},
	}
}

var (
	_ socialconnections.ProviderResponseClassifier = TikTokResponseClassifier{}
	_ socialconnections.ProviderResponseClassifier = YouTubeResponseClassifier{}
)
