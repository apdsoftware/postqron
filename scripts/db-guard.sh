#!/usr/bin/env bash
#
# Verifica che il PostgreSQL raggiungibile su POSTGRES_HOST:POSTGRES_PORT sia
# davvero il container di Postqron, e non un altro server in ascolto sulla
# stessa porta.
#
# Il problema che risolve (follow-up della issue #385). Se sulla porta indicata
# da POSTGRES_PORT c'è già un altro PostgreSQL — quello di sistema, o il
# container di un altro progetto — `make db-up` fallisce con «port is already
# allocated», ma `make migrate` si connette lo stesso: dal punto di vista del
# client Go quella porta risponde e parla il protocollo giusto. Se anche le
# credenziali combaciano (postgres/postgres è un default diffuso), le migrazioni
# finiscono sul database di qualcun altro, in silenzio.
#
# Come lo verifica. Non basta aprire una connessione e chiedere «chi sei»: un
# altro server risponderebbe in modo indistinguibile. Si chiede invece a Docker
# quale host:porta pubblica *il nostro* container, e si confronta con quello che
# .env dice al resto del progetto. Se il container non pubblica esattamente
# quella coppia, la porta è di qualcun altro — non esiste modo di legarla due
# volte.

set -uo pipefail

cd "${1:-.}" || exit 2

. scripts/db-env.sh || exit 1

if ! command -v docker >/dev/null 2>&1; then
  echo "✗ docker non installato: il database di sviluppo gira in Docker" >&2
  exit 1
fi

# `docker compose port` legge il binding reale del container in esecuzione: se
# il servizio è fermo non stampa nulla ed esce diverso da zero.
published=$(docker compose port postgres 5432 2>/dev/null | tr -d '[:space:]')

if [ -z "$published" ]; then
  echo "✗ il container PostgreSQL di Postqron non è in esecuzione." >&2
  echo "  Avvialo con \`make db-up\`. Se db-up fallisce con «port is already" >&2
  echo "  allocated», la porta $POSTGRES_PORT è occupata da un altro server:" >&2
  echo "  cambia POSTGRES_PORT in .env invece di riusare quello che c'è." >&2
  exit 1
fi

expected="$POSTGRES_HOST:$POSTGRES_PORT"

if [ "$published" != "$expected" ]; then
  echo "✗ il container di Postqron pubblica $published, ma .env punta a $expected." >&2
  echo "  Su $expected risponde quindi un altro server: connettersi significherebbe" >&2
  echo "  migrare il database sbagliato. Allinea POSTGRES_HOST/POSTGRES_PORT in" >&2
  echo "  .env e riavvia con \`make db-down && make db-up\`." >&2
  exit 1
fi

# Il container può essere «up» ma non ancora pronto ad accettare connessioni:
# initdb gira al primo avvio e dura qualche secondo.
if ! docker compose exec -T postgres \
  pg_isready -h 127.0.0.1 -p 5432 -U "$POSTGRES_USER" -d "$POSTGRES_DB" >/dev/null 2>&1; then
  echo "✗ il container di Postqron risponde su $expected ma non è pronto." >&2
  echo "  Attendi qualche secondo e riprova, oppure controlla i log:" >&2
  echo "  docker compose logs postgres" >&2
  exit 1
fi

echo "  ✓ PostgreSQL su $expected è il container di Postqron"
