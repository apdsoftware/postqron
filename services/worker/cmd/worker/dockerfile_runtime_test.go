package main

import (
	"os"
	"strings"
	"testing"
)

func TestWorkerDockerfileIncludesEmailBrandTokens(t *testing.T) {
	contents, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(contents)
	if !strings.Contains(
		dockerfile,
		"COPY features/f01-brand/tokens ./features/f01-brand/tokens",
	) {
		t.Fatal("worker Dockerfile must copy F1 tokens required by the F14 renderer")
	}
	if !strings.Contains(
		dockerfile,
		"COPY features/f03-auth/go.mod features/f03-auth/go.sum features/f03-auth/",
	) {
		t.Fatal("worker Dockerfile must copy F3 go.mod for local replace resolution during go mod download")
	}
	if !strings.Contains(
		dockerfile,
		"COPY services/api/go.mod services/api/go.sum services/api/",
	) {
		t.Fatal("worker Dockerfile must copy API go.mod so go.work resolution does not break image smoke builds")
	}
	if !strings.Contains(
		dockerfile,
		"COPY features/f05-social-connections/go.mod features/f05-social-connections/go.sum features/f05-social-connections/",
	) {
		t.Fatal("worker Dockerfile must stage the F5 executor module before dependency download")
	}
	if !strings.Contains(
		dockerfile,
		"COPY features/f08-publishing/go.mod features/f08-publishing/go.sum features/f08-publishing/",
	) {
		t.Fatal("worker Dockerfile must stage the F8 module before dependency download")
	}
}
