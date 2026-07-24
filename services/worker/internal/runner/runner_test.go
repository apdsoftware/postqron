package runner

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
)

func TestTickRunsDiscoveredFeatures(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	features := []featureruntime.Feature{{
		Manifest: featureruntime.Manifest{
			ID:      "runtime",
			Version: "0.1.0",
		},
	}}

	New(features, time.Second, logger).Tick(context.Background())

	if got := output.String(); !strings.Contains(got, `"feature":"runtime"`) {
		t.Fatalf("Tick() output = %q, want runtime feature", got)
	}
}

func TestTickHonorsCancelledContext(t *testing.T) {
	var output bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	New(
		[]featureruntime.Feature{{Manifest: featureruntime.Manifest{ID: "runtime"}}},
		time.Second,
		slog.New(slog.NewJSONHandler(&output, nil)),
	).Tick(ctx)

	if output.Len() != 0 {
		t.Fatalf("Tick() output = %q, want none", output.String())
	}
}
