package main

import (
	"os"
	"strings"
	"testing"
)

func TestProductionDeliveryWiresIndependentOAuthProviders(t *testing.T) {
	composeBytes, err := os.ReadFile("../../../../infra/deploy/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	workflowBytes, err := os.ReadFile("../../../../.github/workflows/deploy.yml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(composeBytes)
	workflow := string(workflowBytes)
	for _, provider := range []string{"GOOGLE", "APPLE", "FACEBOOK", "LINKEDIN"} {
		for _, suffix := range []string{"CLIENT_ID", "CLIENT_SECRET", "REDIRECT_URL"} {
			name := "POSTQRON_AUTH_" + provider + "_" + suffix
			if !strings.Contains(compose, name+": ${"+name+":-}") {
				t.Fatalf("production Compose does not forward optional %s", name)
			}
			if !strings.Contains(workflow, name) {
				t.Fatalf("production workflow does not consume dedicated %s", name)
			}
		}
	}
	if !strings.Contains(
		workflow,
		`local expected_redirect="https://${API_DOMAIN}/api/v1/auth/callback"`,
	) {
		t.Fatal("production workflow does not enforce the canonical API OAuth callback")
	}
	if !strings.Contains(
		workflow,
		`POSTQRON_AUTH_(GOOGLE|APPLE|FACEBOOK|LINKEDIN)_`,
	) {
		t.Fatal("production workflow does not reject OAuth values duplicated in RUNTIME_ENV")
	}
}
