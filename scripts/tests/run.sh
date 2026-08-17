#!/usr/bin/env bash
#
# Test degli script della CI locale (`make ci-selftest`).
#
# La CI verifica il codice del prodotto; questa suite verifica la CI. Serve
# perché il modo in cui una pipeline fallisce davvero non è stampando un errore:
# è passando quando non dovrebbe. Un preflight che non si accorge di una
# directory rinominata, un runner parallelo che perde il codice di uscita di un
# job, un fallback SPA che risponde 200 a tutto: sono tutti guasti che nessun
# altro test del repository intercetta, perché nessun altro test esercita questi
# script.
#
# I casi girano su alberi finti sotto una directory temporanea: nessuno tocca il
# worktree.

set -uo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT" || exit 2

TMP=$(mktemp -d "${TMPDIR:-/tmp}/postqron-selftest.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

passed=0
failed=0

pass() { printf '  ✓ %s\n' "$1"; passed=$((passed + 1)); }
fail() { printf '  ✗ %s\n' "$1" >&2; failed=$((failed + 1)); }

# Confronta l'esito atteso con quello ottenuto. `expect_exit 0 nome -- comando…`
expect_exit() {
  local want=$1 name=$2
  shift 3 # want, name, "--"
  local output
  output=$("$@" 2>&1)
  local got=$?
  if [ "$got" -eq "$want" ]; then
    pass "$name"
  else
    fail "$name (uscita attesa $want, ottenuta $got)"
    printf '%s\n' "$output" | sed 's/^/      | /' >&2
  fi
}

# Crea un albero minimo che il preflight dovrebbe accettare.
# $1 = directory di destinazione
make_fixture() {
  local dir=$1
  mkdir -p "$dir/apps/web" "$dir/apps/dashboard" "$dir/services/api" \
    "$dir/db/migrations" "$dir/e2e" "$dir/scripts" "$dir/node_modules"
  touch "$dir/package.json" "$dir/pnpm-workspace.yaml" "$dir/go.work" \
    "$dir/tsconfig.base.json" "$dir/docker-compose.yml" "$dir/.env.example" \
    "$dir/playwright.config.ts"
  local app
  for app in web dashboard; do
    cat >"$dir/apps/$app/package.json" <<'JSON'
{ "scripts": { "lint": "x", "typecheck": "x", "test": "x", "generate": "x" } }
JSON
  done
}

# Il manifest dei componenti, uguale a quello del Makefile. Le fixture non hanno
# la toolchain installata, quindi il controllo degli strumenti si disattiva: qui
# interessa il layout.
run_preflight() {
  REQUIRED_TOOLS="" \
  REQUIRED_FILES="package.json pnpm-workspace.yaml go.work tsconfig.base.json docker-compose.yml .env.example playwright.config.ts" \
  REQUIRED_DIRS="services/api apps/web apps/dashboard db/migrations e2e scripts" \
  JS_APPS="web dashboard" \
  JS_SCRIPTS="lint typecheck test generate" \
    "$ROOT/scripts/preflight.sh" "$1"
}

echo "→ preflight"

make_fixture "$TMP/ok"
expect_exit 0 "accetta un albero completo" -- run_preflight "$TMP/ok"

# Il caso che ha motivato la riscrittura: con `if_dir` una directory rinominata
# faceva passare la CI senza eseguire nulla.
make_fixture "$TMP/rinominata"
mv "$TMP/rinominata/apps/web" "$TMP/rinominata/apps/sito"
expect_exit 1 "rifiuta un'app rinominata" -- run_preflight "$TMP/rinominata"

make_fixture "$TMP/modulo-mancante"
rm -rf "$TMP/modulo-mancante/services/api"
expect_exit 1 "rifiuta un modulo Go mancante" -- run_preflight "$TMP/modulo-mancante"

make_fixture "$TMP/file-mancante"
rm -f "$TMP/file-mancante/go.work"
expect_exit 1 "rifiuta un file di workspace mancante" -- run_preflight "$TMP/file-mancante"

# L'altra metà del buco: un'app in più, che nessun target della CI toccherebbe.
make_fixture "$TMP/app-extra"
mkdir -p "$TMP/app-extra/apps/admin"
expect_exit 1 "rifiuta un'app non dichiarata nel manifest" -- run_preflight "$TMP/app-extra"

# `pnpm -r --if-present` salterebbe in silenzio uno script rinominato.
make_fixture "$TMP/script-mancante"
cat >"$TMP/script-mancante/apps/web/package.json" <<'JSON'
{ "scripts": { "lint": "x", "typecheck": "x", "generate": "x" } }
JSON
expect_exit 1 "rifiuta un package.json senza lo script test" -- run_preflight "$TMP/script-mancante"

make_fixture "$TMP/senza-deps"
rmdir "$TMP/senza-deps/node_modules"
expect_exit 1 "rifiuta un albero senza dipendenze installate" -- run_preflight "$TMP/senza-deps"

echo "→ runner parallelo"

# Un runner che perde il codice di uscita di un job è una CI che approva
# qualunque cosa: è il caso da provare per primo.
mkdir -p "$TMP/make"
# `mano-a` e `mano-b` si aspettano a vicenda: ciascuno segnala di essere partito
# e attende il segnale dell'altro. Se il runner li eseguisse in serie, il primo
# resterebbe in attesa di un segnale che non può arrivare e fallirebbe allo
# scadere del timeout. È una verifica di concorrenza deterministica: misurare
# invece il tempo totale darebbe un test che fallisce a caso quando la macchina
# è carica — e sotto `make ci` la macchina è sempre carica.
cat >"$TMP/make/Makefile" <<'MAKEFILE'
ok:
	@echo tutto bene
lento:
	@sleep 0.3; echo finito
rotto:
	@echo "errore atteso"; exit 1
mano-a:
	@touch segnale-a; for i in $$(seq 1 100); do \
		[ -f segnale-b ] && exit 0; sleep 0.1; done; \
		echo "mano-a: nessun segnale da mano-b"; exit 1
mano-b:
	@touch segnale-b; for i in $$(seq 1 100); do \
		[ -f segnale-a ] && exit 0; sleep 0.1; done; \
		echo "mano-b: nessun segnale da mano-a"; exit 1
MAKEFILE

run_parallel() { (cd "$TMP/make" && "$ROOT/scripts/ci-parallel.sh" "$@"); }

expect_exit 0 "esce 0 se tutti i job passano" -- run_parallel ok lento
expect_exit 1 "esce 1 se un job fallisce" -- run_parallel ok rotto
expect_exit 1 "esce 1 se il job fallito è l'ultimo" -- run_parallel ok lento rotto

# L'output di un job fallito deve arrivare a chi legge, altrimenti la CI dice
# solo «rosso» e tocca rieseguire tutto a mano per sapere perché.
riepilogo=$(run_parallel ok rotto 2>&1)
case "$riepilogo" in
  *"errore atteso"*) pass "stampa l'output del job fallito" ;;
  *) fail "l'output del job fallito non compare nel riepilogo" ;;
