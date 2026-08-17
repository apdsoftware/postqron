#!/usr/bin/env bash
#
# Smoke test dell'ambiente database locale (`make db-check`).
#
# Risponde alla domanda «il database su cui sto per lavorare è configurato come
# credo?». Non sostituisce i test: verifica l'ambiente, non il codice. È il primo
# comando da lanciare quando qualcosa nel backend si comporta in modo strano.
#
# Ogni controllo corrisponde a un'assunzione che il resto del progetto dà per
# scontata e che, se falsa, produce bug difficili da attribuire: la timezone UTC
# (il motore cron converte dai fusi dei job, R2), la codifica UTF8, la major
# version allineata alla produzione, e la raggiungibilità della porta *dall'host*
# — che è il percorso di rete che useranno API e migrazioni, non il socket Unix
# interno al container.

set -uo pipefail

cd "${1:-.}" || exit 2

# Versione major attesa, allineata a docker-compose.yml e alla VPS di produzione.
EXPECTED_MAJOR=${EXPECTED_MAJOR:-17}

errors=0

ok()   { printf '  ✓ %s\n' "$1"; }
fail() { printf '  ✗ %s\n' "$1" >&2; errors=$((errors + 1)); }

. scripts/db-env.sh || exit 1

ok "configurazione: $POSTGRES_USER@$POSTGRES_HOST:$POSTGRES_PORT/$POSTGRES_DB (sslmode=${POSTGRES_SSLMODE:-non impostato})"

# 1. È il nostro container, e risponde. Senza questo tutti i controlli sotto
#    descriverebbero il server di qualcun altro.
if ! scripts/db-guard.sh .; then
  echo "" >&2
  echo "db-check: interrotto, l'ambiente non è quello atteso." >&2
  exit 1
fi

# 2. La porta pubblicata è raggiungibile dall'host. `docker compose port` dice
#    cosa Docker ha pubblicato; questo dice che il percorso funziona davvero.
if (exec 3<>"/dev/tcp/$POSTGRES_HOST/$POSTGRES_PORT") 2>/dev/null; then
  ok "porta $POSTGRES_HOST:$POSTGRES_PORT raggiungibile dall'host"
else
  fail "porta $POSTGRES_HOST:$POSTGRES_PORT non raggiungibile dall'host"
fi

# Da qui in poi si interroga il server. `psql -tAc` restituisce il solo valore,
# senza intestazioni né allineamento.
psql_value() {
  docker compose exec -T \
    -e PGPASSWORD="$POSTGRES_PASSWORD" \
    postgres psql -h 127.0.0.1 -p 5432 \
    -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
    -tAc "$1" 2>/dev/null | tr -d '[:space:]'
}

# 3. Le credenziali di .env autenticano davvero su quel database.
if [ "$(psql_value 'select 1')" = "1" ]; then
  ok "autenticazione e query di base"
else
  fail "connessione rifiutata con le credenziali di .env — password o utente non corrispondono al container"
  echo "" >&2
  echo "db-check: $errors problema/i." >&2
  exit 1
fi

# 4. Database e utente sono quelli dichiarati, non i default dell'immagine.
actual_db=$(psql_value 'select current_database()')
[ "$actual_db" = "$POSTGRES_DB" ] \
  && ok "database corrente: $actual_db" \
  || fail "database corrente «$actual_db», atteso «$POSTGRES_DB»"

actual_user=$(psql_value 'select current_user')
[ "$actual_user" = "$POSTGRES_USER" ] \
  && ok "utente corrente: $actual_user" \
  || fail "utente corrente «$actual_user», atteso «$POSTGRES_USER»"

# 5. Timezone UTC. Il motore cron ragiona in UTC e converte per timezone del job
#    (R2): un fuso implicito nel database sposterebbe le occorrenze.
tz=$(psql_value 'show timezone')
[ "$tz" = "UTC" ] \
  && ok "timezone: UTC" \
  || fail "timezone «$tz», attesa UTC — il motore cron assume UTC (R2)"

# 6. Codifica UTF8: nomi dei job e output delle esecuzioni sono testo libero.
enc=$(psql_value 'show server_encoding')
[ "$enc" = "UTF8" ] \
  && ok "codifica: UTF8" \
  || fail "codifica «$enc», attesa UTF8"

# 7. Major version allineata alla produzione: testare in locale su una major
#    diversa da quella della VPS rende i test non rappresentativi.
version=$(psql_value "select split_part(current_setting('server_version'), ' ', 1)")
major=${version%%.*}
[ "$major" = "$EXPECTED_MAJOR" ] \
  && ok "PostgreSQL $version" \
  || fail "PostgreSQL $version, attesa la major $EXPECTED_MAJOR (vedi docker-compose.yml)"

# 8. La directory delle migrazioni esiste. Il tool che le applica arriva con la
#    issue #386: qui si verifica solo che ci sia dove metterle.
[ -d db/migrations ] \
  && ok "db/migrations presente ($(find db/migrations -name '*.sql' | wc -l | tr -d ' ') file .sql)" \
  || fail "manca db/migrations"

echo ""
if [ "$errors" -gt 0 ]; then
  echo "✗ db-check: $errors problema/i." >&2
  exit 1
fi

echo "✓ ambiente database coerente con .env e docker-compose.yml"
