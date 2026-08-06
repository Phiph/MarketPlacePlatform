// Command broker runs the marketplace broker API: a multi-tenant HTTP
// facade over the Kratix Promises installed on the platform cluster. See
// README.md for how to run it and what it exposes.
package main

import (
	"log"
	"net/http"
	"os"

	"marketplace-broker/internal/api"
	"marketplace-broker/internal/k8sclient"
	"marketplace-broker/internal/tenant"
)

func main() {
	addr := os.Getenv("BROKER_ADDR")
	if addr == "" {
		addr = ":8878" // avoid :8080 - too easily collides with other local dev tools
	}

	teamsFile := os.Getenv("BROKER_TEAMS_FILE")
	if teamsFile == "" {
		teamsFile = "config/teams.yaml"
	}

	// Origin the UI is served from - lets the browser allow cross-origin
	// calls from `make ui-dev` (localhost:5173) to the broker
	// (localhost:8080). Set to "" to disable CORS for same-origin setups.
	corsOrigin := os.Getenv("BROKER_CORS_ORIGIN")
	if corsOrigin == "" {
		corsOrigin = "http://localhost:5173"
	}

	dir, err := tenant.Load(teamsFile)
	if err != nil {
		log.Fatalf("loading team directory: %v", err)
	}

	clients, err := k8sclient.New()
	if err != nil {
		log.Fatalf("building Kubernetes clients: %v", err)
	}

	server := api.New(clients, dir, corsOrigin)

	log.Printf("marketplace broker listening on %s", addr)
	if err := http.ListenAndServe(addr, server.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
