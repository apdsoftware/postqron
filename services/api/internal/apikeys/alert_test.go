package apikeys_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
)

// avvisatore raccoglie gli eventi di sicurezza invece di mandarli.
type avvisatore struct {
	visti  []apikeys.SecurityNotice
	errore error
}

func (a *avvisatore) SecurityEvent(_ context.Context, notice apikeys.SecurityNotice) error {
	if a.errore != nil {
		return a.errore
	}
	a.visti = append(a.visti, notice)
	return nil
}

// Creare e revocare una chiave sono eventi di sicurezza: se non è stato il
// proprietario, questi avvisi sono la prima cosa che glielo dicono (R21).
func TestCreazioneERevocaSonoEventiDiSicurezza(t *testing.T) {
	posta := &avvisatore{}
	f := newFixture(t, func(o *apikeys.Options) { o.Notifier = posta })

	created := f.crea(apikeys.ScopeJobsRead)
	if err := f.svc.Revoke(context.Background(), f.user.ID, created.Key.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if len(posta.visti) != 2 {
		t.Fatalf("avvisi: %d, attesi due", len(posta.visti))
	}
	creazione, revoca := posta.visti[0], posta.visti[1]
	switch {
	case creazione.Kind != apikeys.SecurityAPIKeyCreated:
		t.Errorf("primo avviso: %q", creazione.Kind)
	case creazione.UserID != f.user.ID:
		t.Errorf("destinatario: %q", creazione.UserID)
	case creazione.ResourceName != created.Key.Name:
		t.Errorf("risorsa: %q, atteso il nome della chiave", creazione.ResourceName)
	case revoca.Kind != apikeys.SecurityAPIKeyRevoked:
		t.Errorf("secondo avviso: %q", revoca.Kind)
	}

	// Il segreto non entra nell'avviso in nessuna forma: né in chiaro, né come
	// prefisso, né come impronta.
	for _, notice := range posta.visti {
		for _, vietato := range []string{created.Secret, created.Key.Prefix, created.Key.Hash} {
			if vietato == "" {
				continue
			}
			if strings.Contains(notice.ResourceName, vietato) {
				t.Errorf("l'avviso porta con sé un pezzo della chiave: %q", notice.ResourceName)
			}
		}
	}
}

// Un avviso che non parte non impedisce di creare la chiave.
//
// Il caso peggiore che questo test esclude è preciso: un errore restituito al
// client farebbe credere che la chiave non esista, mentre la chiave esiste ed è
// già valida. Una credenziale attiva di cui il proprietario non sa niente è
// peggio di un'email persa.
func TestUnAvvisoCheNonParteNonImpedisceDiCreareLaChiave(t *testing.T) {
	posta := &avvisatore{errore: errors.New("coda irraggiungibile")}
	f := newFixture(t, func(o *apikeys.Options) { o.Notifier = posta })

	created, err := f.svc.Create(context.Background(), f.user.ID, apikeys.CreateInput{
		Name: "CI deploy", Scopes: []apikeys.Scope{apikeys.ScopeJobsRead},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Secret == "" {
		t.Error("nessun segreto restituito")
	}
	if err := f.svc.Revoke(context.Background(), f.user.ID, created.Key.ID); err != nil {
		t.Errorf("Revoke: %v", err)
	}
}
