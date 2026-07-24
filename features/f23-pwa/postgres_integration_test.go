package pwa

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresSubscriptionFanoutIntegration(t *testing.T) {
	databaseURL := os.Getenv("POSTQRON_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("POSTQRON_TEST_DATABASE_URL is not set")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	cipher, err := NewAESGCMCipher("integration-key", key)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgresRepository(database, cipher)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 18, 0, 0, 0, time.UTC)
	gateway := &fakeGateway{}
	service := newTestService(t, repository, gateway, func() time.Time {
		return now
	})
	input := validInput(
		"integration-account",
		"https://push.example.test/integration-device",
	)
	subscription, created, err := service.Subscribe(context.Background(), input)
	if err != nil || !created {
		t.Fatalf("subscribe: created=%v err=%v", created, err)
	}
	if subscription.Endpoint != input.Endpoint {
		t.Fatalf("decrypted endpoint=%q", subscription.Endpoint)
	}
	event := validEvent()
	event.EventID = "integration-status-event"
	event.RecipientAccountIDs = []string{"integration-account"}
	createdDeliveries, err := service.ConsumeEvent(context.Background(), event)
	if err != nil || createdDeliveries != 1 {
		t.Fatalf("consume: deliveries=%d err=%v", createdDeliveries, err)
	}
	createdDeliveries, err = service.ConsumeEvent(context.Background(), event)
	if err != nil || createdDeliveries != 0 {
		t.Fatalf("duplicate: deliveries=%d err=%v", createdDeliveries, err)
	}
	found, err := service.Dispatch(context.Background())
	if err != nil || !found || len(gateway.sent) != 1 {
		t.Fatalf(
			"dispatch: found=%v sent=%d err=%v",
			found,
			len(gateway.sent),
			err,
		)
	}
}
