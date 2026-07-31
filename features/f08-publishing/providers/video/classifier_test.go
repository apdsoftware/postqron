package video

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
)

func TestProviderResponseClassifiersAreFailClosedAndSchedulingOnly(t *testing.T) {
	tests := []struct {
		name       string
		classifier socialconnections.ProviderResponseClassifier
		evidence   socialconnections.ProviderResponseEvidence
		kind       socialconnections.ExecutorFailureKind
		reconnect  bool
	}{
		{
			name:       "tiktok retry after",
			classifier: TikTokResponseClassifier{},
			evidence: socialconnections.ProviderResponseEvidence{
				StatusCode: http.StatusTooManyRequests,
				Method:     http.MethodPost,
				Header:     http.Header{"Retry-After": {"23"}},
				Body: []byte(
					`{"error":{"code":"rate_limit_exceeded","message":"token=do-not-copy"}}`,
				),
			},
			kind: socialconnections.ExecutorFailureRateLimit,
		},
		{
			name:       "tiktok reconnect",
			classifier: TikTokResponseClassifier{},
			evidence: socialconnections.ProviderResponseEvidence{
				StatusCode: http.StatusUnauthorized, Method: http.MethodPost,
				Body: []byte(`{"error":{"code":"access_token_invalid"}}`),
			},
			kind: socialconnections.ExecutorFailureReconnect, reconnect: true,
		},
		{
			name:       "youtube quota is not reconnect",
			classifier: YouTubeResponseClassifier{},
			evidence: socialconnections.ProviderResponseEvidence{
				StatusCode: http.StatusForbidden, Method: http.MethodPost,
				Body:   []byte(`{"error":{"errors":[{"reason":"quotaExceeded"}]}}`),
				Header: http.Header{"Retry-After": {"31"}},
			},
			kind: socialconnections.ExecutorFailureRateLimit,
		},
		{
			name:       "youtube permission reconnect",
			classifier: YouTubeResponseClassifier{},
			evidence: socialconnections.ProviderResponseEvidence{
				StatusCode: http.StatusForbidden, Method: http.MethodGet,
				Body: []byte(
					`{"error":{"errors":[{"reason":"insufficientPermissions"}]}}`,
				),
			},
			kind: socialconnections.ExecutorFailureReconnect, reconnect: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := test.classifier.ClassifyProviderResponse(test.evidence)
			if !ok || got.Kind != test.kind || got.Reconnect != test.reconnect {
				t.Fatalf("classification=%#v ok=%v", got, ok)
			}
			if raw := test.evidence.Header.Get("Retry-After"); raw != "" {
				seconds, _ := strconv.Atoi(raw)
				if got.RetryAfter != time.Duration(seconds)*time.Second {
					t.Fatalf("RetryAfter=%v", got.RetryAfter)
				}
			}
		})
	}
}

func TestProviderResponseClassifiersNeverDowngradeUnknownEvidence(t *testing.T) {
	for name, classifier := range map[string]socialconnections.ProviderResponseClassifier{
		"tiktok": TikTokResponseClassifier{}, "youtube": YouTubeResponseClassifier{},
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok := classifier.ClassifyProviderResponse(
				socialconnections.ProviderResponseEvidence{
					StatusCode: http.StatusInternalServerError,
					Method:     http.MethodPost,
					Body:       []byte(`{"unexpected":true}`),
				},
			); ok {
				t.Fatalf("unsafe classification=%#v", got)
			}
		})
	}
}

func TestResponseClassifierRegistryContainsOnlyVideoProviders(t *testing.T) {
	classifiers := ResponseClassifiers()
	if len(classifiers) != 2 ||
		classifiers[socialconnections.ProviderTikTok] == nil ||
		classifiers[socialconnections.ProviderYouTube] == nil {
		t.Fatalf("classifiers=%#v", classifiers)
	}
}
