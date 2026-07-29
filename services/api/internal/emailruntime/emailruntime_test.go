package emailruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	email "github.com/apdsoftware/postqron/features/f14-email"
)

func TestValidateAppDomainRejectsSchemesAndPaths(t *testing.T) {
	for _, value := range []string{
		"",
		"https://app.example.test",
		"http://app.example.test",
		"app.example.test/path",
		"app.example.test?query=1",
		"user@app.example.test",
	} {
		if _, err := validateAppDomain(value); err == nil {
			t.Fatalf("validateAppDomain(%q) accepted an invalid domain", value)
		}
	}
	got, err := validateAppDomain("app.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if got != "app.example.test" {
		t.Fatalf("validated domain = %q", got)
	}
}

func TestAPIDockerfileCopiesF1TokensForEmailRuntime(t *testing.T) {
	dockerfilePath := filepath.Join("..", "..", "Dockerfile")
	source, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		string(source),
		"COPY features/f01-brand/tokens ./features/f01-brand/tokens",
	) {
		t.Fatalf("Dockerfile does not copy F1 tokens for email runtime:\n%s", source)
	}
}

func TestRuntimeSenderBoundaryDefaultsToFakeInDevelopment(t *testing.T) {
	clearMailronixEnvironment(t)
	t.Setenv(postqronEnvEnv, "development")

	boundary, err := runtimeSenderBoundary()
	if err != nil {
		t.Fatal(err)
	}
	if boundary.Mode != email.SenderModeFake {
		t.Fatalf("mode = %q", boundary.Mode)
	}
	fields := boundary.Config.Fields()
	if fields["email.provider"] != "fake" || fields["email.sender_mode"] != "fake" {
		t.Fatalf("fields = %#v", fields)
	}
}

func TestRuntimeSenderBoundaryFailsClosedInProductionWithoutCompleteConfiguration(t *testing.T) {
	clearMailronixEnvironment(t)
	t.Setenv(postqronEnvEnv, "production")

	if _, err := runtimeSenderBoundary(); err == nil ||
		!strings.Contains(err.Error(), "missing "+mailronixEndpointEnv) {
		t.Fatalf("error = %v", err)
	}
}

func TestRuntimeSenderBoundaryUsesLiveModeWhenConfigurationIsComplete(t *testing.T) {
	clearMailronixEnvironment(t)
	t.Setenv(postqronEnvEnv, "staging")
	t.Setenv(mailronixEndpointEnv, "https://delivery.example.test/email/send")
	t.Setenv(mailronixAPIKeySecretEnv, "MAILRONIX_TRANSACTIONAL_API_KEY")
	t.Setenv(mailronixSenderEmailEnv, "notifications@example.test")
	t.Setenv(mailronixDomainVerifiedEnv, "true")
	t.Setenv("MAILRONIX_TRANSACTIONAL_API_KEY", "mrx_live_secret")

	boundary, err := runtimeSenderBoundary()
	if err != nil {
		t.Fatal(err)
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
