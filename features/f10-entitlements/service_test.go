package entitlements

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type serviceStoreStub struct {
	command UsageCommand
	result  UsageDecision
	err     error
}

func (store *serviceStoreStub) Overview(context.Context, string) (Overview, error) {
	return Overview{}, store.err
}

func (store *serviceStoreStub) ApplyUsage(
	_ context.Context,
	command UsageCommand,
) (UsageDecision, error) {
	store.command = command
	return store.result, store.err
}

func TestServiceReservePassesIdempotentServerCommand(t *testing.T) {
	store := &serviceStoreStub{
		result: UsageDecision{
			Accepted: true,
			Code:     "accepted",
			Usage: Usage{
				Resource:  ResourceChannels,
				Used:      3,
				Limit:     5,
				Remaining: 2,
			},
		},
	}
	service := NewService(store)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	result, err := service.Reserve(
		context.Background(),
		"46c847c5-621f-4c2a-a672-bdfeb2f9aa29",
		ResourceChannels,
		1,
		"connect:channel-3",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || result.Usage.Remaining != 2 {
		t.Fatalf("Reserve() = %#v", result)
	}
	if store.command.Delta != 1 ||
		store.command.IdempotencyKey != "connect:channel-3" ||
		!store.command.OccurredAt.Equal(now) {
		t.Fatalf("command = %#v", store.command)
	}
}

func TestServiceRejectsInvalidCommandsBeforeStore(t *testing.T) {
	tests := []struct {
		name string
		call func(*Service) error
		want error
	}{
		{
			name: "missing workspace",
			call: func(service *Service) error {
				_, err := service.Reserve(
					context.Background(),
					"",
					ResourceChannels,
					1,
					"key",
				)
				return err
			},
			want: ErrInvalidWorkspace,
		},
		{
			name: "unknown resource",
			call: func(service *Service) error {
				_, err := service.Reserve(
					context.Background(),
					"workspace",
					Resource("other"),
					1,
					"key",
				)
				return err
			},
			want: ErrUnknownResource,
		},
		{
			name: "zero amount",
			call: func(service *Service) error {
				_, err := service.Reserve(
					context.Background(),
					"workspace",
					ResourceChannels,
					0,
					"key",
				)
				return err
			},
			want: ErrInvalidAmount,
		},
		{
			name: "negative reserve",
			call: func(service *Service) error {
				_, err := service.Reserve(
					context.Background(),
					"workspace",
					ResourceChannels,
					-1,
					"key",
				)
				return err
			},
			want: ErrInvalidAmount,
		},
		{
			name: "negative release",
			call: func(service *Service) error {
				_, err := service.Release(
					context.Background(),
					"workspace",
					ResourceChannels,
					-1,
					"key",
				)
				return err
			},
			want: ErrInvalidAmount,
		},
		{
			name: "missing key",
			call: func(service *Service) error {
				_, err := service.Reserve(
					context.Background(),
					"workspace",
					ResourceChannels,
					1,
					"",
				)
				return err
			},
			want: ErrInvalidIdempotencyKey,
		},
		{
			name: "oversized key",
			call: func(service *Service) error {
				_, err := service.Reserve(
					context.Background(),
					"workspace",
					ResourceChannels,
					1,
					strings.Repeat("x", 256),
				)
				return err
			},
			want: ErrInvalidIdempotencyKey,
		},
		{
			name: "publication release",
			call: func(service *Service) error {
				_, err := service.Release(
					context.Background(),
					"workspace",
					ResourceScheduledPublications,
					1,
					"key",
				)
				return err
			},
			want: ErrPublicationRelease,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &serviceStoreStub{}
			err := test.call(NewService(store))
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if store.command != (UsageCommand{}) {
				t.Fatalf("store received invalid command %#v", store.command)
			}
		})
	}
}
