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
# Uso: scripts/ci-parallel.sh <target-make> [<target-make>...]

set -uo pipefail

if [ $# -eq 0 ]; then
  echo "uso: $0 <target-make> [<target-make>...]" >&2
  exit 2
fi

# `make` invoca lo script esportando MAKE: rispettarlo evita di lanciare un
# eseguibile diverso da quello con cui è partita la pipeline.
MAKE_BIN=${MAKE:-make}

# I sotto-make non devono ereditare i flag del padre (in particolare un
# eventuale jobserver, che senza il descrittore giusto stampa warning).
unset MAKEFLAGS MFLAGS MAKELEVEL

targets=("$@")
logdir=$(mktemp -d "${TMPDIR:-/tmp}/postqron-ci.XXXXXX")
trap 'rm -rf "$logdir"' EXIT

echo "→ in parallelo: ${targets[*]}"

pids=()
for target in "${targets[@]}"; do
  (
    start=$SECONDS
    "$MAKE_BIN" "$target" >"$logdir/$target.log" 2>&1
    status=$?
    echo $((SECONDS - start)) >"$logdir/$target.time"
    exit $status
  ) &
  pids+=("$!")
done

failed=()
for i in "${!targets[@]}"; do
  target=${targets[$i]}
  if wait "${pids[$i]}"; then
    ok=1
  else
    ok=0
    failed+=("$target")
  fi
  elapsed=$(cat "$logdir/$target.time" 2>/dev/null || echo '?')
  if [ "$ok" -eq 1 ]; then
    printf '  ✓ %-14s %ss\n' "$target" "$elapsed"
  else
    printf '  ✗ %-14s %ss\n' "$target" "$elapsed"
  fi
done

if [ ${#failed[@]} -eq 0 ]; then
  exit 0
fi

# Solo i falliti stampano tutto: è l'unico output che serve davvero leggere.
for target in "${failed[@]}"; do
  echo ""
  echo "──────── output di $target ────────"
  cat "$logdir/$target.log"
done

echo ""
echo "✗ componenti falliti: ${failed[*]}"
exit 1
