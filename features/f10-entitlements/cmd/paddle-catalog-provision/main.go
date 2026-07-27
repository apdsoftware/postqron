package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	entitlements "github.com/apdsoftware/postqron/features/f10-entitlements"
)

func main() {
	var apply bool
	var manifestPath string
	var mappingPath string
	flag.BoolVar(&apply, "apply", false, "create missing D09 products and prices")
	flag.StringVar(
		&manifestPath,
		"manifest",
		"../../infra/paddle/catalog-d09-v1.json",
		"path to the versioned catalog manifest",
	)
	flag.StringVar(&mappingPath, "mapping-out", "", "write the resolved runtime mapping")
	flag.Parse()

	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		fail()
	}
	manifest, decodeErr := entitlements.DecodePaddleCatalogManifest(manifestFile)
	closeErr := manifestFile.Close()
	if decodeErr != nil || closeErr != nil {
		fail()
	}
	environment := entitlements.PaddleEnvironment(os.Getenv("PADDLE_ENVIRONMENT"))
	client, err := entitlements.NewPaddleCatalogClient(
		environment,
		os.Getenv("PADDLE_API_KEY"),
		&http.Client{Timeout: 15 * time.Second},
	)
	if err != nil {
		fail()
	}
	result, err := client.ProvisionCatalog(context.Background(), manifest, apply)
	if err != nil {
		fail()
	}
	if err := entitlements.WriteCatalogProvisionReport(os.Stdout, result); err != nil {
		fail()
	}
	if mappingPath == "" {
		return
	}
	if len(result.Catalog) != 14 {
		fmt.Fprintln(os.Stderr, "catalog plan is incomplete; mapping was not written")
		os.Exit(1)
	}
	mappingFile, err := os.OpenFile(mappingPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		fail()
	}
	writeErr := entitlements.WritePaddleCatalogMapping(mappingFile, result.Catalog)
	closeErr = mappingFile.Close()
	if writeErr != nil || closeErr != nil {
		fail()
	}
}

func fail() {
	fmt.Fprintln(os.Stderr, "Paddle catalog provisioning failed")
	os.Exit(1)
}
