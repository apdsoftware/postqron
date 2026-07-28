package main

import (
	"os"
	"strings"
	"testing"
)

func TestAPIDockerfilePreservesCustomRuntimeFactories(t *testing.T) {
	contents, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(contents)
	if !strings.Contains(
		dockerfile,
		"POSTQRON_BUILD_SERVER_FACTORIES=0 ./scripts/runtime/bundle-features.sh features /out/features",
	) {
		t.Fatal("API Dockerfile must disable server factory regeneration when bundling features")
	}
	if !strings.Contains(dockerfile, "COPY services/api/features ./foundation") {
		t.Fatal("API Dockerfile must copy services adapters into the runtime foundation root")
	}
}
