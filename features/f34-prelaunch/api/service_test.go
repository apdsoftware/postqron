package prelaunch

import (
	"context"
	"errors"
	"testing"
	"time"
)

func validRequest() AccessRequest {
	return AccessRequest{
		Email:                "Person@Example.test",
		Locale:               "it-IT",
		AccessConsent:        true,
		MarketingConsent:     false,
		ConsentPolicyVersion: AccessConsentPolicyVersion,
	}
}

func TestSubmitStoresConsentAndTransactionalCommand(t *testing.T) {
	repository := NewMemoryRepository()
	now := time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
	service, err := NewService(repository, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	service.newID = func() (string, error) { return "pre_request", nil }

	result, err := service.Submit(context.Background(), validRequest(), "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.RequestID != "pre_request" {
		t.Fatalf("result = %#v", result)
	}
	requests := repository.Requests()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	request := requests[0]
	if request.Email != "person@example.test" || request.Locale != "it" {
		t.Fatalf("normalized request = %#v", request)
	}
	if !request.Consent.AccessConsent || request.Consent.MarketingConsent {
		t.Fatalf("consent proof = %#v", request.Consent)
	}
	command := request.Command
	if command.Event != ConfirmationEvent ||
		command.Channel != "transactional" ||
		command.TemplateID != ConfirmationTemplate ||
		command.Recipient.Email != request.Email {
		t.Fatalf("email command = %#v", command)
	}
}

func TestSubmitDeduplicatesNormalizedEmail(t *testing.T) {
	repository := NewMemoryRepository()
	service, _ := NewService(repository, func() time.Time {
		return time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
	})
	ids := []string{"pre_first", "pre_second"}
	service.newID = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	first := validRequest()
	second := validRequest()
	second.Email = " person@example.test "
	firstResult, err := service.Submit(context.Background(), first, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := service.Submit(context.Background(), second, "192.0.2.2")
	if err != nil {
		t.Fatal(err)
	}
	if !firstResult.Created || secondResult.Created ||
		secondResult.RequestID != firstResult.RequestID {
		t.Fatalf("results = %#v, %#v", firstResult, secondResult)
	}
	if len(repository.Requests()) != 1 {
		t.Fatal("deduplication created a second request")
	}
}

func TestSubmitRejectsImplicitOrMarketingConsent(t *testing.T) {
	repository := NewMemoryRepository()
	service, _ := NewService(repository, time.Now)
	tests := []struct {
		name string
		edit func(*AccessRequest)
		want error
	}{
		{"missing access", func(request *AccessRequest) {
			request.AccessConsent = false
		}, ErrConsentRequired},
		{"marketing", func(request *AccessRequest) {
			request.MarketingConsent = true
		}, ErrMarketingConsent},
		{"policy", func(request *AccessRequest) {
			request.ConsentPolicyVersion = "unknown"
		}, ErrInvalidPolicy},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest()
			test.edit(&request)
			_, err := service.Submit(
				context.Background(), request, "192.0.2.1",
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestSubmitRateLimitsClientWithoutStoringAddressInKey(t *testing.T) {
	repository := NewMemoryRepository()
	service, _ := NewService(repository, func() time.Time {
		return time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
	})
	for attempt := 0; attempt < rateLimitCount; attempt++ {
		request := validRequest()
		request.Email = "person" + string(rune('a'+attempt)) + "@example.test"
		if _, err := service.Submit(
			context.Background(), request, "192.0.2.20",
		); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	request := validRequest()
	request.Email = "last@example.test"
	if _, err := service.Submit(
		context.Background(), request, "192.0.2.20",
	); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("error = %v, want rate limited", err)
	}
	for key := range repository.rates {
		if key.key == "192.0.2.20" {
			t.Fatal("rate limiter stored raw client identity")
		}
	}
}
