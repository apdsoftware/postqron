package paddlepg_test

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/paddle"
	"github.com/apdsoftware/postqron/services/api/internal/paddlepg"
)

// L'idempotenza di R16 è un conflitto di chiave primaria, quindi si prova contro
// un database vero: una finzione in memoria proverebbe la finzione, e il caso
// che conta — due copie della stessa consegna che arrivano **insieme** — non
// esiste nemmeno, senza un arbitro condiviso.

var quando = time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)

func nuovoStore(t *testing.T) (*paddlepg.Store, *pgxpool.Pool) {
	t.Helper()
	pool := newTestDatabase(t)
	store, err := paddlepg.New(pool)
	if err != nil {
		t.Fatalf("costruzione dello store: %v", err)
	}
	return store, pool
}

func evento(id string) paddle.Record {
	return paddle.Record{
		ID:             id,
		Type:           paddle.EventSubscriptionUpdated,
		OccurredAt:     quando,
		SubscriptionID: "sub_01",
		CustomerID:     "ctm_01",
		ReceivedAt:     quando,
	}
}

func stato(t *testing.T, pool *pgxpool.Pool, id string) (string, int) {
	t.Helper()
	var s string
	var tentativi int
	if err := pool.QueryRow(t.Context(),
		`SELECT status::text, attempts FROM paddle_webhook_events WHERE event_id = $1`, id).
		Scan(&s, &tentativi); err != nil {
		t.Fatalf("lettura dell'evento: %v", err)
	}
	return s, tentativi
}

func TestClaimRegistraLEventoNuovo(t *testing.T) {
	store, pool := nuovoStore(t)

	preso, err := store.Claim(t.Context(), evento("evt_01"))
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !preso {
		t.Fatal("il primo Claim deve prendere in carico l'evento")
	}
	if s, tentativi := stato(t, pool, "evt_01"); s != "received" || tentativi != 1 {
		t.Fatalf("stato = %s, tentativi = %d", s, tentativi)
	}
}

// Paddle ripete le consegne: la seconda copia non deve produrre un secondo
// upgrade, e non deve nemmeno essere un errore — dire a Paddle che è andata male
// otterrebbe altre copie.
func TestClaimRifiutaLEventoRipetuto(t *testing.T) {
	store, _ := nuovoStore(t)

	if _, err := store.Claim(t.Context(), evento("evt_01")); err != nil {
		t.Fatalf("primo Claim: %v", err)
	}
	if err := store.Complete(t.Context(), "evt_01", paddle.StatusProcessed, "", quando); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	preso, err := store.Claim(t.Context(), evento("evt_01"))
	if err != nil {
		t.Fatalf("secondo Claim: %v", err)
	}
	if preso {
		t.Fatal("un evento già applicato non va rilavorato")
	}
}

// Un evento ancora in lavorazione non si riassegna: due consegne concorrenti
// devono produrre un vincitore, non due.
func TestClaimRifiutaLEventoInLavorazione(t *testing.T) {
	store, _ := nuovoStore(t)

	if _, err := store.Claim(t.Context(), evento("evt_01")); err != nil {
		t.Fatalf("primo Claim: %v", err)
	}
	preso, err := store.Claim(t.Context(), evento("evt_01"))
	if err != nil {
		t.Fatalf("secondo Claim: %v", err)
	}
	if preso {
		t.Fatal("un evento in lavorazione non va riassegnato")
	}
}

// `failed` è **l'unico** stato da cui una ripetizione viene rilavorata: è il
// caso per cui Paddle ripete, e scartarlo come duplicato perderebbe per sempre
// l'evento che ha regalato o tolto un piano.
func TestClaimRiassegnaLEventoFallito(t *testing.T) {
	store, pool := nuovoStore(t)

	if _, err := store.Claim(t.Context(), evento("evt_01")); err != nil {
		t.Fatalf("primo Claim: %v", err)
	}
	if err := store.Complete(t.Context(), "evt_01", paddle.StatusFailed, "database irraggiungibile", quando); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	preso, err := store.Claim(t.Context(), evento("evt_01"))
	if err != nil {
		t.Fatalf("Claim della ripetizione: %v", err)
	}
	if !preso {
		t.Fatal("una ripetizione dopo un fallimento va rilavorata")
	}

	// Il contatore dei tentativi è l'unica traccia che resta dei giri andati male.
	s, tentativi := stato(t, pool, "evt_01")
	if s != "received" || tentativi != 2 {
		t.Fatalf("stato = %s, tentativi = %d, attesi received/2", s, tentativi)
	}

	// E il motivo del fallimento precedente viene ripulito: appartiene al giro
	// che si è chiuso, non a quello che comincia.
	var motivo *string
	if err := pool.QueryRow(t.Context(),
		`SELECT error_message FROM paddle_webhook_events WHERE event_id = 'evt_01'`).Scan(&motivo); err != nil {
		t.Fatalf("lettura: %v", err)
	}
	if motivo != nil {
		t.Fatalf("motivo residuo = %q", *motivo)
	}
}

