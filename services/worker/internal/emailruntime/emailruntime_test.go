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
	t.Setenv("PADDLE_ENVIRONMENT", "sandbox")

	sender, err := runtimeSender()
	if err != nil {
		t.Fatalf("runtimeSender: %v", err)
	}
	if _, ok := sender.(*email.FakeSender); !ok {
		t.Fatalf("runtimeSender = %T, want *email.FakeSender", sender)
	}
}

func TestRuntimeSenderFailsClosedInProduction(t *testing.T) {
	clearMailronixEnvironment(t)
	t.Setenv("PADDLE_ENVIRONMENT", "production")

	if _, err := runtimeSender(); err == nil ||
		!strings.Contains(err.Error(), "Mailronix configuration") {
		t.Fatalf("runtimeSender error = %v", err)
	}
}

func TestRuntimeSenderRejectsPartialConfiguration(t *testing.T) {
	clearMailronixEnvironment(t)
	t.Setenv("PADDLE_ENVIRONMENT", "sandbox")
	t.Setenv(mailronixEndpointEnv, "https://mail.example.test/send")

	if _, err := runtimeSender(); err == nil ||
		!strings.Contains(err.Error(), "Mailronix configuration") {
		t.Fatalf("runtimeSender error = %v", err)
	}
}

func clearMailronixEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		mailronixEndpointEnv,
		mailronixAPIKeySecretEnv,
		mailronixSenderEmailEnv,
		mailronixDomainVerifiedEnv,
		mailronixFailureThresholdEnv,
		mailronixCircuitOpenForEnv,
	} {
		t.Setenv(key, "")
	}
}
