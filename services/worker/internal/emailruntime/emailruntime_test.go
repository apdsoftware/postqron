package emailruntime

import (
	"strings"
	"testing"

	email "github.com/apdsoftware/postqron/features/f14-email"
)

func TestValidateAppDomainRequiresBareHost(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "https://app.example.test", "app.example.test/path", "user@app.example.test"} {
		if _, err := validateAppDomain(value); err == nil {
			t.Fatalf("validateAppDomain(%q) succeeded, want error", value)
		}
	}
	got, err := validateAppDomain("app.example.test")
	if err != nil {
		t.Fatalf("validateAppDomain: %v", err)
	}
	if got != "app.example.test" {
		t.Fatalf("validateAppDomain = %q", got)
	}
}

func TestRuntimeSenderAllowsFakeOnlyOutsideProduction(t *testing.T) {
	clearMailronixEnvironment(t)
	t.Setenv(postqronEnvEnv, "development")

	boundary, err := runtimeSenderBoundary()
	if err != nil {
		t.Fatalf("runtimeSenderBoundary: %v", err)
	}
	if boundary.Mode != email.SenderModeFake {
		t.Fatalf("mode = %q", boundary.Mode)
	}
	if _, ok := boundary.Sender.(*email.FakeSender); !ok {
		t.Fatalf("runtimeSenderBoundary sender = %T, want *email.FakeSender", boundary.Sender)
	}
}

func TestRuntimeSenderFailsClosedInProduction(t *testing.T) {
	clearMailronixEnvironment(t)
	t.Setenv(postqronEnvEnv, "production")

	if _, err := runtimeSenderBoundary(); err == nil ||
		!strings.Contains(err.Error(), "missing "+mailronixEndpointEnv) {
		t.Fatalf("runtimeSender error = %v", err)
	}
}

func TestRuntimeSenderUsesLiveModeWhenConfigurationIsComplete(t *testing.T) {
	clearMailronixEnvironment(t)
	t.Setenv(postqronEnvEnv, "staging")
	t.Setenv(mailronixEndpointEnv, "https://delivery.example.test/email/send")
	t.Setenv(mailronixAPIKeySecretEnv, "MAILRONIX_TRANSACTIONAL_API_KEY")
	t.Setenv(mailronixSenderEmailEnv, "notifications@example.test")
	t.Setenv(mailronixDomainVerifiedEnv, "true")
	t.Setenv("MAILRONIX_TRANSACTIONAL_API_KEY", "mrx_live_secret")

	boundary, err := runtimeSenderBoundary()
	if err != nil {
		t.Fatalf("runtimeSenderBoundary: %v", err)
	}
	if boundary.Mode != email.SenderModeLive {
		t.Fatalf("mode = %q", boundary.Mode)
	}
	fields := boundary.Config.Fields()
	if fields["email.provider"] != "mailronix" ||
		fields["email.sender_mode"] != "live" ||
		fields["email.api_key_secret_name"] != "MAILRONIX_TRANSACTIONAL_API_KEY" {
		t.Fatalf("fields = %#v", fields)
	}
}

func clearMailronixEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		postqronEnvEnv,
		mailronixEndpointEnv,
		mailronixAPIKeySecretEnv,
		mailronixSenderEmailEnv,
		mailronixDomainVerifiedEnv,
		mailronixFailureThresholdEnv,
		mailronixCircuitOpenForEnv,
		"MAILRONIX_TRANSACTIONAL_API_KEY",
	} {
		t.Setenv(key, "")
	}
}
