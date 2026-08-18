# PostQron — CI locale.
# La Continuous Integration gira esclusivamente qui: nessun workflow GitHub.
# Vedi AGENTS.md §2.

SHELL := /bin/bash
.DEFAULT_GOAL := help

# ------------------------------------------------------- manifest dei componenti
#
# Questo elenco è il contratto fra il monorepo e la CI, ed è l'unico posto da
# aggiornare quando il repository cresce.
#
# La versione precedente non aveva un manifest: ogni comando girava dentro una
# funzione `if_dir` che, se la directory non c'era, stampava «salto» e usciva con
# 0. Era la scelta giusta a repository vuoto, quando i componenti non esistevano
# ancora e la CI doveva restare verde comunque. Ora esistono tutti, e quella
# stessa funzione è il modo più economico di ottenere una CI verde che non ha
# eseguito niente: basta rinominare una cartella. La CI ora *pretende* i
# componenti che sa esistere — `make preflight` fallisce se non li trova, e
# fallisce anche se ne trova di nuovi non dichiarati qui.

GO_DIR  := services/api
JS_APPS := web dashboard

REQUIRED_DIRS  := $(GO_DIR) $(addprefix apps/,$(JS_APPS)) db/migrations e2e scripts
REQUIRED_FILES := package.json pnpm-workspace.yaml go.work tsconfig.base.json \
                  docker-compose.yml .env.example playwright.config.ts
JS_SCRIPTS     := lint typecheck test generate

# Il preflight legge il manifest dall'ambiente.
export REQUIRED_DIRS REQUIRED_FILES JS_APPS JS_SCRIPTS

# Radice degli artefatti della CI. Dichiarata qui e non solo dentro
# ci-parallel.sh perché `make ci` deve leggere allo stesso percorso il
# promemoria dei controlli degradati (vedi il target `ci`).
CI_LOG_DIR ?= $(CURDIR)/.ci-logs
export CI_LOG_DIR

.PHONY: help
help: ## Mostra questo elenco
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- setup

.PHONY: setup
setup: ## Installa le dipendenze e prepara l'ambiente
	@command -v pnpm >/dev/null || { echo "pnpm non installato"; exit 1; }
	@command -v go   >/dev/null || { echo "go non installato"; exit 1; }
	@command -v gitleaks >/dev/null || { \
		echo "gitleaks non installato — serve alla CI (\`brew install gitleaks\`)"; exit 1; }
	@# govulncheck si installa con `go install`, che lo mette in $$(go env
	@# GOPATH)/bin senza toccare l'installazione di sistema — e quella directory
	@# non è nel PATH di tutte le shell, quindi si guarda anche lì (come fa il
	@# preflight).
	@command -v govulncheck >/dev/null || [ -x "$$(go env GOPATH)/bin/govulncheck" ] || { \
		echo "govulncheck non installato — serve alla CI:"; \
		echo "  go install golang.org/x/vuln/cmd/govulncheck@latest"; exit 1; }
	pnpm install --frozen-lockfile
	go work sync
	@# Il browser dei test e2e. Scarica solo se manca dalla cache di Playwright.
	pnpm exec playwright install chromium
	@$(MAKE) hooks

