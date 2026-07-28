package emailruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
