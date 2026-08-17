#!/usr/bin/env bash
#
# Esegue più target make in parallelo, uno per componente del monorepo.
#
# Perché non `make -j`: la CI deve restare leggibile quando fallisce, e GNU Make
# 3.81 — quello che macOS installa di default — non ha `--output-sync`. Con `-j`
# l'output dei componenti si mescola riga per riga e un errore di ESLint finisce
# in mezzo a `go vet`. Qui ogni job scrive su un log suo; a fine corsa si stampa
# una riga di riepilogo per componente e l'output completo solo dei falliti.
#
# La partizione è *per componente*, non per fase (prima tutti i lint, poi tutti i
# test): dentro apps/web le fasi condividono la directory .nuxt, che `nuxt
# prepare`, `nuxt typecheck` e `nuxt generate` riscrivono. Farle girare in
# parallelo sullo stesso componente è una corsa; farle girare in parallelo su
# componenti diversi non tocca nulla di condiviso.
#
# Perché i log *sopravvivono* alla corsa (issue #497). La prima versione teneva i
# log in una directory temporanea con `trap rm -rf EXIT`: stampava l'output del
# job fallito e poi lo cancellava. Su un fallimento intermittente questo lascia
# senza prove — il flake di #489 è stato visto tre volte da tre attori diversi e
# ogni volta la causa è andata persa, perché alla riesecuzione (verde) non
# restava niente da guardare. È stato spiegato solo quando un agente ha eseguito
# il test ottocento volte, che non è un metodo su cui si può contare. Ora ogni
# corsa scrive in una directory datata sotto .ci-logs/ che *resta*, con il
# contesto necessario a ricostruirla: ora, revisione, componenti in parallelo,
# esito e durata di ciascuno.
#
# La rotazione distingue le corse verdi dalle rosse perché non hanno lo stesso
# valore: di una corsa verde interessa solo l'ultima (serve a leggere l'output di
# un job che è comunque passato), di una rossa interessa la storia — è la prova
# di un fallimento che potrebbe non ripresentarsi. Tenere un solo tetto comune
# significherebbe che poche riesecuzioni verdi — ed è la prima cosa che si fa
# davanti a un rosso intermittente — spingono fuori proprio il log da leggere.
#
# Il terzo esito (issue #511). Un job può non essere passato né fallito: non aver
# potuto controllare. Succede a govulncheck, che senza rete non scarica il
# database delle vulnerabilità, e succederebbe a qualunque altro controllo che
# dipende da una risorsa esterna. Le due alternative sono entrambe peggiori:
# farlo fallire rende impossibile spingere da offline — e chi deve farlo toglie
# il controllo dalla CI — mentre farlo passare in silenzio produce un verde che
# non ha verificato niente, che è il guasto da cui nasce la issue. Qui la corsa
# resta verde, ma il riepilogo marca il job ⚠, ne stampa l'output e lascia il
# promemoria in `degradato-ultima-corsa.txt`, che `make ci` ripete in fondo:
# dopo gli e2e il riepilogo è scorso via da un pezzo.
#
# Il segnale è un file e non un codice di uscita perché i job sono target make, e
# make non lascia passare il codice della ricetta: qualunque comando fallisca,
# esce 2. A ogni job si assegna quindi un percorso in CI_DEGRADED_MARKER; se al
# termine quel file esiste, il job dichiara di non aver controllato.
#
# Uso: scripts/ci-parallel.sh <target-make> [<target-make>...]
#
# Ambiente:
#   CI_LOG_DIR           radice degli artefatti (default <root>/.ci-logs)
#   CI_LOG_KEEP_FAILED   corse rosse da conservare (default 10)
#   CI_LOG_KEEP_PASSED   corse verdi da conservare (default 3)

set -uo pipefail

if [ $# -eq 0 ]; then
  echo "uso: $0 <target-make> [<target-make>...]" >&2
  exit 2
fi

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# `make` invoca lo script esportando MAKE: rispettarlo evita di lanciare un
# eseguibile diverso da quello con cui è partita la pipeline.
MAKE_BIN=${MAKE:-make}

# I sotto-make non devono ereditare i flag del padre (in particolare un
# eventuale jobserver, che senza il descrittore giusto stampa warning).
unset MAKEFLAGS MFLAGS MAKELEVEL

logroot=${CI_LOG_DIR:-$ROOT/.ci-logs}
keep_failed=${CI_LOG_KEEP_FAILED:-10}
keep_passed=${CI_LOG_KEEP_PASSED:-3}

# Il promemoria della corsa precedente non deve sopravvivere a questa: sta fuori
# dalle directory datate proprio perché `make ci` lo trova a percorso fisso, e a
# percorso fisso un file vecchio è indistinguibile da uno nuovo.
promemoria="$logroot/degradato-ultima-corsa.txt"
rm -f "$promemoria"

targets=("$@")

# Il PID nel nome non è decorativo: due corse nello stesso secondo (le fa il
# selftest, e le fa chi lancia la CI due volte di fila) collidono sul solo
# timestamp. L'ordinamento per nome resta cronologico, perché il timestamp
# precede il PID.
logdir="$logroot/$(date +%Y%m%d-%H%M%S)-$$"
if ! mkdir -p "$logdir"; then
  echo "✗ non riesco a creare la directory dei log: $logdir" >&2
  exit 2
fi

# Contesto della corsa: senza revisione e stato dell'albero, un log vecchio di
# tre giorni dice cosa è fallito ma non su cosa.
revisione=$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo sconosciuta)
ramo=$(git -C "$ROOT" rev-parse --abbrev-ref HEAD 2>/dev/null || echo sconosciuto)
if [ -n "$(git -C "$ROOT" status --porcelain 2>/dev/null)" ]; then
  albero="con modifiche non committate"
