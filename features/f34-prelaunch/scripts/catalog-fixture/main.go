package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"time"

	entitlements "github.com/apdsoftware/postqron/features/f10-entitlements"
)

func main() {
	port := os.Getenv("F34_E2E_CATALOG_PORT")
	if port == "" {
		port = "41737"
	}
	supervisorPID, _ := strconv.Atoi(os.Getenv("F34_E2E_SUPERVISOR_PID"))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/billing/plans", func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(writer).Encode(map[string]any{
			"provider":        "paddle",
			"catalog_version": entitlements.CatalogVersion,
			"currency":        "EUR",
			"plans":           entitlements.PublicPlans(),
		}); err != nil {
			panic(err)
		}
	})

	server := &http.Server{
		Addr:              "127.0.0.1:" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if supervisorPID > 0 {
		go stopWithoutSupervisor(server, supervisorPID)
	}
	fmt.Printf("F34 catalog fixture listening on http://%s\n", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}

func stopWithoutSupervisor(server *http.Server, supervisorPID int) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if err := syscall.Kill(supervisorPID, 0); err != nil {
			_ = server.Close()
			return
		}
	}
}
