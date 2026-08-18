---
document: acceptable-use-policy
version: 1.0.0
effective_date: 2026-08-17
language: it
status: pending-review
---

# Informativa sull'uso accettabile

Questa informativa è parte dei [Termini di servizio](terms-of-service.md). Descrive che
cosa non puoi fare con Postqron, e che cosa succede se lo fai.

Postqron invia richieste HTTP verso indirizzi che scegli tu, secondo una pianificazione
che scegli tu, dalla nostra infrastruttura e dai nostri indirizzi IP. È una capacità
utile, ed è anche la capacità che vuole un attaccante. Questa informativa esiste perché la
differenza fra le due sia scritta invece che lasciata al giudizio.

## 1. A chi si applica

A chiunque usi Postqron, su qualsiasi piano, piano gratuito compreso. Si applica anche a
chiunque tu inviti nel tuo workspace: sei responsabile del loro utilizzo del servizio.

## 2. Che cosa non devi fare

### 2.1 Attaccare, sovraccaricare o sondare sistemi

Non devi usare Postqron per:

- inviare richieste a un sistema che non ti appartiene o che non sei espressamente
  autorizzato a testare;
- generare carico allo scopo di degradare, esaurire o negare il servizio di un qualsiasi
  sistema, anche attraverso pianificazioni ad alta frequenza, molti job contro un unico
  bersaglio, o l'uso coordinato di più account;
- scansionare, enumerare o sondare host, porte, percorsi o credenziali;
- raggiungere sistemi che non sono destinati a essere pubblicamente raggiungibili,
  comprese reti private, indirizzi di loopback, endpoint di metadati cloud e servizi
  interni — nostri o di chiunque altro.

L'autorizzazione conta più dell'intenzione. Pianificare una richiesta verso l'endpoint di
un terzo non diventa accettabile chiamandola health check.

### 2.2 Aggirare i nostri controlli

Non devi tentare di eludere le misure tecniche che fanno rispettare questa informativa,
compresi il filtro degli indirizzi, i limiti di frequenza, i limiti di piano o i tetti di
esecuzione. Ciò comprende l'uso di redirect, di voci DNS sotto il tuo controllo o di proxy
per raggiungere una destinazione che altrimenti rifiuteremmo.

### 2.3 Usare il servizio in modo illecito o abusivo

Non devi usare Postqron per violare la legge, per ledere i diritti di qualcuno, per
distribuire malware, per inviare messaggi non sollecitati, o per trattare contenuti
illeciti nelle giurisdizioni in cui ti trovi tu o si trovano i tuoi destinatari.

### 2.4 Falsare l'origine

Non devi presentare richieste provenienti da Postqron come se venissero da qualcun altro,
né usare il servizio per occultare l'origine di un'attività.

### 2.5 Rivendere o esporre il servizio come se fosse tuo

Non devi offrire a terzi la capacità di esecuzione di Postqron come un servizio tuo senza
un accordo scritto. Eseguire job per conto dei tuoi clienti all'interno di un workspace
Agency è previsto e consentito; costruire un prodotto sopra il nostro scheduler e venderlo
non lo è.

## 3. Risorse condivise

Le richieste in uscita partono da indirizzi IP condivisi da tutti i clienti, salvo dove un
piano includa un indirizzo dedicato. La reputazione di quegli indirizzi è un bene comune:
l'abuso di un cliente degrada il servizio per tutti. Facciamo rispettare questa
informativa per proteggere gli altri clienti, non per sorvegliare te.

Possiamo applicare limiti aggregati per host di destinazione, e possiamo rifiutare o
rallentare le richieste verso una destinazione che mostri i segni di essere presa di mira
anziché servita.

## 4. Che cosa facciamo in caso di violazione

Dove la situazione lo consente, ti contattiamo prima e ti diamo modo di rimediare. Dove
non lo consente — perché il danno è in corso, perché un terzo è sotto attacco, o perché
siamo tenuti per legge ad agire — possiamo agire immediatamente e dirtelo dopo.

A seconda della gravità possiamo:

1. **limitare o bloccare** job o destinazioni specifici;
2. **sospendere** i job interessati lasciando per il resto l'account utilizzabile;
3. **sospendere l'account**, fermando ogni esecuzione;
4. **chiudere** l'account.

Sospendiamo la cosa più circoscritta che ferma il danno. La sospensione non è un evento da
rimborso: vedi i Termini.

Dove sospendiamo o chiudiamo, conservi il diritto di esportare i tuoi dati
per 30 giorni,
salvo che ciò sia illecito.

## 5. Segnalare un abuso

Se ritieni che qualcuno stia usando Postqron per attaccare o abusare di un sistema di cui
sei responsabile, scrivi a
abuse@postqron.com.
Indica l'indirizzo di destinazione, gli orari in UTC e, se disponibile, l'IP di origine.
Esaminiamo le segnalazioni e ne confermeremo la ricezione
entro due giorni lavorativi.

## 6. Modifiche

Possiamo aggiornare questa informativa. Quando una modifica restringe in modo sostanziale
ciò che è consentito, ti diamo un preavviso di
30 giorni
prima che abbia effetto, salvo che un termine più breve sia necessario per fermare un
danno in corso o per adempiere alla legge.

---

**Contatto:** hello@postqron.com
**Gestito da:** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
