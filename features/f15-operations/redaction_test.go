package operations

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactingHandlerRemovesSensitiveFieldsAndPatterns(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(slog.NewJSONHandler(&output, nil))).With(
		"service", "api",
		"api_token", "token-from-context",
	)

	logger.Info(
		"request for person@example.test with Bearer abc.def",
		"workspace_id", "workspace_123",
		"authorization", "Bearer very-secret-token",
		"database", "postgres://postqron:db-password@database/postqron",
		"error", "callback?access_token=oauth-secret&state=ok",
		slog.Group("request",
			"email", "person@example.test",
			"method", "POST",
		),
	)

	got := output.String()
	for _, forbidden := range []string{
		"person@example.test",
		"abc.def",
		"very-secret-token",
		"db-password",
		"oauth-secret",
		"token-from-context",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("redacted log contains %q: %s", forbidden, got)
		}
	}
	for _, wanted := range []string{
		`"service":"api"`,
		`"workspace_id":"workspace_123"`,
		`"method":"POST"`,
		RedactedValue,
	} {
		if !strings.Contains(got, wanted) {
			t.Fatalf("redacted log does not contain %q: %s", wanted, got)
		}
	}
}

func TestRedactingHandlerPreservesLevelsAndGroups(t *testing.T) {
	var output bytes.Buffer
	handler := NewRedactingHandler(slog.NewTextHandler(&output, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
	logger := slog.New(handler).WithGroup("security")

	logger.Info("ignored")
	logger.Warn("denied", "reason", "policy")

	got := output.String()
	if strings.Contains(got, "ignored") {
		t.Fatalf("handler ignored configured log level: %s", got)
	}
	if !strings.Contains(got, "security.reason=policy") {
		t.Fatalf("handler lost group: %s", got)
	}
}
