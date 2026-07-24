package runner

import (
	"context"
	"log/slog"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
)

type Runner struct {
	features []featureruntime.Feature
	interval time.Duration
	logger   *slog.Logger
}

func New(features []featureruntime.Feature, interval time.Duration, logger *slog.Logger) *Runner {
	return &Runner{
		features: features,
		interval: interval,
		logger:   logger,
	}
}

func (r *Runner) Run(ctx context.Context) {
	r.Tick(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Tick(ctx)
		}
	}
}

func (r *Runner) Tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	for _, feature := range r.features {
		r.logger.Info(
			"worker feature tick",
			"feature", feature.Manifest.ID,
			"version", feature.Manifest.Version,
		)
	}
}