// Il caso per cui Claim dev'essere **una sola istruzione**: due copie della
// stessa consegna arrivate insieme. Con una SELECT seguita da un INSERT
// passerebbero entrambe.
func TestClaimConcorrentiHannoUnSoloVincitore(t *testing.T) {
	store, _ := nuovoStore(t)

	const copie = 8
	var wg sync.WaitGroup
	esiti := make([]bool, copie)
	errori := make([]error, copie)
	for i := range copie {
		wg.Add(1)
		go func() {
			defer wg.Done()
			esiti[i], errori[i] = store.Claim(t.Context(), evento("evt_01"))
		}()
	}
	wg.Wait()

	vincitori := 0
	for i := range copie {
		if errori[i] != nil {
			t.Fatalf("Claim %d: %v", i, errori[i])
		}
		if esiti[i] {
			vincitori++
		}
	}
	if vincitori != 1 {
		t.Fatalf("vincitori = %d, atteso 1", vincitori)
	}
}

// Gli eventi che non riguardano una sottoscrizione si registrano lo stesso: la
// deduplicazione dev'essere uniforme, e il registro serve a capire cosa è
// arrivato quando un piano non cambia.
func TestEventoSenzaSottoscrizione(t *testing.T) {
	store, pool := nuovoStore(t)

	record := paddle.Record{
		ID:         "evt_prezzo",
		Type:       "price.updated",
		OccurredAt: quando,
		ReceivedAt: quando,
	}
	if _, err := store.Claim(t.Context(), record); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// Le colonne facoltative hanno vincoli che la stringa vuota viola: il valore
	// assente dev'essere NULL, non "".
	var sub, cliente *string
	if err := pool.QueryRow(t.Context(),
		`SELECT paddle_subscription_id, paddle_customer_id
		   FROM paddle_webhook_events WHERE event_id = 'evt_prezzo'`).Scan(&sub, &cliente); err != nil {
		t.Fatalf("lettura: %v", err)
	}
	if sub != nil || cliente != nil {
		t.Fatalf("identità = %v/%v, attese NULL", sub, cliente)
	}
}

// Il vincolo della 0013 esige un motivo quando lo stato è `failed`: un
// fallimento senza motivo è comunque un fallimento da registrare, e non deve
// farsi rifiutare dal database.
func TestCompleteFallitaSenzaMotivo(t *testing.T) {
	store, pool := nuovoStore(t)

	if _, err := store.Claim(t.Context(), evento("evt_01")); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.Complete(t.Context(), "evt_01", paddle.StatusFailed, "", quando); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var motivo *string
	if err := pool.QueryRow(t.Context(),
		`SELECT error_message FROM paddle_webhook_events WHERE event_id = 'evt_01'`).Scan(&motivo); err != nil {
		t.Fatalf("lettura: %v", err)
	}
	if motivo == nil || *motivo == "" {
		t.Fatal("un fallimento senza motivo dev'essere registrato con un motivo di ripiego")
	}
}

func TestCompleteSuUnEventoInesistente(t *testing.T) {
	store, _ := nuovoStore(t)

	if err := store.Complete(t.Context(), "evt_mai_visto", paddle.StatusProcessed, "", quando); err == nil {
		t.Fatal("atteso un errore: la riga doveva esserci")
	}
}

// La retention di questa tabella **è** la finestra in cui la deduplicazione
// vale: cancellata la riga, lo stesso evento ripetuto risulta nuovo.
func TestPurgeEliminaGliEventiVecchi(t *testing.T) {
	store, pool := nuovoStore(t)

	if _, err := store.Claim(t.Context(), evento("evt_vecchio")); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`UPDATE paddle_webhook_events SET received_at = now() - interval '120 days'
		  WHERE event_id = 'evt_vecchio'`); err != nil {
		t.Fatalf("invecchiamento: %v", err)
	}
	if _, err := store.Claim(t.Context(), evento("evt_recente")); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	rimossi, err := store.Purge(t.Context(), 90*24*time.Hour)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if rimossi != 1 {
		t.Fatalf("rimossi = %d, atteso 1", rimossi)
	}

	var rimasti int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM paddle_webhook_events`).Scan(&rimasti); err != nil {
		t.Fatalf("conteggio: %v", err)
	}
	if rimasti != 1 {
		t.Fatalf("rimasti = %d, atteso 1", rimasti)
	}

	if _, err := store.Purge(t.Context(), -time.Hour); err == nil {
		t.Fatal("un grace negativo dev'essere rifiutato")
	}
}

func TestNewRifiutaUnPoolNullo(t *testing.T) {
	if _, err := paddlepg.New(nil); err == nil {
		t.Fatal("atteso un errore")
	}
}

// Senza segreto il servizio è nil e la rotta non viene registrata: su un
// endpoint di fatturazione è l'unica alternativa accettabile a registrarne uno
// che accetta tutto.
func TestNewServiceSenzaSegreto(t *testing.T) {
	pool := newTestDatabase(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc, err := paddlepg.NewService(pool, func(string) string { return "" }, logger, nil)
	if err != nil {
		t.Fatalf("senza segreto non è un errore d'avvio: %v", err)
	}
	if svc != nil {
		t.Fatal("senza segreto il servizio dev'essere nil")
	}

	svc, err = paddlepg.NewService(pool,
		func(k string) string {
			if k == paddle.SecretEnvVar {
				return "pdl_ntfset_segreto_di_prova_lungo"
			}
			return ""
		}, logger, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if svc == nil {
		t.Fatal("con il segreto il servizio dev'esserci")
	}
	if svc.HasSink() {
		t.Error("HasSink() vero senza consumatore")
	}
}
