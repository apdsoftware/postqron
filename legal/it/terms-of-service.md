---
document: terms-of-service
version: 1.2.0
effective_date: 2026-08-18
language: it
status: pending-review
---

# Termini di servizio

Questi termini regolano il tuo utilizzo di Postqron. Creando un account li accetti,
insieme all'[Informativa sull'uso accettabile](acceptable-use-policy.md) e
all'[Informativa sulla privacy](privacy-policy.md).

## 1. Con chi stai contrattando

Postqron è gestito da
Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
(«noi»).

**Gli acquisti avvengono tramite Paddle.** Paddle agisce come Merchant of Record: quando
acquisti un piano a pagamento, il contratto di vendita relativo a quell'acquisto è fra
te e Paddle, e a esso si applicano i termini per gli acquirenti di Paddle in aggiunta ai
presenti termini. Paddle si occupa del pagamento, della fatturazione e delle imposte.
Noi ci occupiamo del servizio.

## 2. Che cosa fa il servizio

Postqron esegue richieste HTTP verso indirizzi che configuri tu, negli orari che
configuri tu, ne registra l'esito e ti avvisa in caso di errore. Le pianificazioni si
possono definire nell'applicazione oppure in un file `cron.yaml` all'interno di un
repository che colleghi.

**Postqron non esegue il tuo codice.** Effettua richieste HTTP. Se una richiesta avvia
un'elaborazione sui tuoi sistemi, quell'elaborazione è tua.

## 3. Il tuo account

Sei responsabile di ciò che accade sotto il tuo account, della custodia delle tue
credenziali e delle persone che inviti nel tuo workspace. Comunicacelo tempestivamente
se ritieni che il tuo account sia stato compromesso.

Devi avere almeno 16 anni e, se agisci per conto di un'organizzazione, essere
autorizzato a vincolarla.

**Il piano gratuito è aperto a chiunque.** Usalo per un progetto personale, per provare
il servizio, o perché ti basta così. Nulla qui ti chiede di essere un'impresa per creare
un account.

**I piani a pagamento sono offerti per l'uso professionale.** Quando ne acquisti uno,
confermi di agire nell'esercizio di un'attività imprenditoriale, commerciale, artigianale
o professionale. È per questo che i nostri prezzi sono esposti al netto delle imposte:
per chi ha un'attività, la cifra netta è quella che conta, perché è quella che deduce. Te
lo chiediamo di confermare in fase di acquisto, e raccogliamo la tua partita IVA quando
ne hai una — alcuni regimi per le piccole imprese perfettamente legittimi, in Europa, non
ne prevedono una, quindi la chiediamo, non la pretendiamo.

Dove la legge ti riconosce tutele da consumatore nonostante quella conferma, vince la
legge — compreso il diritto di recesso di cui al §4.3.

## 4. Piani, limiti e pagamento

Piani, prezzi e limiti sono quelli pubblicati sulla nostra pagina dei prezzi e applicati
dal servizio. **I limiti sono imposti dal motore**, non soltanto dichiarati: il numero di
job di un piano, l'intervallo minimo e la conservazione dei log sono tetti reali.

I prezzi sono esposti **al netto delle imposte**. Paddle calcola e aggiunge l'imposta
applicabile in base a dove ti trovi.

I piani a pagamento si rinnovano automaticamente per lo stesso periodo fino alla
disdetta. Puoi disdire in qualsiasi momento; la disdetta ha effetto alla fine del periodo
che hai già pagato, e fino ad allora il servizio prosegue.

### 4.1 Cambio di piano

Gli upgrade hanno effetto immediato. **I downgrade hanno effetto alla fine del periodo in
corso**, e ti diciamo che cosa accadrà prima che tu confermi.

**Se hai più job attivi di quanti il piano inferiore ne consenta, li mettiamo in pausa
tutti e scegli tu quali riattivare**, fino al nuovo limite. Non scegliamo noi al posto
tuo, perché non possiamo: due job che a noi sembrano identici possono essere, per te, uno
che emette fatture e uno che manda un promemoria. Qualunque regola automatica ci fossimo
inventati tirerebbe a indovinare — e sbaglierebbe proprio dove conta di più.

Se i tuoi job attivi rientrano già nel nuovo limite, non viene messo in pausa nulla.

**Non cancelliamo il tuo lavoro.** I job in pausa restano visibili, modificabili ed
esportabili, con il loro storico di esecuzioni. Una cosa da sapere: un job pianificato
più di frequente di quanto il nuovo piano consenta non può essere riattivato finché non
ne cambi la pianificazione, anche se c'è spazio per lui.

Lo stesso vale se un pagamento fallisce definitivamente o se un abbonamento decade:
entrambi portano l'account al piano gratuito.

### 4.2 Pagamento non riuscito

Se un pagamento fallisce, Paddle riprova secondo la propria cadenza. In quel periodo il
tuo servizio prosegue. Se il pagamento fallisce definitivamente, l'account passa al piano
gratuito e il §4.1 si applica senza modifiche: se hai più job attivi di quanti il piano
gratuito ne consenta, vengono messi in pausa tutti e scegli tu quali riattivare. Non
viene cancellato nulla.

