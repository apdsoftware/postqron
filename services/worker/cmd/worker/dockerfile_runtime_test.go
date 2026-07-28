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
	if !strings.Contains(
		string(contents),
		"COPY features/f01-brand/tokens ./features/f01-brand/tokens",
	) {
		t.Fatal("worker Dockerfile must copy F1 tokens required by the F14 renderer")
	}
}
