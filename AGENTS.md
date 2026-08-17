# Regole operative per gli agenti — PostQron

Vale per ogni agente Paseo che lavora su questo repository, e per l'agente PM che li
orchestra. Leggi questo file **prima** di agire.

---

## 1. Contesto obbligatorio

Prima di scrivere codice, leggi sempre:

1. [`docs/SPEC.md`](docs/SPEC.md) — la specifica funzionale, fonte di verità.
2. [`docs/BACKLOG.md`](docs/BACKLOG.md) — la issue assegnata e le sue dipendenze.

Lavora **solo** sui file pertinenti alla tua issue. Non toccare file di altre
funzionalità: worktree paralleli stanno lavorando sugli stessi percorsi e i conflitti
si pagano al merge.

---

## 2. CI: esclusivamente locale

> È **vietato** gestire la CI tramite infrastrutture GitHub.

- Non creare `.github/workflows/`, non aggiungere GitHub Actions, non configurare
  Dependabot o altri controlli lato GitHub.
- La pipeline è `make ci` e gira in locale. L'hook `pre-push` la esegue
  automaticamente (`make hooks` per installarlo).
- **Nessuna issue è completa se `make ci` non passa** nel suo worktree.

GitHub resta in uso per il codice, le issue e le pull request — e come *funzionalità
di prodotto* (webhook per `cron.yaml`, R11–R13). Il divieto riguarda solo la CI.

---

## 3. Isolamento: un worktree per issue

- Ogni issue in lavorazione ha un **git worktree dedicato** e un agente dedicato.
- Branch: `paseo/issue-<numero>-<slug>`.
- È vietato aprire progetti temporanei scollegati dal repository.
- **Al merge**, worktree e ambiente dell'agente vengono cancellati
  (`git worktree remove` + archiviazione dell'agente). Nessun residuo.

---

## 4. Assegnazione dei modelli

| Ambito | Provider ammessi | Modello di riferimento |
|---|---|---|
| Backend Go, PostgreSQL, migrazioni, infrastruttura, sicurezza | **`claude` soltanto** | `claude-opus-5` |
| Frontend Vue/Nuxt, template Hexagon e Flowbite, UI | `claude` oppure `codex` | `claude-opus-5` / `gpt-5.6-sol` |
| Documentazione, contenuti, testi legali | `claude` oppure `codex` | qualsiasi |

**Regola vincolante:** *non utilizzare Codex per nessuna issue relativa al backend*,
dato l'alto tasso di errori riscontrato.

> Nota: tra i provider configurati in Paseo l'unico riconducibile a ChatGPT è
> `codex`, che è vietato sul backend. Di conseguenza **tutto il backend va a
> `claude`**. Se in futuro viene abilitato un provider ChatGPT distinto da Codex,
> diventa ammissibile anche quello.

---

## 5. Definition of Done

Una issue è chiusa solo quando:

- [ ] Il requisito della spec (`R<n>`) è implementato per intero, non a metà.
- [ ] Esistono test che coprono il comportamento nuovo.
- [ ] `make ci` passa nel worktree.
- [ ] Le migrazioni, se presenti, sono versionate e reversibili.
- [ ] Nessun segreto è finito nel codice o nei log.
- [ ] La PR descrive la modifica e riferisce il numero della issue.

---

## 6. Escalation all'umano

L'intervento umano è previsto **solo** per la verifica finale e per le scelte critiche
o di business. Non chiedere conferma per task operativi standard: procedi.

Fermati e chiedi **solo** se:

- la scelta ha impatto commerciale (prezzi, piani, limiti);
- servono credenziali o account esterni non presenti;
- la spec è ambigua e le due letture producono lavoro sostanzialmente diverso;
- l'azione è distruttiva o irreversibile (force push, cancellazione di dati o branch).

I punti già noti come bloccanti sono elencati in [`docs/SPEC.md`](docs/SPEC.md) §7.

---

## 7. Convenzioni

- Commit in stile Conventional Commits: `feat(engine): ...`, `fix(api): ...`.
- Go: `gofmt`, errori sempre gestiti, `context.Context` propagato.
- Vue/Nuxt: Composition API con `<script setup>`, TypeScript, componenti tipizzati.
- SQL: migrazioni numerate in `db/migrations/`, mai modificate dopo il merge.
- Documentazione e commenti in italiano; identificatori di codice in inglese.