else
  albero="pulito"
fi

corsa="$logdir/corsa.txt"
{
  echo "corsa CI locale"
  echo "  data       $(date '+%Y-%m-%d %H:%M:%S %z')"
  echo "  worktree   $ROOT"
  echo "  ramo       $ramo"
  echo "  revisione  $revisione ($albero)"
  echo "  parallelo  ${targets[*]}"
  echo "  make       $MAKE_BIN"
  echo "  host       $(uname -sr) — $(hostname)"
  echo ""
} >"$corsa"

echo "→ in parallelo: ${targets[*]}"
echo "  log: $logdir"

pids=()
for target in "${targets[@]}"; do
  (
    start=$SECONDS
    export CI_DEGRADED_MARKER="$logdir/$target.degradato"
    "$MAKE_BIN" "$target" >"$logdir/$target.log" 2>&1
    status=$?
    echo $((SECONDS - start)) >"$logdir/$target.time"
    exit $status
  ) &
  pids+=("$!")
done

failed=()
degraded=()
for i in "${!targets[@]}"; do
  target=${targets[$i]}
  stato=0
  wait "${pids[$i]}" || stato=$?
  elapsed=$(cat "$logdir/$target.time" 2>/dev/null || echo '?')
  if [ "$stato" -ne 0 ]; then
    failed+=("$target")
    riga=$(printf '  ✗ %-14s %ss  → %s' "$target" "$elapsed" "$logdir/$target.log")
  elif [ -f "$logdir/$target.degradato" ]; then
    degraded+=("$target")
    riga=$(printf '  ⚠ %-14s %ss  → controllo incompleto: %s' \
      "$target" "$elapsed" "$logdir/$target.log")
  else
    riga=$(printf '  ✓ %-14s %ss' "$target" "$elapsed")
  fi
  echo "$riga"
  echo "$riga" >>"$corsa"
done

# I degradati stampano l'output anche a corsa verde: è l'unico modo perché chi
# ha lanciato la CI sappia *cosa* non è stato controllato. Il riepilogo da solo
# direbbe che qualcosa manca, non che manca il controllo delle vulnerabilità.
if [ ${#degraded[@]} -gt 0 ]; then
  {
    echo ""
    echo "controlli incompleti: ${degraded[*]}"
  } >>"$corsa"
  for target in "${degraded[@]}"; do
    echo ""
    echo "──────── $target: controllo incompleto ────────"
    cat "$logdir/$target.log"
  done
  {
    echo ""
    echo "⚠ controlli incompleti: ${degraded[*]}"
    echo "  Questa corsa non li ha eseguiti: il verde non li include."
    for target in "${degraded[@]}"; do
      echo "    $logdir/$target.log"
    done
  } | tee "$promemoria"
fi

# La rotazione gira sempre, anche sulla corsa verde: è l'unico momento in cui si
# sa se questa corsa va contata fra le rosse o fra le verdi.
ruota() {
  local esito_cercato=$1 quante=$2 dir
  # Le corse interrotte (Ctrl-C, hook ucciso) non hanno il file `esito`: contano
  # come rosse, perché è lì che può nascondersi la prova di un fallimento.
  for dir in $(ls -1d "$logroot"/*/ 2>/dev/null | sort -r); do
    dir=${dir%/}
    [ "$dir" = "$logdir" ] && continue
    local esito
    esito=$(cat "$dir/esito" 2>/dev/null || echo ko)
    [ "$esito" = "$esito_cercato" ] || continue
    if [ "$quante" -gt 0 ]; then
      quante=$((quante - 1))
    else
      rm -rf "$dir"
    fi
  done
}

if [ ${#failed[@]} -eq 0 ]; then
  echo ok >"$logdir/esito"
  # -1: la corsa appena finita occupa già un posto fra quelle da conservare.
  ruota ok $((keep_passed - 1))
  ruota ko "$keep_failed"
  exit 0
fi

{
  echo ""
  echo "componenti falliti: ${failed[*]}"
} >>"$corsa"
echo ko >"$logdir/esito"
ruota ko $((keep_failed - 1))
ruota ok "$keep_passed"

# Solo i falliti stampano tutto: è l'unico output che serve davvero leggere.
for target in "${failed[@]}"; do
  echo ""
  echo "──────── output di $target ────────"
  cat "$logdir/$target.log"
done

echo ""
echo "✗ componenti falliti: ${failed[*]}"
echo ""
echo "  Output integrale conservato — sopravvive alle riesecuzioni:"
for target in "${failed[@]}"; do
  echo "    $logdir/$target.log"
done
echo "  Contesto della corsa: $corsa"
exit 1
