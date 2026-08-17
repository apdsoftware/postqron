#!/usr/bin/env bash
# Verifica che nessun segreto entri nel repository.
#
# La superficie da controllare sono i commit che stanno per essere pubblicati,
# non l'albero di lavoro e non lo storico completo. Le due alternative ovvie
# sono entrambe sbagliate, e lo sono state davvero:
#
#   gitleaks dir .   scandisce il filesystem, quindi anche file che git non
#                    vedrà mai: `.env` reale e i worktree delle altre issue.
#                    Con le credenziali configurate falliva per tutti.
#
#   gitleaks git     senza intervallo risale tutto lo storico. Qui sono 830
#                    commit e 7 GB, perché il repository conserva l'archivio
#                    del prodotto precedente: due minuti e 814 rilevamenti che
#                    non riguardano Postqron.
#
# Quello che segue guarda esattamente i commit nuovi rispetto al ramo remoto.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

base=""
for ref in "@{upstream}" "origin/main" "origin/HEAD"; do
	if base=$(git rev-parse --verify --quiet "$ref" 2>/dev/null); then break; fi
	base=""
done

if [ -z "$base" ]; then
	# Nessun riferimento remoto: clone appena inizializzato o repository locale.
	# Si scandisce l'intero storico, che in quel caso è corto per definizione.
	echo "· nessun ramo remoto di riferimento: scansione dello storico completo"
	opts="--all"
elif [ "$base" = "$(git rev-parse HEAD)" ]; then
	echo "· nessun commit da pubblicare: niente da scansionare"
	exit 0
else
	count=$(git rev-list --count "$base..HEAD")
	echo "· scansione di $count commit non ancora pubblicati"
	opts="$base..HEAD"
fi

gitleaks git --no-banner --redact --log-opts="$opts"
