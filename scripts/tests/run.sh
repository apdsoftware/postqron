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

# I log delle corse finte finiscono sotto $TMP: il selftest non deve scrivere in
# .ci-logs del worktree, né far rotare le corse vere di chi sta indagando un
# fallimento.
run_parallel() {
  (cd "$TMP/make" && CI_LOG_DIR="$TMP/ci-logs" "$ROOT/scripts/ci-parallel.sh" "$@")
}

# Come sopra, ma con radice dei log e tetti di rotazione espliciti.
# `logs keep_verdi keep_rosse -- target…`
run_parallel_in() {
  local logs=$1 keep_ok=$2 keep_ko=$3
  shift 4 # logs, keep_ok, keep_ko, "--"
  (cd "$TMP/make" && CI_LOG_DIR="$logs" \
    CI_LOG_KEEP_PASSED="$keep_ok" CI_LOG_KEEP_FAILED="$keep_ko" \
    "$ROOT/scripts/ci-parallel.sh" "$@")
}

conta_corse() { ls -1d "$1"/*/ 2>/dev/null | wc -l | tr -d ' '; }

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

# ------------------------------------------------- conservazione (issue #497)
#
# Il difetto storico: il log del job fallito veniva stampato e poi cancellato
# insieme alla directory temporanea. Su un fallimento intermittente vuol dire che
# alla riesecuzione — verde — non resta niente da indagare, ed è andata così tre
# volte di fila sullo stesso flake (#489). Stampare non basta: deve *restare*.

conserva="$TMP/ci-logs-conserva"
uscita=$(run_parallel_in "$conserva" 3 10 -- ok rotto 2>&1)

if grep -rq 'errore atteso' "$conserva" 2>/dev/null; then
  pass "conserva su disco l'output del job fallito"
else
  fail "l'output del job fallito non è rimasto su disco sotto $conserva"
fi

# Un log conservato che chi legge deve andare a cercare è conservato a metà.
case "$uscita" in
  *"$conserva"*) pass "il riepilogo indica dove sta il log conservato" ;;
  *) fail "il riepilogo non nomina il percorso dei log" ;;
esac

# Contesto della corsa: senza revisione e senza l'elenco dei componenti in
# parallelo, un log di ieri dice cosa è fallito ma non su cosa.
contesto=$(cat "$conserva"/*/corsa.txt 2>/dev/null)
if printf '%s\n' "$contesto" | grep -q 'revisione' \
  && printf '%s\n' "$contesto" | grep -q 'parallelo.*rotto'; then
  pass "registra revisione e componenti della corsa"
else
  fail "corsa.txt non riporta revisione e componenti in parallelo"
fi

# Anche i job passati lasciano il loro log: quando un componente fallisce per
# colpa di un altro (le connessioni PostgreSQL esaurite di #489 fanno
# esattamente questo) l'output da leggere è quello di chi *non* è fallito.
if ls "$conserva"/*/ok.log >/dev/null 2>&1; then
  pass "conserva anche il log dei job passati"
else
  fail "il log del job passato non è stato conservato"
fi

# ------------------------------------------------------------------ rotazione
#
# Senza tetto la directory cresce a ogni corsa. Con un tetto *unico* invece le
# riesecuzioni verdi — la prima cosa che si fa davanti a un rosso intermittente —
# spingerebbero fuori proprio il log da leggere: i due tetti sono separati per
# questo.

rotazione="$TMP/ci-logs-rotazione"
run_parallel_in "$rotazione" 1 10 -- ok rotto >/dev/null 2>&1
rossa=$(ls -1d "$rotazione"/*/ 2>/dev/null | head -1)
rossa=${rossa%/}
for _ in 1 2 3 4; do
  run_parallel_in "$rotazione" 1 10 -- ok >/dev/null 2>&1
done
if [ -d "$rossa" ]; then
  pass "quattro corse verdi non cancellano la rossa"
else
  fail "la corsa rossa è stata rotata via da corse verdi"
fi
if [ "$(conta_corse "$rotazione")" = "2" ]; then
  pass "delle corse verdi resta solo l'ultima"
else
  fail "atteso 1 rossa + 1 verde, trovate $(conta_corse "$rotazione") corse"
fi

# L'altro lato: neanche le rosse si accumulano senza fine.
tetto="$TMP/ci-logs-tetto"
for _ in 1 2 3 4; do
  run_parallel_in "$tetto" 3 2 -- ok rotto >/dev/null 2>&1
done
if [ "$(conta_corse "$tetto")" = "2" ]; then
  pass "le corse rosse si fermano al tetto di rotazione"