esac

expect_exit 0 "esegue i job in parallelo" -- run_parallel mano-a mano-b

echo "→ server statico dei test e2e"

mkdir -p "$TMP/site/sub"
echo '<!doctype html><h1>home</h1>' >"$TMP/site/index.html"
echo 'non trovato' >"$TMP/site/404.html"
echo 'segreto' >"$TMP/fuori.txt"

# $1 = argomenti extra; esporta SERVER_PORT e SERVER_PID
start_server() {
  local log="$TMP/server.log"
  : >"$log"
  node "$ROOT/scripts/static-server.mjs" --root "$TMP/site" --port 0 "$@" >"$log" 2>&1 &
  SERVER_PID=$!
  SERVER_PORT=""
  local i
  for i in $(seq 1 50); do
    SERVER_PORT=$(sed -n 's|.*http://127\.0\.0\.1:\([0-9]*\).*|\1|p' "$log")
    [ -n "$SERVER_PORT" ] && return 0
    sleep 0.1
  done
  return 1
}

status_of() {
  curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$SERVER_PORT$1"
}

if start_server; then
  [ "$(status_of /)" = "200" ] \
    && pass "serve index.html sulla radice" \
    || fail "la radice non risponde 200 (ha risposto $(status_of /))"

  [ "$(status_of /jobs/42)" = "404" ] \
    && pass "senza --spa una rotta profonda è 404" \
    || fail "senza --spa /jobs/42 dovrebbe essere 404 (ha risposto $(status_of /jobs/42))"

  # Traversal: il fallback su 404.html non deve mai diventare una via d'accesso
  # a file fuori dalla radice servita.
  body=$(curl -s "http://127.0.0.1:$SERVER_PORT/../fuori.txt")
  case "$body" in
    *segreto*) fail "il server serve file fuori dalla radice" ;;
    *) pass "rifiuta i percorsi fuori dalla radice" ;;
  esac

  kill "$SERVER_PID" 2>/dev/null
  wait "$SERVER_PID" 2>/dev/null
