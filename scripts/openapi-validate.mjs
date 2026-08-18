#!/usr/bin/env node
//
// Valida il contratto OpenAPI dell'API pubblica (R51) contro lo schema del
// formato.
//
// # Perché serve, visto che c'è già il controllo di allineamento
//
// Perché rispondono a due domande diverse, e nessuna delle due copre l'altra.
// Il controllo in `internal/httpapi/contract_test.go` verifica che il documento
// dica **la verità sul servizio**: rotte, permessi, codici, campi. Questo
// verifica che dica qualcosa che **gli strumenti sanno leggere**: un `$ref` che
// punta a uno schema inesistente, un `type` scritto male o un parametro senza
// `in` non fanno divergere niente dal codice, e rompono ugualmente ogni
// generatore di client — cioè rendono il contratto inutile proprio per chi
// dovrebbe usarlo.
//
// # Che cosa questo controllo **non** copre
//
// Lo schema di OpenAPI 3.1 delega gli oggetti-schema al dialetto JSON Schema
// 2020-12 con un `$dynamicRef`, e il validatore lì è permissivo: un `type:
// stringa` scritto male dentro uno schema passa di qui. È misurato, non
// supposto — provato scrivendolo. Quel buco lo chiude
// `TestContrattoTipiDichiarati` in internal/httpapi/contract_test.go, che
// verifica che ogni `type` dichiarato sia uno dei sette del formato.
//
// # Perché in locale e senza rete
//
// La CI di questo repository è esclusivamente locale (AGENTS.md §2), e il
// validatore porta con sé gli schemi ufficiali di OpenAPI: nessuna chiamata di
// rete, né qui né nei test Go. Un controllo che dipendesse dalla rete
// fallirebbe, o peggio passerebbe in silenzio, il giorno in cui la rete non c'è.
//
// Uso: node scripts/openapi-validate.mjs [percorso]

import { readFile } from "node:fs/promises";
import { argv, exit } from "node:process";
import { Validator } from "@seriousme/openapi-schema-validator";

const percorso = argv[2] ?? "services/api/openapi/openapi.yaml";

let documento = "";
try {
  documento = await readFile(percorso, "utf8");
} catch (errore) {
  console.error(`✗ contratto OpenAPI non leggibile: ${percorso}`);
  console.error(`  ${errore instanceof Error ? errore.message : errore}`);
  exit(1);
}

const validatore = new Validator();
const esito = await validatore.validate(documento);

if (!esito.valid) {
  console.error(`✗ ${percorso} non è un documento OpenAPI valido:`);
  // `errors` è un elenco quando il documento è leggibile ma non conforme, una
  // stringa quando non si è potuto nemmeno analizzarlo — per esempio un `$ref`
  // che non punta a niente.
  if (Array.isArray(esito.errors)) {
    for (const errore of esito.errors) {
      console.error(`  ${errore.instancePath || "/"} ${errore.message}`);
    }
  } else {
    console.error(`  ${esito.errors}`);
  }
  exit(1);
}

// Dal documento e non dal validatore: `specification` è ciò che è stato letto, e
// `openapi` è la versione del **formato** che il documento dichiara di seguire.
const spec = /** @type {Record<string, any>} */ (validatore.specification ?? {});
const rotte = Object.keys(spec.paths ?? {}).length;
console.log(
  `  ✓ contratto OpenAPI ${spec.openapi ?? "?"} valido — versione ${spec.info?.version ?? "?"}, ${rotte} percorsi`,
);
