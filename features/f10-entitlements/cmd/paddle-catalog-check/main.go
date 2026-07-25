package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	entitlements "github.com/apdsoftware/postqron/features/f10-entitlements"
)

func main() {
	config, err := entitlements.NewPaddleConfigFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid Paddle configuration")
		os.Exit(2)
	}
	client, err := entitlements.NewPaddleClient(
		config,
		&http.Client{Timeout: 15 * time.Second},
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid Paddle configuration")
		os.Exit(2)
	}
	checks, err := client.DryRunCatalog(context.Background(), config.Catalog)
	if err == nil {
		err = entitlements.WriteCatalogDryRun(os.Stdout, checks)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Paddle catalog dry-run failed")
		os.Exit(1)
	}
}