### 4.3 Rimborsi e recesso

La regola è semplice: **puoi smettere quando vuoi, e il mese che hai già pagato arriva
alla sua fine.** Non viene rimborsato nulla pro rata, e non c'è nulla da reclamare o da
negoziare.

Se sei un consumatore nell'Unione europea hai inoltre il diritto legale di recedere entro
14 giorni dall'acquisto. Poiché il servizio inizia subito, ti viene chiesto di
acconsentire all'esecuzione immediata; quel consenso fa venire meno il diritto di recesso
una volta che il servizio è stato interamente eseguito. Dove la legge ci imponga comunque
di rimborsarti, lo facciamo, e Paddle esegue il rimborso.

## 5. Disponibilità

Puntiamo a tenere il servizio sempre attivo, e ti diremo quando non lo è (vedi
l'Informativa sull'uso accettabile per il modo in cui ti contattiamo in caso di
incidenti).

**Non offriamo una garanzia di disponibilità, e vogliamo essere chiari sul perché.** Lo
scheduler e il database girano su un unico server, scelto deliberatamente perché
l'invio non sia rallentato dalla latenza di rete. Quella scelta baratta resilienza per
precisione. Facciamo backup e ne verifichiamo il ripristino, ma un guasto di quella
macchina interrompe il servizio. Qualunque impegno prendessimo oltre a ciò che una sola
macchina può garantire sarebbe un impegno che non potremmo mantenere.

Se un giorno offriremo un accordo sul livello di servizio con impegni misurabili,
comparirà qui — e l'architettura sarà cambiata prima, non dopo.

## 6. I tuoi contenuti e i nostri

**Ciò che è tuo resta tuo.** Le tue pianificazioni, la configurazione, i log e i dati che
fai transitare dal servizio restano di tua proprietà. Ci concedi soltanto il permesso che
ci serve per far funzionare il servizio per te: conservare quei dati, eseguire le
richieste che configuri e mostrarti i risultati.

Postqron in sé — il software, l'interfaccia, il nome e il marchio — resta nostro. Questi
termini ti danno il diritto di usare il servizio, non di copiarlo o rivenderlo.

## 7. Sospensione e cessazione

Possiamo sospendere o chiudere il tuo account per una violazione sostanziale di questi
termini o dell'Informativa sull'uso accettabile, nei modi e con il preavviso ivi
descritti.

Puoi chiudere il tuo account in qualsiasi momento. Alla chiusura interrompiamo
l'esecuzione, revochiamo le chiavi e cancelliamo i tuoi dati dopo il periodo di
ripensamento indicato nell'Informativa sulla privacy.

**Chiudere l'account non annulla un abbonamento a pagamento.** Il pagamento è gestito da
Paddle in qualità di Merchant of Record (§1), quindi un abbonamento si disdice presso
Paddle, non presso di noi. Se chiudi il tuo account mentre è attivo un piano a pagamento,
il periodo che hai già pagato arriva alla sua fine, come descritto al §4.3. Te lo diciamo
prima che tu confermi, e ti chiediamo di darne atto.

## 8. Responsabilità

Nulla qui limita la responsabilità che per legge non può essere limitata, compresa quella
per morte o lesioni personali causate da negligenza, per dolo, o i diritti che i
consumatori hanno in forza di norme inderogabili.

Fermo restando quanto sopra: forniamo il servizio con ragionevole diligenza e perizia, ma
non rispondiamo di danni indiretti o consequenziali, di perdita di profitto o di
avviamento, né delle conseguenze delle elaborazioni che i tuoi job avviano sui tuoi
sistemi. **Una richiesta pianificata non è una garanzia che l'elaborazione dietro di essa
sia riuscita**, e dovresti progettare i tuoi sistemi partendo da questo presupposto.

Al di là di quelle eccezioni, **la nostra responsabilità è esclusa nella misura massima
consentita dalla legge applicabile**.

Preferiamo dirlo apertamente piuttosto che nasconderlo: Postqron è uno scheduler che
costa da zero a poche decine di euro al mese, e non può farsi carico del rischio di ciò
che dipende dai job che esegue. Se un'esecuzione mancata o duplicata ti causasse un danno
rilevante, il servizio non è il posto giusto dove mettere quella dipendenza, e nessuna
formulazione qui dentro cambia questa realtà ingegneristica.

## 9. Modifiche a questi termini

Possiamo modificare questi termini. Quando una modifica incide in modo sostanziale sui
tuoi diritti ti diamo un preavviso di
30 giorni.
Se non accetti la modifica, puoi chiudere il tuo account prima che abbia effetto.

## 10. Legge applicabile e foro competente

Questi termini sono regolati
dalla legge italiana.
Le controversie sono devolute alla competenza esclusiva
del Foro di Bergamo, Italia,
**salvo** che, se sei un consumatore, conservi la protezione delle norme inderogabili del
Paese in cui risiedi e puoi agire davanti al tuo giudice locale.

---

**Contatto:** hello@postqron.com
**Gestito da:** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