else
  fail "tetto rosse=2 ma dopo quattro fallimenti ci sono $(conta_corse "$tetto") corse"
fi

echo "→ vulnerabilità note (issue #511)"

# Le due fixture sono output reali di `govulncheck -format json ./...` su questo
# modulo, ridotti agli OSV citati dai finding: una scattata con go1.26.5 (le
# sette della issue, tutte raggiungibili) e una con go1.26.6 (nessuna
# raggiungibile, resta la sola GO-2026-5932 su x/crypto, che non chiamiamo e per
# cui non esiste una versione corretta).
#
# Il caso che conta è il secondo: dimostra che il controllo *non* si ferma su una
# vulnerabilità irraggiungibile. Se lo facesse, la CI sarebbe rossa per sempre
# per un pacchetto che non importiamo — e sarebbe disattivata entro la settimana.
fixtures="$ROOT/scripts/tests/fixtures"
report() { node "$ROOT/scripts/vulncheck-report.mjs" <"$1"; }

expect_exit 1 "fallisce sulle vulnerabilità raggiungibili" \
  -- report "$fixtures/govulncheck-raggiungibili.json"
expect_exit 0 "non fallisce su una vulnerabilità che non chiamiamo" \
  -- report "$fixtures/govulncheck-irraggiungibili.json"

# Fallire non basta: deve dire dove. Un rapporto senza il file da aprire manda a
# rieseguire govulncheck a mano, ed è il momento in cui il controllo diventa un
# fastidio.
rapporto=$(report "$fixtures/govulncheck-raggiungibili.json" 2>&1)
case "$rapporto" in
  *"internal/httpexec/execute.go:56"*) pass "il rapporto indica il punto di chiamata" ;;
  *) fail "il rapporto non nomina il file da cui parte la chiamata" ;;
esac
case "$rapporto" in
  *GO-2026-5932*) pass "elenca anche le irraggiungibili, senza farne un blocco" ;;
  *) fail "le vulnerabilità irraggiungibili spariscono dal rapporto" ;;
esac

# Le tre forme di verde falso, che sono il vero guasto da evitare: un output
# vuoto, uno troncato a metà e uno prodotto da una scansione che non ha risolto i
# simboli (`-scan module`), dove *niente* risulterebbe raggiungibile.
expect_exit 2 "un output vuoto non è un verde" -- report /dev/null

troncato="$TMP/troncato.json"
head -c 400 "$fixtures/govulncheck-raggiungibili.json" >"$troncato"
expect_exit 2 "un output troncato non è un verde" -- report "$troncato"

livello="$TMP/livello-modulo.json"
sed 's/"scan_level": "symbol"/"scan_level": "module"/' \
  "$fixtures/govulncheck-irraggiungibili.json" >"$livello"
expect_exit 2 "una scansione senza simboli non è un verde" -- report "$livello"

# Rete assente: il controllo non deve né fallire né tacere. Qui il database è
# irraggiungibile davvero — la porta 1 di localhost non risponde — e la sonda
# dello script se ne accorge prima di govulncheck, che direbbe soltanto
# «creating client: unrecognized vulndb format».
senza_rete() { VULN_DB=http://127.0.0.1:1 VULN_PROBE_TIMEOUT=3 "$ROOT/scripts/vulncheck.sh"; }
expect_exit 3 "senza database esce 3 (degradato), non 0 e non 1" -- senza_rete

avviso=$(senza_rete 2>&1)
case "$avviso" in
  *"CONTROLLO NON ESEGUITO"*) pass "senza database lo dice a chiare lettere" ;;
  *) fail "il degrado non è visibile nell'output" ;;
esac

# E il degrado deve arrivare fino al riepilogo della corsa: un job degradato non
# è un fallimento (la CI resta verde) ma non è nemmeno un ✓. Il target finto usa
# lo stesso protocollo del vero — il file in CI_DEGRADED_MARKER — perché il
# codice di uscita non uscirebbe da make, che esce 2 e basta.
cat >>"$TMP/make/Makefile" <<'MAKEFILE'
degradato:
	@echo "controllo non eseguito"; : >"$$CI_DEGRADED_MARKER"
MAKEFILE

expect_exit 0 "un job degradato non fa fallire la corsa" -- run_parallel ok degradato

degradi="$TMP/ci-logs-degradati"
riepilogo=$(run_parallel_in "$degradi" 3 10 -- ok degradato 2>&1)
case "$riepilogo" in
  *"⚠ degradato"*) pass "il riepilogo marca il job degradato con ⚠" ;;
  *) fail "il job degradato compare nel riepilogo come gli altri" ;;