else
  fail "il server statico non è partito"
fi

if start_server --spa; then
  [ "$(status_of /jobs/42)" = "200" ] \
    && pass "con --spa una rotta profonda ricade su index.html" \
    || fail "con --spa /jobs/42 dovrebbe essere 200 (ha risposto $(status_of /jobs/42))"

  kill "$SERVER_PID" 2>/dev/null
  wait "$SERVER_PID" 2>/dev/null
else
  fail "il server statico non è partito in modalità spa"
fi

echo "→ ambiente database"

# Senza .env i target db-* devono fermarsi con un messaggio utile, non tentare
# una connessione con valori vuoti.
mkdir -p "$TMP/senza-env/scripts"
cp "$ROOT/scripts/db-env.sh" "$ROOT/scripts/db-guard.sh" "$ROOT/scripts/db-check.sh" "$TMP/senza-env/scripts/"
expect_exit 1 "db-guard fallisce senza .env" -- "$TMP/senza-env/scripts/db-guard.sh" "$TMP/senza-env"
expect_exit 1 "db-check fallisce senza .env" -- "$TMP/senza-env/scripts/db-check.sh" "$TMP/senza-env"

# Un .env incompleto è peggio di uno assente: i default farebbero puntare le
# migrazioni da qualche altra parte.
mkdir -p "$TMP/env-parziale/scripts"
cp "$ROOT/scripts/db-env.sh" "$ROOT/scripts/db-guard.sh" "$TMP/env-parziale/scripts/"
printf 'POSTGRES_HOST=127.0.0.1\nPOSTGRES_PORT=5432\n' >"$TMP/env-parziale/.env"
expect_exit 1 "db-guard fallisce con un .env incompleto" -- "$TMP/env-parziale/scripts/db-guard.sh" "$TMP/env-parziale"

echo "→ hook pre-push"

hooks_path=$(git config --get core.hooksPath || true)
if [ "$hooks_path" = ".githooks" ]; then
  pass "core.hooksPath punta a .githooks"
else
  fail "core.hooksPath è «${hooks_path:-non impostato}»: esegui \`make hooks\`"
fi

if [ -x .githooks/pre-push ]; then
  pass "l'hook pre-push è presente ed eseguibile"
else
  fail "manca .githooks/pre-push eseguibile"
fi

echo ""
if [ "$failed" -gt 0 ]; then
  echo "✗ selftest: $failed falliti su $((passed + failed))" >&2
  exit 1
fi

echo "✓ selftest: $passed controlli superati"
