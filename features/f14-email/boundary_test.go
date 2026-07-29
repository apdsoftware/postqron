package email

import (
	"strings"
	"testing"
	"time"
)

type mapEnv map[string]string

func (env mapEnv) Lookup(name string) (string, bool) {
	value, ok := env[name]
	return value, ok
}

func testLiveSenderEnv() mapEnv {
	return mapEnv{
		postqronMailronixEndpointEnv:         MailronixProductionURL,
		postqronMailronixAPIKeySecretNameEnv: "MAILRONIX_TRANSACTIONAL_API_KEY",
		postqronMailronixSenderEmailEnv:      "notifications@postqron.example",
		postqronMailronixDomainVerifiedEnv:   "true",
		postqronMailronixFailureThresholdEnv: "3",
		postqronMailronixCircuitOpenForEnv:   "2m",
	}
}

func TestNewSenderBoundaryDefaultsToFakeInDevelopment(t *testing.T) {
	boundary, err := newSenderBoundaryFromEnv(
		mapEnv{}.Lookup,
		SenderBoundaryOptions{Environment: "development"},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if boundary.Mode != SenderModeFake {
		t.Fatalf("mode = %q", boundary.Mode)
	}
	if _, ok := boundary.Sender.(*FakeSender); !ok {
		t.Fatalf("sender = %#v, want FakeSender", boundary.Sender)
	}
	fields := boundary.Config.Fields()
	if fields["email.sender_mode"] != "fake" ||
		fields["email.provider"] != "fake" {
		t.Fatalf("fields = %#v", fields)
	}
}

func TestNewSenderBoundaryFailsClosedWithoutExplicitLiveModeInProduction(t *testing.T) {
	_, err := newSenderBoundaryFromEnv(
		mapEnv{}.Lookup,
		SenderBoundaryOptions{Environment: "production", Production: true},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "explicit live sender mode") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewSenderBoundaryRequiresExplicitModeOutsideLocalDevelopment(t *testing.T) {
	_, err := newSenderBoundaryFromEnv(
		mapEnv{}.Lookup,
		SenderBoundaryOptions{Environment: "staging"},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), `environment "staging"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestNewSenderBoundaryRejectsNonProductionModesInProduction(t *testing.T) {
	for _, mode := range []SenderMode{SenderModeFake, SenderModeNoop} {
		t.Run(string(mode), func(t *testing.T) {
			_, err := newSenderBoundaryFromEnv(
				mapEnv{}.Lookup,
				SenderBoundaryOptions{
					Environment: "production", Production: true, Mode: mode,
				},
				nil,
			)
			if err == nil || !strings.Contains(err.Error(), "forbidden in production") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestNewSenderBoundaryBuildsLiveMailronixClientFromEnvironment(t *testing.T) {
	boundary, err := newSenderBoundaryFromEnv(
		testLiveSenderEnv().Lookup,
		SenderBoundaryOptions{
			Environment: "production", Production: true,
			Mode: SenderModeLive, HTTPTimeout: 12 * time.Second,
		},
		mapSecrets{"MAILRONIX_TRANSACTIONAL_API_KEY": "mrx_live_super_secret"},
	)
	if err != nil {
		t.Fatal(err)
	}
	client, ok := boundary.Sender.(*MailronixClient)
	if !ok {
		t.Fatalf("sender = %#v, want MailronixClient", boundary.Sender)
	}
	if client.http.Timeout != 12*time.Second ||
		client.config.Endpoint != MailronixProductionURL ||
		client.config.APIKeySecret != "MAILRONIX_TRANSACTIONAL_API_KEY" {
		t.Fatalf("client config = %#v", client.config)
	}
	fields := boundary.Config.Fields()
	joined := strings.Join(mapValues(fields), " ")
	if fields["email.sender_mode"] != "live" ||
		fields["email.provider"] != "mailronix" ||
		fields["email.api_key_secret_name"] != "MAILRONIX_TRANSACTIONAL_API_KEY" ||
		strings.Contains(joined, "mrx_live_super_secret") {
		t.Fatalf("redacted fields = %#v", fields)
	}
}

func TestNewSenderBoundaryAllowsNoopOnlyOutsideProduction(t *testing.T) {
	boundary, err := newSenderBoundaryFromEnv(
		mapEnv{}.Lookup,
		SenderBoundaryOptions{Environment: "staging", Mode: SenderModeNoop},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := boundary.Sender.(*NoopSender); !ok {
		t.Fatalf("sender = %#v, want NoopSender", boundary.Sender)
	}
}

func TestNewSenderBoundaryRejectsFakeOutsideLocalDevelopmentTestCI(t *testing.T) {
	_, err := newSenderBoundaryFromEnv(
		mapEnv{}.Lookup,
		SenderBoundaryOptions{Environment: "staging", Mode: SenderModeFake},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "fake sender is only supported") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewSenderBoundaryRedactsInvalidEnvironmentConfigValues(t *testing.T) {
	env := testLiveSenderEnv()
	env[postqronMailronixFailureThresholdEnv] = "mrx_live_super_secret"
	_, err := newSenderBoundaryFromEnv(
		env.Lookup,
		SenderBoundaryOptions{
			Environment: "production", Production: true, Mode: SenderModeLive,
		},
		mapSecrets{"MAILRONIX_TRANSACTIONAL_API_KEY": "mrx_live_super_secret"},
	)
	if err == nil {
		t.Fatal("expected invalid config error")
	}
	if strings.Contains(err.Error(), "mrx_live_super_secret") ||
		!strings.Contains(err.Error(), postqronMailronixFailureThresholdEnv) {
		t.Fatalf("error leaked config value: %v", err)
	}
}

func mapValues(input map[string]string) []string {
	values := make([]string, 0, len(input))
	for _, value := range input {
		values = append(values, value)
	}
	return values
}
