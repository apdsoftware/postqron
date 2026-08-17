#!/usr/bin/env bash
#
# Verifica che l'ambiente e il layout del monorepo siano quelli che la CI si
# aspetta, prima di eseguire qualunque controllo.
#
# Perché esiste. La prima versione del Makefile eseguiva ogni comando dentro un
# `if [ -d "$dir" ]`, e quando la directory mancava stampava «salto» uscendo con
# 0. Aveva senso a repository vuoto, quando i componenti non esistevano ancora.
# Ora esistono tutti, e quella scorciatoia è diventata il modo più semplice di
# avere una CI verde che non ha eseguito niente: basta rinominare `apps/web`,
# spostare un modulo Go o cancellare uno script da un package.json. La CI non
# deve *adattarsi* al layout che trova, deve *pretendere* quello che sa esistere
# e fallire se non c'è.
#
# Il manifest dei componenti sta nel Makefile ed arriva qui via ambiente, così
# resta un posto solo da aggiornare quando il monorepo cresce.
#
# Uso: scripts/preflight.sh [root]
#   REQUIRED_FILES  file che devono esistere (separati da spazi)
#   REQUIRED_DIRS   directory che devono esistere
#   JS_APPS         nomi delle app sotto apps/ (es. "web dashboard")
#   JS_SCRIPTS      script che ogni package.json di apps/* deve definire

set -uo pipefail

root=${1:-.}
cd "$root" || { echo "preflight: root inesistente: $root" >&2; exit 2; }

REQUIRED_FILES=${REQUIRED_FILES:-}
REQUIRED_DIRS=${REQUIRED_DIRS:-}
JS_APPS=${JS_APPS:-}
JS_SCRIPTS=${JS_SCRIPTS:-}

# Strumenti necessari a `make ci`. Docker non è nell'elenco: la CI non tocca il
# database, i target db-* si controllano da soli.
REQUIRED_TOOLS=${REQUIRED_TOOLS:-pnpm node go gitleaks govulncheck}

errors=0

fail() {
  echo "  ✗ $1" >&2
  errors=$((errors + 1))
}

# ------------------------------------------------------------------ strumenti

# `go install` non mette i binari nel PATH: li mette in $(go env GOBIN), o in
# $(go env GOPATH)/bin, che su molte macchine nel PATH non c'è. Cercare solo con
# `command -v` direbbe «govulncheck non installato» a chi ce l'ha, e la CI non
# partirebbe per un problema che non esiste.
ha_strumento() {
  command -v "$1" >/dev/null 2>&1 && return 0
  command -v go >/dev/null 2>&1 || return 1
  local gobin gopath
  gobin=$(go env GOBIN 2>/dev/null)
  gopath=$(go env GOPATH 2>/dev/null)
  [ -n "$gobin" ] && [ -x "$gobin/$1" ] && return 0
  [ -n "$gopath" ] && [ -x "$gopath/bin/$1" ] && return 0
  return 1
}

for tool in $REQUIRED_TOOLS; do
  ha_strumento "$tool" || fail "$tool non installato (vedi README § Sviluppo)"
done

# ------------------------------------------------------------------- layout

for file in $REQUIRED_FILES; do
  [ -f "$file" ] || fail "manca il file $file"
done

for dir in $REQUIRED_DIRS; do
  [ -d "$dir" ] || fail "manca la directory $dir"
done

# --------------------------------------------------------------- app frontend
#
# Non basta che le app attese esistano: serve anche che non ce ne siano altre.
# Un'app aggiunta senza toccare il manifest sarebbe fuori dalla CI e nessuno se
# ne accorgerebbe, che è esattamente il buco di prima al contrario.

if [ -d apps ] && [ -n "$JS_APPS" ]; then
  found=$(find apps -mindepth 1 -maxdepth 1 -type d -exec basename {} \; | sort | tr '\n' ' ')
  expected=$(printf '%s\n' $JS_APPS | sort | tr '\n' ' ')
  if [ "$found" != "$expected" ]; then
    fail "apps/ contiene [${found% }] ma il manifest dichiara [${expected% }]: aggiorna JS_APPS nel Makefile"
  fi
fi

# ------------------------------------------------- script attesi nei package
#
# `pnpm -r --if-present` salta in silenzio gli script che non trova: è la stessa
# trappola di if_dir un livello più in basso. Qui li pretendiamo esplicitamente,
# così rinominare `test` in `tests` rompe la CI invece di svuotarla.

for app in $JS_APPS; do
  manifest="apps/$app/package.json"
  if [ ! -f "$manifest" ]; then
    fail "manca $manifest"
    continue
  fi
  for script in $JS_SCRIPTS; do
    node -e '
      const [file, name] = process.argv.slice(1)
      const pkg = JSON.parse(require("node:fs").readFileSync(file, "utf8"))
      process.exit(pkg.scripts && pkg.scripts[name] ? 0 : 1)
    ' "$manifest" "$script" || fail "$manifest non definisce lo script \"$script\""
  done
done

# ------------------------------------------------------------- dipendenze JS

if [ ! -d node_modules ]; then
  fail "dipendenze non installate: esegui \`make setup\`"
fi

# ------------------------------------------------------------------- esito

if [ "$errors" -gt 0 ]; then
  echo "" >&2
  echo "preflight: $errors problema/i — la CI non parte su un albero incompleto." >&2
  exit 1
fi

echo "  ✓ layout e strumenti"