.PHONY: hooks
hooks: ## Installa l'hook pre-push che esegue `make ci`
	@# core.hooksPath e non .git/hooks: in un worktree `.git` è un file, non una
	@# directory, quindi `mkdir -p .git/hooks` fallirebbe — ed è da un worktree
	@# che lavora ogni agente (AGENTS.md §3). Il percorso è relativo, e git lo
	@# risolve rispetto alla radice del worktree corrente: un hook, tutti i
	@# worktree, e versionato in .githooks/ dove si può leggere in review.
	@chmod +x .githooks/*
	@git config core.hooksPath .githooks
	@echo "✓ hook pre-push attivo (core.hooksPath=.githooks)"

# ---------------------------------------------------------------- database

.PHONY: db-up
db-up: ## Avvia PostgreSQL locale
	docker compose up -d --wait postgres

.PHONY: db-down
db-down: ## Ferma PostgreSQL locale
	docker compose down

.PHONY: db-guard
db-guard: ## Verifica che POSTGRES_PORT sia il container di PostQron
	@scripts/db-guard.sh .

.PHONY: db-check
db-check: ## Smoke test dell'ambiente database
	@scripts/db-check.sh .

.PHONY: migrate
migrate: db-guard ## Applica le migrazioni
	@# La guardia sopra non è cerimoniale: se su POSTGRES_PORT c'è un altro
	@# PostgreSQL, `db-up` fallisce ma le migrazioni si connetterebbero comunque —
	@# e con credenziali di default combacianti finirebbero sul database di un
	@# altro progetto, senza un errore.
	@if [ ! -d $(GO_DIR)/cmd/migrate ]; then \
		echo "✗ il tool di migrazione non esiste ancora: $(GO_DIR)/cmd/migrate"; \
		echo "  Arriva con la issue #386 (schema PostgreSQL iniziale)."; \
		exit 1; \
	fi
	cd $(GO_DIR) && go run ./cmd/migrate up

# ---------------------------------------------------------------- formattazione

.PHONY: fmt
fmt: ## Formatta il codice
	cd $(GO_DIR) && gofmt -w .
	pnpm exec prettier --write .
	@for app in $(JS_APPS); do pnpm --filter @postqron/$$app run format || exit 1; done

# ------------------------------------------------------------- fasi per componente
#
# Le fasi esistono sia in forma trasversale (`make lint`) sia per componente
# (`make lint-web`). Le prime servono a chi lavora, le seconde alla CI: è per
# componente che la pipeline parallelizza, perché dentro una singola app Nuxt le
# fasi condividono la directory .nuxt e non possono girare insieme.
#
# Nessuna di queste ricette usa `--if-present`: uno script rinominato deve
# rompere la CI, non svuotarla in silenzio. Il preflight verifica in anticipo che
# ogni package.json dichiari tutti gli script di JS_SCRIPTS.

.PHONY: lint-go
lint-go:
	cd $(GO_DIR) && go vet ./...
	@cd $(GO_DIR) && test -z "$$(gofmt -l .)" \
		|| { echo "✗ gofmt: file non formattati (\`make fmt\`):"; gofmt -l .; exit 1; }

.PHONY: test-go
test-go:
	cd $(GO_DIR) && go test ./... -race -count=1

.PHONY: build-go
build-go:
	cd $(GO_DIR) && go build ./...

.PHONY: lint-format
lint-format:
	@# Prettier copre solo i JSON e gli YAML di root (vedi .prettierignore):
	@# il resto ha già gofmt o ESLint stylistic.
	pnpm exec prettier --check .

.PHONY: openapi
openapi: ## Valida il contratto OpenAPI dell'API pubblica (R51)
	@# Il contratto è verificato da due controlli che rispondono a due domande
	@# diverse, e nessuno dei due copre l'altro: questo dice se il documento è
	@# leggibile dagli strumenti, `test-go` dice se è vero — vedi
	@# services/api/internal/httpapi/contract_test.go.
	@node scripts/openapi-validate.mjs

.PHONY: typecheck-ci
typecheck-ci:
	@# Il codice della CI stessa — configurazione Playwright, test e2e, server
	@# statico — non appartiene a nessuna delle due app e resterebbe altrimenti
	@# l'unico TypeScript del repository che nessuno controlla.
	pnpm exec tsc -p e2e --noEmit

# Una regola per app, generata da pattern. Deliberatamente *non* dichiarate
# .PHONY: make salta la ricerca delle regole implicite sui target phony, e
# `lint-web` non verrebbe più risolto da `lint-%`. Nessun file si chiama così,
# quindi girano comunque a ogni invocazione.

lint-%:
	pnpm --filter @postqron/$* run lint

typecheck-%:
	pnpm --filter @postqron/$* run typecheck

test-%:
	pnpm --filter @postqron/$* run test

build-%:
	pnpm --filter @postqron/$* run generate

# ------------------------------------------------------------- fasi trasversali

.PHONY: lint
lint: lint-go lint-format $(addprefix lint-,$(JS_APPS)) ## Analisi statica

.PHONY: typecheck
typecheck: typecheck-ci $(addprefix typecheck-,$(JS_APPS)) ## Controllo dei tipi TypeScript

.PHONY: test
test: test-go $(addprefix test-,$(JS_APPS)) ## Test unitari e di integrazione

.PHONY: build
build: build-go $(addprefix build-,$(JS_APPS)) ## Build di backend e frontend

.PHONY: e2e
e2e: ## Test end-to-end dei frontend sull'output statico
	@for app in $(JS_APPS); do \
		[ -d "apps/$$app/.output/public" ] \
			|| { echo "✗ manca apps/$$app/.output/public — esegui \`make build\`"; exit 1; }; \
	done
	pnpm exec playwright test

.PHONY: vulncheck
vulncheck: ## Vulnerabilità note nelle dipendenze Go (govulncheck)
	@# Fallisce solo sulle vulnerabilità che il nostro codice raggiunge davvero, e
	@# quando la rete non c'è esce 3 invece di mentire. Il perché di entrambe le
	@# scelte sta in scripts/vulncheck.sh.
	@#
	@# Il 3 si ferma qui: senza rete il controllo non ha potuto controllare, e non
	@# è un motivo per rifiutare un push. Diventa un file, perché è l'unico
	@# segnale che esce da una ricetta — make esce 2 qualunque cosa restituisca il
	@# comando, quindi un codice di uscita non arriverebbe mai al runner. Il
	@# percorso lo assegna scripts/ci-parallel.sh, che sul quel file marca il job
	@# ⚠ nel riepilogo; fuori dalla CI la variabile non c'è e resta il solo
	@# avviso a schermo.
	@scripts/vulncheck.sh $(GO_DIR); rc=$$?; \
	if [ $$rc -ne 3 ]; then exit $$rc; fi; \
	if [ -n "$$CI_DEGRADED_MARKER" ]; then : >"$$CI_DEGRADED_MARKER"; fi

.PHONY: secrets
secrets: ## Verifica che non ci siano segreti nel codice
	@# La superficie da controllare sono i commit in partenza. Le due alternative
	@# ovvie sono state entrambe provate e sono entrambe sbagliate: `gitleaks dir .`
	@# includeva `.env` e i worktree delle altre issue, `gitleaks git` senza
	@# intervallo l'intero archivio legacy. Dettaglio in scripts/secrets-scan.sh.
	@scripts/secrets-scan.sh

.PHONY: preflight
preflight: ## Verifica strumenti e layout del monorepo
	@scripts/preflight.sh .

.PHONY: ci-selftest
ci-selftest: ## Test degli script della CI
	@scripts/tests/run.sh

# ---------------------------------------------------------------- pipeline
#
# Un componente per job, i job in parallelo. La partizione non è per fase (prima
# tutti i lint, poi tutti i test) perché dentro una stessa app Nuxt le fasi si
# contendono .nuxt; fra componenti diversi non c'è niente di condiviso.
#
# Gli e2e restano a valle: hanno bisogno dell'output di `nuxt generate` di
# entrambe le app, quindi non possono partire finché le build non sono finite.

CI_JOBS := ci-go ci-web ci-dashboard ci-root ci-vuln

.PHONY: ci-go
ci-go: lint-go build-go test-go

# govulncheck sta in un job suo e non dentro ci-go per due motivi. Ricompila il
# modulo per intero: in coda a lint/build/test allungherebbe il componente più
# lungo, in parallelo alle build Nuxt il tempo aggiunto alla corsa è quasi tutto
# assorbito. E il suo codice di uscita 3 significa «non ho potuto controllare»
# (vedi scripts/ci-parallel.sh): dentro una ricetta con altri comandi sarebbe
# indistinguibile da un 3 qualsiasi.
.PHONY: ci-vuln
ci-vuln: vulncheck

.PHONY: ci-web
ci-web: lint-web typecheck-web test-web build-web

.PHONY: ci-dashboard
ci-dashboard: lint-dashboard typecheck-dashboard test-dashboard build-dashboard

.PHONY: ci-root
ci-root: lint-format typecheck-ci openapi secrets ci-selftest

.PHONY: ci
ci: ## Pipeline completa — deve passare prima del merge
	@$(MAKE) preflight
	@scripts/ci-parallel.sh $(CI_JOBS)
	@$(MAKE) e2e
	@echo ""
	@echo "  ✓ CI locale superata"
	@# Un controllo che non ha potuto controllare (govulncheck senza rete) lo dice
	@# nel riepilogo dei job, ma fra quel riepilogo e qui ci sono gli e2e: dopo
	@# qualche centinaio di righe «✓ CI locale superata» sarebbe l'unica cosa che
	@# resta letta. Il promemoria viene cancellato all'inizio di ogni corsa.
	@[ -f "$(CI_LOG_DIR)/degradato-ultima-corsa.txt" ] \
		&& cat "$(CI_LOG_DIR)/degradato-ultima-corsa.txt" || true

# ---------------------------------------------------------------- sviluppo

.PHONY: dev
dev: ## Avvia backend e frontend in parallelo
	pnpm run dev

.PHONY: clean
clean: ## Rimuove artefatti di build
	@rm -rf $(addsuffix /.output,$(addprefix apps/,$(JS_APPS)))
	@rm -rf $(addsuffix /.nuxt,$(addprefix apps/,$(JS_APPS)))
	@# `nuxi generate` lascia un symlink `dist` -> `.output/public`: senza
	@# rimuoverlo resterebbe pendente.
	@rm -f $(addsuffix /dist,$(addprefix apps/,$(JS_APPS)))
	@rm -rf playwright-report test-results
	cd $(GO_DIR) && go clean -cache -testcache
