#!/usr/bin/env bash
#
# Carica e valida le variabili POSTGRES_* da `.env`. Va incluso con `.`, non
# eseguito: serve a esportare l'ambiente nel processo chiamante.
#
# `.env` è l'unica fonte di verità della connessione (AGENTS.md §7): host, porta,
# database, utente e password stanno lì e da nessun'altra parte. Questo file
# esiste perché db-guard.sh e db-check.sh leggano esattamente gli stessi valori
# che Docker Compose usa per pubblicare il container.

# shellcheck disable=SC1091

if [ ! -f .env ]; then
  echo "✗ manca .env — copia .env.example in .env (\`cp .env.example .env\`)" >&2
  return 1 2>/dev/null || exit 1
fi

set -a
. ./.env
set +a

_db_env_missing=""
for _var in POSTGRES_HOST POSTGRES_PORT POSTGRES_DB POSTGRES_USER POSTGRES_PASSWORD; do
  if [ -z "${!_var:-}" ]; then
    _db_env_missing="$_db_env_missing $_var"
  fi
done

if [ -n "$_db_env_missing" ]; then
  echo "✗ .env non definisce:$_db_env_missing" >&2
  return 1 2>/dev/null || exit 1
fi

unset _db_env_missing _var