esac
case "$riepilogo" in
  *"controllo non eseguito"*) pass "stampa l'output del job degradato anche a corsa verde" ;;
  *) fail "l'output del job degradato non viene stampato" ;;
esac
if [ -f "$degradi/degradato-ultima-corsa.txt" ]; then
  pass "lascia il promemoria che \`make ci\` ripete in fondo"
else
  fail "manca il promemoria dei controlli degradati sotto $degradi"
fi
# Il promemoria sta a percorso fisso: se sopravvivesse alla corsa successiva,
# `make ci` segnalerebbe per sempre un degrado avvenuto una volta sola.
run_parallel_in "$degradi" 3 10 -- ok >/dev/null 2>&1
if [ -f "$degradi/degradato-ultima-corsa.txt" ]; then
  fail "il promemoria è sopravvissuto a una corsa senza degradi"
else
  pass "una corsa pulita cancella il promemoria precedente"
fi

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

# La misura di R53-bis in locale dipende da --compress: se smettesse di
# comprimere in silenzio, il punteggio Lighthouse crollerebbe e sembrerebbe una
# regressione del sito. Serve anche il verso opposto — la modalità di default
# non deve comprimere, perché i tetti di peso degli e2e contano byte di build.
printf 'body { color: red }\n' >"$TMP/site/style.css"
head -c 4096 /dev/zero | tr '\0' 'a' >"$TMP/site/font.woff2"

if start_server --compress; then
  encoding=$(curl -s -o /dev/null -D - -H 'accept-encoding: br, gzip' \
    "http://127.0.0.1:$SERVER_PORT/style.css" | sed -n 's/^[Cc]ontent-[Ee]ncoding: *\([a-z]*\).*/\1/p')
  [ "$encoding" = "br" ] \
    && pass "con --compress il CSS esce in brotli" \
    || fail "con --compress il CSS dovrebbe uscire in brotli (ha risposto '$encoding')"

  encoding=$(curl -s -o /dev/null -D - -H 'accept-encoding: gzip' \
    "http://127.0.0.1:$SERVER_PORT/" | sed -n 's/^[Cc]ontent-[Ee]ncoding: *\([a-z]*\).*/\1/p')
  [ "$encoding" = "gzip" ] \
    && pass "senza brotli fra le codifiche accettate ricade su gzip" \
    || fail "avrebbe dovuto ricadere su gzip (ha risposto '$encoding')"

  # Il corpo deve restare leggibile: comprimere senza dichiararlo, o dichiararlo
  # senza comprimere, darebbe misure plausibili e sbagliate.
  # La decompressione la fa Node e non `curl --compressed`: il curl di sistema
  # non supporta brotli ovunque, e il test fallirebbe per la macchina.
  body=$(curl -s -H 'accept-encoding: br' "http://127.0.0.1:$SERVER_PORT/style.css" \
    | node -e 'const c=[];process.stdin.on("data",d=>c.push(d)).on("end",()=>process.stdout.write(require("node:zlib").brotliDecompressSync(Buffer.concat(c))))')
  body=${body%$'\n'}
  [ "$body" = "body { color: red }" ] \
    && pass "il corpo compresso si decomprime nell'originale" \
    || fail "il corpo compresso non torna l'originale (ha dato '$body')"

  encoding=$(curl -s -o /dev/null -D - -H 'accept-encoding: br, gzip' \
    "http://127.0.0.1:$SERVER_PORT/font.woff2" | sed -n 's/^[Cc]ontent-[Ee]ncoding: *\([a-z]*\).*/\1/p')
  [ -z "$encoding" ] \
    && pass "i formati già compressi restano intatti" \
    || fail "woff2 non va ricompresso (ha risposto '$encoding')"

  kill "$SERVER_PID" 2>/dev/null
  wait "$SERVER_PID" 2>/dev/null
else
  fail "il server statico non è partito in modalità compress"
fi

if start_server; then
  encoding=$(curl -s -o /dev/null -D - -H 'accept-encoding: br, gzip' \
    "http://127.0.0.1:$SERVER_PORT/style.css" | sed -n 's/^[Cc]ontent-[Ee]ncoding: *\([a-z]*\).*/\1/p')
  [ -z "$encoding" ] \
    && pass "senza --compress il corpo esce non compresso" \
    || fail "senza --compress non deve esserci content-encoding (ha risposto '$encoding')"

  kill "$SERVER_PID" 2>/dev/null
  wait "$SERVER_PID" 2>/dev/null
else
  fail "il server statico non è ripartito in modalità semplice"
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
