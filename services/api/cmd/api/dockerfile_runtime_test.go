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
	if !strings.Contains(
		dockerfile,
		"COPY services/worker/go.mod services/worker/go.sum services/worker/",
	) {
		t.Fatal("API Dockerfile must copy worker go.mod so go.work resolution does not break image smoke builds")
	}
	if !strings.Contains(
		dockerfile,
		"COPY features/f15-operations/go.mod features/f15-operations/",
	) {
		t.Fatal("API Dockerfile must copy f15 go.mod for the admin console dependency chain")
	}
	if !strings.Contains(
		dockerfile,
		"COPY features/f31-admin-console/go.mod features/f31-admin-console/",
	) {
		t.Fatal("API Dockerfile must copy f31 go.mod so the runtime API can resolve admin-console")
	}
	if !strings.Contains(
		dockerfile,
		"COPY features/f34-prelaunch/api/go.mod features/f34-prelaunch/api/",
	) {
		t.Fatal("API Dockerfile must copy f34 go.mod so the runtime API can resolve prelaunch-access")
	}
	if !strings.Contains(dockerfile, "COPY services/api/features ./foundation") {
		t.Fatal("API Dockerfile must copy services adapters into the runtime foundation root")
	}
}
