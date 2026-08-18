package execstream_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/execstream"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// Le prove di questo package non aspettano mai.
//
// Il battito è un canale che il test fa scattare a comando: una suite che
// dormisse un secondo per vedere un evento avrebbe legato la CI all'orologio
// invece che alla logica, e ogni prova costerebbe un secondo per sempre.

// ------------------------------------------------------------- impalcatura

// registro è una sorgente in memoria: la tabella `job_executions` di un job,
// indicizzata per chiave naturale come nella 0006.
type registro struct {
	mu   sync.Mutex
	rows map[jobs.StreamPosition]jobs.Execution
	err  error
	// letture conta le chiamate: serve a provare che il ciclo di lettura è uno
	// solo e che non interroga i flussi già chiusi.
	letture int
}

func nuovoRegistro() *registro {
	return &registro{rows: map[jobs.StreamPosition]jobs.Execution{}}
}

// scrivi inserisce o aggiorna una riga, come fanno l'INSERT dello scheduler e
// gli UPDATE del worker pool.
func (r *registro) scrivi(exec jobs.Execution) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[jobs.PositionOf(exec)] = exec
}

func (r *registro) guasto(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func (r *registro) Next(_ context.Context, from *jobs.StreamPosition, limit int) ([]jobs.Execution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.letture++
	if r.err != nil {
		return nil, r.err
	}

	var out []jobs.Execution
	for position, exec := range r.rows {
		if from != nil && !dopo(position, *from) {
			continue
		}
		out = append(out, exec)
	}
	sort.Slice(out, func(i, j int) bool {
		return dopo(jobs.PositionOf(out[j]), jobs.PositionOf(out[i]))
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func dopo(a, b jobs.StreamPosition) bool {
	switch {
	case !a.ScheduledFor.Equal(b.ScheduledFor):
		return a.ScheduledFor.After(b.ScheduledFor)
	case a.Environment != b.Environment:
		return jobs.EnvironmentRank(a.Environment) > jobs.EnvironmentRank(b.Environment)
	default:
		return a.Attempt > b.Attempt
	}
}

var origine = time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

// esecuzione compone una riga alla `secondi`-esima occorrenza.
func esecuzione(secondi int, stato jobs.ExecutionStatus) jobs.Execution {
	return jobs.Execution{
		JobID:        "job",
		ScheduledFor: origine.Add(time.Duration(secondi) * time.Second),
		Environment:  jobs.EnvironmentProduction,
		Attempt:      1,
		Status:       stato,
		TriggeredBy:  jobs.TriggerSchedule,
	}
}

type banco struct {
	t    *testing.T
	hub  *execstream.Hub
	tick chan time.Time
}

func nuovoBanco(t *testing.T, limits execstream.Limits) *banco {
	t.Helper()
	tick := make(chan time.Time)
	hub, err := execstream.New(execstream.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Limits: limits,
		Tick:   tick,
	})
	if err != nil {
		t.Fatalf("execstream.New: %v", err)
	}
	t.Cleanup(hub.Stop)
	return &banco{t: t, hub: hub, tick: tick}
}

// battito esegue un giro di lettura **nella goroutine della prova**.
//
// Il canale del battito passato all'hub non scatta mai: il ciclo resta fermo, e
// il giro lo fa [execstream.Hub.Poll] a comando. Così ogni prova è
// deterministica e non costa un secondo — vedi la nota in testa al file.
func (b *banco) battito() {
	b.t.Helper()
	b.hub.Poll()
}

// eventi svuota ciò che il flusso ha accodato, senza aspettare.
func eventi(s *execstream.Stream) []execstream.Event {
	var out []execstream.Event
	for {
		select {
		case e, open := <-s.Events():
			if !open {
				return out
			}
			out = append(out, e)
		default:
			return out
		}
	}
}

// ------------------------------------------------------------------- prove

// TestUnEsecuzioneConclusaViaggiaConLaPropriaPosizione è la distinzione su cui
// si regge la riconnessione: solo un fatto definitivo può essere una posizione
// da cui riprendere.
func TestUnEsecuzioneConclusaViaggiaConLaPropriaPosizione(t *testing.T) {
	b := nuovoBanco(t, execstream.Limits{})
	reg := nuovoRegistro()
	reg.scrivi(esecuzione(0, jobs.StatusSucceeded))
	reg.scrivi(esecuzione(1, jobs.StatusRunning))

	stream, err := b.hub.Subscribe("utente", reg, nil)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer stream.Close()

	b.battito()
	got := eventi(stream)
	if len(got) != 2 {
		t.Fatalf("eventi = %d, attesi 2: %+v", len(got), got)
	}

	if got[0].ID == "" {
		t.Error("l'esecuzione conclusa è arrivata senza posizione: un client che riprende da lì la riceverebbe due volte")
	}
	if got[1].ID != "" {
		t.Error("l'esecuzione in corso è arrivata con una posizione: chi riprendesse da lì non ne vedrebbe mai l'esito")
	}

	// La posizione è quella della riga, ed è rileggibile.
	position, err := jobs.ParseStreamPosition(got[0].ID)
	if err != nil {
		t.Fatalf("la posizione consegnata non è leggibile: %v", err)
	}
	if !position.ScheduledFor.Equal(origine) {
		t.Errorf("posizione = %s, attesa %s", position.ScheduledFor, origine)
	}
}

// TestUnEsecuzioneInCorsoNonSiRipeteAOgniBattito: senza questo controllo un job
// con timeout di trenta secondi manderebbe trenta volte la stessa riga.
func TestUnEsecuzioneInCorsoNonSiRipeteAOgniBattito(t *testing.T) {
	b := nuovoBanco(t, execstream.Limits{})
	reg := nuovoRegistro()
	reg.scrivi(esecuzione(0, jobs.StatusRunning))

	stream, err := b.hub.Subscribe("utente", reg, nil)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer stream.Close()

	b.battito()
	if got := eventi(stream); len(got) != 1 {
		t.Fatalf("primo battito: eventi = %d, atteso 1", len(got))
	}
	b.battito()
	if got := eventi(stream); len(got) != 0 {
		t.Fatalf("secondo battito: eventi = %d, atteso 0 (lo stato non è cambiato): %+v", len(got), got)
	}

	// Cambia stato: adesso è un fatto nuovo, e con esso la posizione.
	reg.scrivi(esecuzione(0, jobs.StatusSucceeded))
	b.battito()
	got := eventi(stream)
	if len(got) != 1 {
		t.Fatalf("terzo battito: eventi = %d, atteso 1: %+v", len(got), got)
	}
	if got[0].ID == "" {
		t.Error("la conclusione è arrivata senza posizione")
	}
}

// TestIlCursoreNonSuperaUnEsecuzioneAncoraInCorso è il caso di `on_overlap:
// allow` (R41): un'occorrenza lunga si conclude dopo una breve che la segue.
//
// Se il cursore saltasse alla più recente conclusa, quella rimasta indietro non
// arriverebbe mai a chi si riconnette — ed è precisamente il buco che la
// riconnessione deve non avere.
func TestIlCursoreNonSuperaUnEsecuzioneAncoraInCorso(t *testing.T) {
	b := nuovoBanco(t, execstream.Limits{})
	reg := nuovoRegistro()
	reg.scrivi(esecuzione(0, jobs.StatusRunning))   // la lunga
	reg.scrivi(esecuzione(1, jobs.StatusSucceeded)) // la breve, già finita

	stream, err := b.hub.Subscribe("utente", reg, nil)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer stream.Close()

	b.battito()
	got := eventi(stream)
	if len(got) != 1 {
		t.Fatalf("eventi = %d, atteso 1 — solo quella in corso: %+v", len(got), got)
	}
	if got[0].ID != "" || !got[0].Execution.ScheduledFor.Equal(origine) {
		t.Fatalf("atteso lo stato intermedio della prima occorrenza, senza posizione: %+v", got[0])
	}

	// La lunga finisce: adesso il prefisso concluso arriva fino in fondo, e le due
	// occorrenze escono **in ordine** e **una volta sola ciascuna**.
	reg.scrivi(esecuzione(0, jobs.StatusSucceeded))
	b.battito()

	got = eventi(stream)
	if len(got) != 2 {
		t.Fatalf("eventi = %d, attesi 2: %+v", len(got), got)
	}
	for i, e := range got {
		if e.ID == "" {
			t.Errorf("evento %d senza posizione: %+v", i, e)
		}
		if !e.Execution.ScheduledFor.Equal(origine.Add(time.Duration(i) * time.Second)) {
			t.Errorf("evento %d fuori ordine: %s", i, e.Execution.ScheduledFor)
		}
	}
}

// TestLaRipresaNonHaBuchiNeDoppioni è la prova della riconnessione, ed è quella
// che conta: si consegna, si simula la caduta, si riapre dall'ultima posizione
// ricevuta e si verifica che l'unione delle due consegne sia esattamente
// l'elenco delle esecuzioni, ciascuna una volta sola.
func TestLaRipresaNonHaBuchiNeDoppioni(t *testing.T) {
	b := nuovoBanco(t, execstream.Limits{})
	reg := nuovoRegistro()
	for i := range 3 {
		reg.scrivi(esecuzione(i, jobs.StatusSucceeded))
	}

	primo, err := b.hub.Subscribe("utente", reg, nil)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	b.battito()
	consegnate := eventi(primo)
	primo.Close()

	if len(consegnate) != 3 {
		t.Fatalf("prima consegna = %d eventi, attesi 3", len(consegnate))
	}
	ultima := consegnate[len(consegnate)-1].ID

	// Mentre il client è scollegato ne arrivano altre due.
	for i := 3; i < 5; i++ {
		reg.scrivi(esecuzione(i, jobs.StatusSucceeded))
	}

	position, err := jobs.ParseStreamPosition(ultima)
	if err != nil {
		t.Fatalf("Last-Event-ID non leggibile: %v", err)
	}
	secondo, err := b.hub.Subscribe("utente", reg, &position)
	if err != nil {
		t.Fatalf("Subscribe alla ripresa: %v", err)
	}
	defer secondo.Close()

	b.battito()
	ripresa := eventi(secondo)

	// Nessun buco: l'unione copre tutte e cinque le occorrenze.
	// Nessun doppione: sono esattamente cinque eventi in tutto.
	viste := map[time.Time]int{}
	for _, e := range append(consegnate, ripresa...) {
		viste[e.Execution.ScheduledFor]++
	}
	if len(viste) != 5 {
		t.Fatalf("occorrenze distinte consegnate = %d, attese 5 — c'è un buco: %v", len(viste), viste)
	}
	for istante, volte := range viste {
		if volte != 1 {
			t.Errorf("l'occorrenza %s è stata consegnata %d volte: doppione", istante.Format(time.RFC3339), volte)
		}
	}
	if len(ripresa) != 2 {
		t.Errorf("la ripresa ha consegnato %d eventi, attesi 2: %+v", len(ripresa), ripresa)
	}
}

// TestIlClientLentoNonFaCrescereNienteAllInfinito: un client che non legge non
// deve poter far crescere un buffer, né bloccare il ciclo di lettura — che è uno
// solo e servirebbe a tutti gli altri.
func TestIlClientLentoNonFaCrescereNienteAllInfinito(t *testing.T) {
	const buffer = 4
	b := nuovoBanco(t, execstream.Limits{Buffer: buffer})
	reg := nuovoRegistro()
	for i := range 50 {
		reg.scrivi(esecuzione(i, jobs.StatusSucceeded))
	}

	stream, err := b.hub.Subscribe("utente", reg, nil)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer stream.Close()

	// Nessuno legge. Il giro deve tornare comunque: se si bloccasse, questa
	// chiamata non tornerebbe e la prova andrebbe in timeout — che è il modo in
	// cui si accorgerebbe della regressione peggiore.
	b.battito()

	accodati := eventi(stream)
	if len(accodati) > buffer {
		t.Fatalf("accodati %d eventi con un buffer di %d: il buffer è cresciuto", len(accodati), buffer)
	}
	if !stream.Overrun() {
		t.Error("il flusso non è stato marcato come troppo lento")
	}
	if aperti := b.hub.Open(); aperti != 0 {
		t.Errorf("flussi aperti = %d, atteso 0: il posto non è stato liberato", aperti)
	}

	// Il ciclo di lettura è vivo: un altro flusso continua a essere servito.
	altro, err := b.hub.Subscribe("utente", nuovoRegistro(), nil)
	if err != nil {
		t.Fatalf("Subscribe dopo l'overflow: %v", err)
	}
	defer altro.Close()
	b.battito()
}

// TestIlTettoDelleConnessioniEUnTettoTecnico verifica i due tetti e, soprattutto,
// **la forma del rifiuto**: R10 vuole che un tetto tecnico non nomini nessun
// piano, perché nessun piano ne concede di più.
func TestIlTettoDelleConnessioniEUnTettoTecnico(t *testing.T) {
	b := nuovoBanco(t, execstream.Limits{MaxPerUser: 2, MaxStreams: 3})

	for i := range 2 {
		s, err := b.hub.Subscribe("mario", nuovoRegistro(), nil)
		if err != nil {
			t.Fatalf("flusso %d: %v", i, err)
		}
		defer s.Close()
	}

	_, err := b.hub.Subscribe("mario", nuovoRegistro(), nil)
	limite, ok := jobs.AsServiceLimit(err)
	if !ok {
		t.Fatalf("terzo flusso: errore = %v, atteso un tetto tecnico", err)
	}
	if limite.Limit != jobs.LimitStreamCeiling {
		t.Errorf("tetto = %q, atteso %q", limite.Limit, jobs.LimitStreamCeiling)
	}
	if limite.RetryAfter <= 0 {
		t.Error("un tetto di capienza istantanea deve dire fra quanto riprovare")
	}
	// La stessa verifica che fa `Admit`, che è quella che l'API usa per rifiutare
	// prima di leggere il database.
	if err := b.hub.Admit("mario"); err == nil {
		t.Error("Admit ha ammesso un utente che ha già riempito il proprio tetto")
	}

	// Un altro utente ha il proprio tetto, non quello di mario.
	terzo, err := b.hub.Subscribe("giulia", nuovoRegistro(), nil)
	if err != nil {
		t.Fatalf("il tetto per utente si applica a un altro utente: %v", err)
	}
	defer terzo.Close()

	// Ma il tetto del servizio vale per tutti insieme.
	if _, err := b.hub.Subscribe("giulia", nuovoRegistro(), nil); err == nil {
		t.Error("il tetto complessivo del servizio non ha rifiutato la quarta connessione")
	}
}

// TestUnPostoSiLiberaUnaVoltaSola: chiudere due volte lo stesso flusso — cosa
// che succede quando il client se ne va mentre l'hub lo sta già scartando — non
// deve scalare due volte il contatore, o il tetto dell'utente scenderebbe a zero.
func TestUnPostoSiLiberaUnaVoltaSola(t *testing.T) {
	b := nuovoBanco(t, execstream.Limits{MaxPerUser: 1})

	stream, err := b.hub.Subscribe("mario", nuovoRegistro(), nil)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	stream.Close()
	stream.Close()

	if aperti := b.hub.Open(); aperti != 0 {
		t.Fatalf("flussi aperti = %d, atteso 0", aperti)
	}
	altro, err := b.hub.Subscribe("mario", nuovoRegistro(), nil)
	if err != nil {
		t.Fatalf("il posto non si è liberato: %v", err)
	}
	altro.Close()
}

// TestUnGuastoIsolatoNonChiudeIlFlusso: il database che si riavvia non deve far
// ricominciare tutte le dashboard; il database che non torna più sì.
func TestUnGuastoIsolatoNonChiudeIlFlusso(t *testing.T) {
	b := nuovoBanco(t, execstream.Limits{})
	reg := nuovoRegistro()
	reg.scrivi(esecuzione(0, jobs.StatusSucceeded))

	stream, err := b.hub.Subscribe("utente", reg, nil)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer stream.Close()

	reg.guasto(errors.New("connessione caduta"))
	b.battito()
	if aperti := b.hub.Open(); aperti != 1 {
		t.Fatalf("un guasto isolato ha chiuso il flusso: aperti = %d", aperti)
	}

	reg.guasto(nil)
	b.battito()
	if got := eventi(stream); len(got) != 1 {
		t.Fatalf("dopo il guasto il flusso non ha ripreso a consegnare: eventi = %d", len(got))
	}

	// Ora il guasto non passa più.
	reg.guasto(errors.New("connessione caduta"))
	for range 10 {
		b.battito()
	}
	if aperti := b.hub.Open(); aperti != 0 {
		t.Errorf("il flusso è rimasto aperto pur non riuscendo più a leggere: aperti = %d", aperti)
	}
	if _, open := <-stream.Events(); open {
		t.Error("il canale degli eventi non è stato chiuso")
	}
}

// TestUnFlussoChiusoNonVieneInterrogato: una lettura per un client che se n'è
// andato è una query pagata per niente, e su una VPS sola le query pagate per
// niente sono la cosa da non fare.
func TestUnFlussoChiusoNonVieneInterrogato(t *testing.T) {
	b := nuovoBanco(t, execstream.Limits{})
	reg := nuovoRegistro()

	stream, err := b.hub.Subscribe("utente", reg, nil)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	b.battito()

	reg.mu.Lock()
	prima := reg.letture
	reg.mu.Unlock()

	stream.Close()
	b.battito()
	b.battito()

	reg.mu.Lock()
	dopo := reg.letture
	reg.mu.Unlock()
	if dopo != prima {
		t.Errorf("letture dopo la chiusura = %d, attese %d", dopo, prima)
	}
}
