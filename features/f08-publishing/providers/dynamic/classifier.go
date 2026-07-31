package dynamic

import (
	"encoding/json"
	"net/http"
	"strings"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
)

type MastodonResponseClassifier struct{}

func (MastodonResponseClassifier) ClassifyProviderResponse(
	evidence socialconnections.ProviderResponseEvidence,
) (socialconnections.ProviderResponseClassification, bool) {
	switch {
	case evidence.StatusCode == http.StatusTooManyRequests:
		return classify(socialconnections.ExecutorFailureRateLimit, evidence, false), true
	case evidence.StatusCode == http.StatusUnauthorized ||
		evidence.StatusCode == http.StatusForbidden:
		return classify(socialconnections.ExecutorFailureReconnect, evidence, true), true
	case evidence.StatusCode >= 500 && safeRead(evidence.Method):
		return classify(socialconnections.ExecutorFailureTemporary, evidence, false), true
	case evidence.StatusCode >= 400 && evidence.StatusCode < 500:
		return classify(socialconnections.ExecutorFailurePermanent, evidence, false), true
	default:
		return socialconnections.ProviderResponseClassification{}, false
	}
}

type BlueskyResponseClassifier struct{}

func (BlueskyResponseClassifier) ClassifyProviderResponse(
	evidence socialconnections.ProviderResponseEvidence,
) (socialconnections.ProviderResponseClassification, bool) {
	var envelope struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(evidence.Body, &envelope)
	code := strings.TrimSpace(envelope.Error)
	switch {
	case evidence.StatusCode == http.StatusTooManyRequests ||
		code == "RateLimitExceeded":
		return classify(socialconnections.ExecutorFailureRateLimit, evidence, false), true
	case evidence.StatusCode == http.StatusUnauthorized ||
		code == "AuthMissing" || code == "InvalidToken" || code == "ExpiredToken":
		return classify(socialconnections.ExecutorFailureReconnect, evidence, true), true
	case evidence.StatusCode >= 500 && safeRead(evidence.Method):
		return classify(socialconnections.ExecutorFailureTemporary, evidence, false), true
	case evidence.StatusCode >= 400 && evidence.StatusCode < 500:
		return classify(socialconnections.ExecutorFailurePermanent, evidence, false), true
	default:
		return socialconnections.ProviderResponseClassification{}, false
	}
}

func classify(
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

func safeRead(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

func ResponseClassifiers() map[socialconnections.Provider]socialconnections.ProviderResponseClassifier {
	return map[socialconnections.Provider]socialconnections.ProviderResponseClassifier{
		socialconnections.ProviderMastodon: MastodonResponseClassifier{},
		socialconnections.ProviderBluesky:  BlueskyResponseClassifier{},
	}
}

var (
	_ socialconnections.ProviderResponseClassifier = MastodonResponseClassifier{}
	_ socialconnections.ProviderResponseClassifier = BlueskyResponseClassifier{}
)
