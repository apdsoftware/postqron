package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/account"
)

// storePurgabile è uno store che dice quali account sono scaduti e come va la
// loro purga. Il database ha i suoi test in internal/accountpg: qui si verifica
// la sola cosa che vive in questo package, cioè **cosa fa la passata quando un
// account non si lascia purgare**.
type storePurgabile struct {
	due       []string
	esiti     map[string]account.Purged
	guasti    map[string]error
	purgati   []string
	limite    int
	daNonDare error
}

func (s *storePurgabile) Status(context.Context, string) (account.Status, error) {
	return account.Status{}, nil
}

func (s *storePurgabile) RequestDeletion(context.Context, string, time.Time, time.Time) (account.Receipt, error) {
	return account.Receipt{}, nil
}

func (s *storePurgabile) CancelDeletion(context.Context, string) (account.Restored, error) {
	return account.Restored{}, nil
}

func (s *storePurgabile) DueForPurge(_ context.Context, _ time.Time, limit int) ([]string, error) {
	if s.daNonDare != nil {
		return nil, s.daNonDare
	}
	s.limite = limit
	if len(s.due) > limit {
		return s.due[:limit], nil
	}
	return s.due, nil
}

func (s *storePurgabile) Purge(_ context.Context, userID string) (account.Purged, error) {
	s.purgati = append(s.purgati, userID)
	return s.esiti[userID], s.guasti[userID]
}

func nuovaPassata(t *testing.T, store *storePurgabile, maxAccounts int) *account.Purger {
	t.Helper()
	p, err := account.NewPurger(account.PurgeOptions{
		Store:       store,
		MaxAccounts: maxAccounts,
		Now:         func() time.Time { return time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("costruzione della passata: %v", err)
	}
	return p
}

// TestUnAccountCheFallisceNonBloccaGliAltri è la proprietà per cui gli errori si
// accumulano invece di fermare il ciclo.
//
// Il costo dell'alternativa non è teorico: fermarsi al primo errore
// significherebbe che un solo account in stato strano tiene in vita a tempo
// indeterminato i dati di tutti quelli in coda dietro di lui — cioè trasforma un
// guasto in una promessa non mantenuta, e per persone che non c'entrano niente.
func TestUnAccountCheFallisceNonBloccaGliAltri(t *testing.T) {
	guasto := errors.New("connessione persa")
	store := &storePurgabile{
		due:    []string{"u-1", "u-2", "u-3"},
		guasti: map[string]error{"u-2": guasto},
		esiti: map[string]account.Purged{
			"u-1": {Executions: 10},
			"u-3": {Executions: 5},
		},
	}
	passata := nuovaPassata(t, store, 10)

	stats, err := passata.Sweep(t.Context())
	if !errors.Is(err, guasto) {
		t.Fatalf("errore = %v, atteso quello dell'account guasto", err)
	}
	if len(store.purgati) != 3 {
		t.Errorf("account tentati = %v, attesi tutti e tre", store.purgati)
	}
	if stats.Accounts != 2 {
		t.Errorf("account purgati = %d, attesi 2", stats.Accounts)
	}
	if stats.Executions != 15 {
		t.Errorf("esecuzioni cancellate = %d, attese 15", stats.Executions)
	}
}

// TestUnAccountLasciatoAMetàNonSiContaComeFatto: la purga di un account grosso
// si spezza su più passate, e finché non è finita l'account non è purgato.
//
// Contarlo sarebbe peggio di un numero sbagliato in un log: significherebbe
// dichiarare mantenuta una promessa che è a metà.
func TestUnAccountLasciatoAMetàNonSiContaComeFatto(t *testing.T) {
	store := &storePurgabile{
		due:   []string{"u-1"},
		esiti: map[string]account.Purged{"u-1": {Executions: 5000, Batches: 1, Truncated: true}},
	}
	passata := nuovaPassata(t, store, 10)

	stats, err := passata.Sweep(t.Context())
	if err != nil {
		t.Fatalf("passata: %v", err)
	}
	if stats.Accounts != 0 {
		t.Errorf("account purgati = %d, atteso 0: quello c'era ancora", stats.Accounts)
	}
	if !stats.Truncated {
		t.Error("la passata non dichiara di aver lasciato lavoro indietro")
	}
	if stats.Executions != 5000 {
		t.Errorf("esecuzioni cancellate = %d, attese 5000: il lavoro fatto va contato", stats.Executions)
	}
}

// TestIlTettoDiAccountPerPassataSiDichiara: un tetto silenzioso si legge come
// «ho finito» quando non è vero. È la stessa scelta di internal/retention, dove
// sovrastimare il lavoro rimasto è deliberato.
func TestIlTettoDiAccountPerPassataSiDichiara(t *testing.T) {
	store := &storePurgabile{
		due:   []string{"u-1", "u-2", "u-3"},
		esiti: map[string]account.Purged{},
	}
	passata := nuovaPassata(t, store, 2)

	stats, err := passata.Sweep(t.Context())
	if err != nil {
		t.Fatalf("passata: %v", err)
	}
	if store.limite != 2 {
		t.Errorf("limite chiesto allo store = %d, atteso 2", store.limite)
	}
	if stats.Accounts != 2 {
		t.Errorf("account purgati = %d, attesi 2", stats.Accounts)
	}
	if !stats.Truncated {
		t.Error("il tetto è stato raggiunto e la passata non lo dichiara")
	}
}

// TestUnaPassataSenzaScadutiNonFaNiente è il caso normale — la stragrande
// maggioranza delle passate — e va verificato per una ragione precisa: la purga
// è un'operazione irreversibile che gira ogni ora su un database di produzione,
// e «non fa niente quando non c'è niente da fare» è la sua proprietà più
// importante.
func TestUnaPassataSenzaScadutiNonFaNiente(t *testing.T) {
	store := &storePurgabile{}
	passata := nuovaPassata(t, store, 10)

	stats, err := passata.Sweep(t.Context())
	if err != nil {
		t.Fatalf("passata: %v", err)
	}
	if len(store.purgati) != 0 {
		t.Errorf("account purgati senza che nessuno fosse scaduto: %v", store.purgati)
	}
	if stats.Due != 0 || stats.Accounts != 0 || stats.Truncated {
		t.Errorf("resoconto = %+v, atteso vuoto", stats)
	}
}

// TestUnErroreNellaRicercaNonPurgaNiente: se non si riesce a sapere chi è
// scaduto, non si cancella nessuno. È l'errore da cui non si prosegue, a
// differenza di quello su un singolo account.
func TestUnErroreNellaRicercaNonPurgaNiente(t *testing.T) {
	guasto := errors.New("database non raggiungibile")
	store := &storePurgabile{due: []string{"u-1"}, daNonDare: guasto}
	passata := nuovaPassata(t, store, 10)

	if _, err := passata.Sweep(t.Context()); !errors.Is(err, guasto) {
		t.Fatalf("errore = %v, atteso quello della ricerca", err)
	}
	if len(store.purgati) != 0 {
		t.Errorf("account purgati nonostante la ricerca fallita: %v", store.purgati)
	}
}
