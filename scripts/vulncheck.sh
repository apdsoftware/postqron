#!/usr/bin/env bash
#
# Vulnerabilità note nelle dipendenze Go (`make vulncheck`, job `ci-vuln`).
#
# Perché esiste (issue #511). Sette vulnerabilità della libreria standard, cinque
# delle quali con il percorso di chiamata che parte dal nostro codice — fra cui
# httpexec.Execute, che elabora URL scritti dall'utente, e la lettura della
# chiave privata della GitHub App — sono rimaste aperte finché qualcuno non ha
# eseguito govulncheck a mano. Il banner Dependabot su GitHub non ne ha segnalata
# nessuna: parlava di manifest del repository precedente, che non esistono più.
# Il controllo che serviva non era altrove, era qui: nella CI locale, che gira
# sull'hook pre-push a ogni push.
#
# Le tre decisioni non ovvie:
#
#   soglia   Fallisce solo sulle vulnerabilità *raggiungibili*. Il perché sta in
#            scripts/vulncheck-report.mjs, che è dove la soglia si applica.
#
#   rete     govulncheck scarica il database delle vulnerabilità. Senza rete non
#            può controllare, e le due uscite facili sono entrambe sbagliate:
#            fallire renderebbe impossibile spingere da offline (e il primo che
#            deve farlo toglie il controllo dalla CI), passare in silenzio
#            produrrebbe un verde che non ha guardato niente — che è il guasto
#            preciso da cui nasce questa issue. Quindi: uscita 3, che
#            ci-parallel.sh riporta come «controllo incompleto» sopra il
#            riepilogo e ripete in fondo a `make ci`. La corsa resta verde e chi
#            l'ha lanciata sa che quel verde non include questo controllo.
#            Senza la sonda, l'errore sarebbe l'illeggibile «creating client:
#            unrecognized vulndb format» — govulncheck dice la stessa cosa per un
#            host che non risolve, una rete assente e un URL sbagliato.
#
#   tempo    Il costo lo paga chi spinge, ogni volta. Per questo il controllo è
#            un job a sé in CI_JOBS invece di stare dentro ci-go: gira in
#            parallelo alle build Nuxt, che sono più lente, e il tempo aggiunto
#            alla corsa completa è quasi nullo. (L'altro motivo è il codice di
#            uscita: dentro ci-go, un 3 sarebbe indistinguibile da un 3 di
#            qualunque altro comando della ricetta.)
#
# Uso: scripts/vulncheck.sh [directory-del-modulo]
#
# Ambiente:
#   VULN_DB             database delle vulnerabilità (default https://vuln.go.dev)
#   VULN_PROBE_TIMEOUT  secondi concessi alla sonda di rete (default 8)
#
# Uscita: 0 pulito · 1 vulnerabilità raggiungibili o errore vero · 3 non
# controllato (rete assente).

set -uo pipefail

DEGRADATO=3

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
modulo=${1:-services/api}
db=${VULN_DB:-https://vuln.go.dev}
timeout_sonda=${VULN_PROBE_TIMEOUT:-8}

cd "$ROOT/$modulo" 2>/dev/null || {
  echo "  ✗ govulncheck: modulo inesistente: $ROOT/$modulo" >&2
  exit 1
}

# ------------------------------------------------------------------ strumento
#
# `go install` mette i binari in $(go env GOPATH)/bin, che su molte macchine non
# è nel PATH: cercarlo solo con `command -v` direbbe «non installato» a chi ce
# l'ha. Il preflight usa la stessa risoluzione.

govulncheck=$(command -v govulncheck 2>/dev/null)
if [ -z "$govulncheck" ]; then
  for candidato in "$(go env GOBIN 2>/dev/null)" "$(go env GOPATH 2>/dev/null)/bin"; do
    [ -n "$candidato" ] && [ -x "$candidato/govulncheck" ] && {
      govulncheck="$candidato/govulncheck"
      break
    }
  done
fi

if [ -z "$govulncheck" ]; then
  echo "  ✗ govulncheck non installato — serve alla CI:" >&2
  echo "      go install golang.org/x/vuln/cmd/govulncheck@latest" >&2
  exit 1
fi

# --------------------------------------------------------------------- sonda
#
# Distinguere «non ho potuto controllare» da «ho controllato e c'è un problema»
# è tutta la differenza fra i due esiti, e govulncheck da solo non la fa. La
# sonda la fa prima, e con un messaggio che dice cosa è successo.
#
# Se curl manca la sonda si salta: resta la classificazione dell'errore più
# sotto, meno precisa ma non peggiore di niente.

rete_assente=""
case "$db" in
  http://* | https://*)
    if command -v curl >/dev/null 2>&1; then
      if ! curl -fsS --max-time "$timeout_sonda" -o /dev/null "$db/index/db.json" 2>/dev/null; then
        rete_assente="la sonda su $db/index/db.json non ha risposto entro ${timeout_sonda}s"
      fi
    fi
    ;;
esac

degrada() {
  echo "  ⚠ CONTROLLO NON ESEGUITO — database delle vulnerabilità irraggiungibile"
  echo "    $1"
  echo ""
  echo "    Questa corsa NON ha verificato le vulnerabilità note: il verde qui"
  echo "    sopra non le include. Rieseguila con la rete prima di fidartene."
  exit "$DEGRADATO"
}

[ -n "$rete_assente" ] && degrada "$rete_assente"

# ----------------------------------------------------------------- scansione

grezzo=$(mktemp "${TMPDIR:-/tmp}/postqron-vulncheck.XXXXXX")
errori=$(mktemp "${TMPDIR:-/tmp}/postqron-vulncheck-err.XXXXXX")
trap 'rm -f "$grezzo" "$errori"' EXIT

# In modalità JSON govulncheck esce 0 anche quando trova vulnerabilità: qui
# un'uscita diversa da zero significa sempre che la scansione non è arrivata in
# fondo.
"$govulncheck" -db "$db" -format json ./... >"$grezzo" 2>"$errori"
stato=$?

if [ "$stato" -ne 0 ]; then
  # La rete può essere caduta fra la sonda e la scansione, e in quel caso
  # govulncheck fallisce mentre costruisce il client del database. Distinguerlo
  # da un errore vero (codice che non compila, modulo rotto) tiene fuori dal
  # degrado tutto ciò che va davvero corretto.
  if grep -qE 'creating client|no such host|connection refused|dial tcp|i/o timeout|TLS handshake' "$errori"; then
    degrada "govulncheck non è riuscito a leggere $db: $(head -1 "$errori")"
  fi
  echo "  ✗ govulncheck è fallito (uscita $stato):" >&2
  sed 's/^/      /' "$errori" >&2
  exit 1
fi

node "$ROOT/scripts/vulncheck-report.mjs" <"$grezzo"
esito=$?

# Un report che non riesce a interpretare l'output (uscita 2) non è un verde:
# vale come fallimento, perché non sappiamo che cosa c'era dentro.
[ "$esito" -eq 0 ] || exit 1
exit 0
