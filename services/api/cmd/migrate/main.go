package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/api/internal/migrations"
)

func main() {
	checkOnly := flag.Bool("check", false, "validate manifests and migrations without a database")
	rootsFlag := flag.String("roots", "", "feature roots separated by the OS path-list separator")
	flag.Parse()

	roots := filepath.SplitList(*rootsFlag)
	if *rootsFlag == "" {
		roots = filepath.SplitList(envOrDefault("POSTQRON_FEATURE_ROOTS", "services/api/features"))
	}
	features, err := featureruntime.Discover(roots...)
	exitOnError(err)
	collected, err := migrations.Collect(features)
	exitOnError(err)

	if *checkOnly {
		fmt.Printf("validated %d feature(s) and %d migration(s)\n", len(features), len(collected))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	exitOnError(migrations.Apply(ctx, os.Getenv("DATABASE_URL"), collected))
	fmt.Printf("applied %d migration(s)\n", len(collected))
}

func exitOnError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
