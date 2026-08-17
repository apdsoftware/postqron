# PostQron — CI locale.
# La Continuous Integration gira esclusivamente qui: nessun workflow GitHub.
# Vedi AGENTS.md §2.

SHELL := /bin/bash
.DEFAULT_GOAL := help

GO_DIR  := services/api
WEB_DIR := apps/web
DSH_DIR := apps/dashboard

# Esegue il comando solo se la directory esiste: il monorepo si popola per issue,
# e la CI deve restare verde anche quando un componente non è ancora stato creato.
define if_dir
	@if [ -d "$(1)" ]; then \
		echo "→ $(1): $(2)"; \
		cd "$(1)" && $(2); \
	else \
		echo "· $(1) non ancora presente, salto"; \
	fi
endef

.PHONY: help
help: ## Mostra questo elenco
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- setup

.PHONY: setup
setup: ## Installa le dipendenze e prepara l'ambiente
	@command -v pnpm >/dev/null || { echo "pnpm non installato"; exit 1; }
	@command -v go   >/dev/null || { echo "go non installato"; exit 1; }
	@if [ -f pnpm-workspace.yaml ]; then pnpm install --frozen-lockfile || pnpm install; fi
	@if [ -f go.work ]; then go work sync; fi
	@$(MAKE) hooks

.PHONY: hooks
hooks: ## Installa l'hook pre-push che esegue `make ci`
	@mkdir -p .git/hooks
	@printf '#!/bin/sh\nexec make ci\n' > .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "hook pre-push installato"

# ---------------------------------------------------------------- database

.PHONY: db-up
db-up: ## Avvia PostgreSQL locale
	@if [ -f docker-compose.yml ]; then docker compose up -d postgres; \
	else echo "· docker-compose.yml non ancora presente (issue 3)"; fi

.PHONY: db-down
db-down: ## Ferma PostgreSQL locale
	@if [ -f docker-compose.yml ]; then docker compose down; fi

.PHONY: migrate
migrate: ## Applica le migrazioni
	$(call if_dir,$(GO_DIR),go run ./cmd/migrate up)

# ---------------------------------------------------------------- qualità

.PHONY: fmt
fmt: ## Formatta il codice
	$(call if_dir,$(GO_DIR),gofmt -w .)
	@if [ -f package.json ]; then pnpm -r --if-present run format; fi

.PHONY: lint
lint: ## Analisi statica
	$(call if_dir,$(GO_DIR),go vet ./...)
	$(call if_dir,$(GO_DIR),test -z "$$(gofmt -l .)" || { echo "gofmt: file non formattati"; gofmt -l .; exit 1; })
	@if [ -f package.json ]; then pnpm -r --if-present run lint; fi

.PHONY: typecheck
typecheck: ## Controllo dei tipi TypeScript
	@if [ -f package.json ]; then pnpm -r --if-present run typecheck; fi

.PHONY: test
test: ## Test unitari e di integrazione
	$(call if_dir,$(GO_DIR),go test ./... -race -count=1)
	@if [ -f package.json ]; then pnpm -r --if-present run test; fi

.PHONY: build
build: ## Build di backend e frontend
	$(call if_dir,$(GO_DIR),go build ./...)
	$(call if_dir,$(WEB_DIR),pnpm run generate)
	$(call if_dir,$(DSH_DIR),pnpm run generate)

.PHONY: secrets
secrets: ## Verifica che non ci siano segreti nel diff
	@if command -v gitleaks >/dev/null; then gitleaks protect --staged --no-banner || exit 1; \
	else echo "· gitleaks non installato, controllo saltato"; fi

# ---------------------------------------------------------------- pipeline

.PHONY: ci
ci: lint typecheck test build secrets ## Pipeline completa — deve passare prima del merge
	@echo ""
	@echo "  ✓ CI locale superata"

.PHONY: dev
dev: ## Avvia backend e frontend in parallelo
	@if [ -f package.json ]; then pnpm run dev; \
	else echo "· scaffold non ancora presente (issue 1)"; fi

.PHONY: clean
clean: ## Rimuove artefatti di build
	@rm -rf $(WEB_DIR)/.output $(WEB_DIR)/.nuxt $(DSH_DIR)/.output $(DSH_DIR)/.nuxt
	@# `nuxi generate` lascia un symlink `dist` -> `.output/public`: senza
	@# rimuoverlo resterebbe pendente.
	@rm -f $(WEB_DIR)/dist $(DSH_DIR)/dist
	$(call if_dir,$(GO_DIR),go clean -cache -testcache)
