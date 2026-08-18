package legalpg_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/apdsoftware/postqron/services/api/internal/legal"
)

// Questi test girano contro PostgreSQL vero perché ciò che verificano **non
// esiste in Go**: l'immutabilità di un consenso, l'unicità per (documento,
// versione) e la cascata dalla cancellazione dell'account sono proprietà dello
// schema, e provarle con uno store finto proverebbe soltanto che lo store finto
// è d'accordo con chi lo ha scritto.

func consenso(doc legal.Document, versione string, lingua legal.Language, quando time.Time) legal.Consent {
	return legal.Consent{
		Document:   doc,
		Version:    versione,
		Language:   lingua,
		Checksum:   "1692f61d85fc243307da45b65501497a5196d0f27de0ebc6d6a7cfab0da94bc9",
		Source:     legal.SourceRegistration,
		AcceptedAt: quando,
	}
}

// TestIlConsensoRegistraVersioneELinguaPerOgniDocumento è R46 alla lettera, su
// database vero.
func TestIlConsensoRegistraVersioneELinguaPerOgniDocumento(t *testing.T) {
	store, pool := newStore(t)
	userID := nuovoUtente(t, pool, "alfa")

	quando := time.Now().Truncate(time.Millisecond)
	atteso := legal.Current().ConsentsFor(quando, legal.Italian, legal.SourceRegistration)
	if len(atteso) != len(legal.Documents()) {
		t.Fatalf("%d consensi per %d documenti", len(atteso), len(legal.Documents()))
	}
	if err := store.Record(t.Context(), userID, atteso); err != nil {
		t.Fatalf("registrazione: %v", err)
	}

	letti, err := store.ConsentsOf(t.Context(), userID)
	if err != nil {
		t.Fatalf("lettura: %v", err)
	}
	if len(letti) != len(atteso) {
		t.Fatalf("%d consensi riletti, %d scritti", len(letti), len(atteso))
	}

	perDocumento := map[legal.Document]legal.Consent{}
	for _, c := range letti {
		perDocumento[c.Document] = c
	}
	for _, scritto := range atteso {
		riletto, presente := perDocumento[scritto.Document]
		if !presente {
			t.Errorf("%s: nessun consenso registrato", scritto.Document)
			continue
		}
		if riletto.Version != scritto.Version {
			t.Errorf("%s: versione %q, attesa %q", scritto.Document, riletto.Version, scritto.Version)
		}
		if riletto.Language != scritto.Language {
			t.Errorf("%s: lingua %q, attesa %q", scritto.Document, riletto.Language, scritto.Language)
		}
		if riletto.Checksum != scritto.Checksum {
			t.Errorf("%s: impronta %q, attesa %q", scritto.Document, riletto.Checksum, scritto.Checksum)
		}
		if !riletto.AcceptedAt.Equal(scritto.AcceptedAt) {
			t.Errorf("%s: data %s, attesa %s", scritto.Document, riletto.AcceptedAt, scritto.AcceptedAt)
		}
	}

	// E le versioni **non sono tutte uguali**: è il fatto per cui questa tabella
	// ha una riga per documento invece di una per accettazione.
	versioni := map[string]bool{}
	for _, c := range letti {
		versioni[c.Version] = true
	}
	if len(versioni) < 2 {
		t.Errorf("i quattro documenti risultano tutti alla stessa versione (%v): "+
			"o il registro è sbagliato, o questa tabella sta appiattendo qualcosa che non è piatto", versioni)
	}
}

// TestDueDocumentiAVersioniDiverseNonSiConfondonoSulDatabase verifica che
// l'unicità sia sulla **coppia** e non sul documento: la stessa versione di due
// documenti diversi convive, e due versioni dello stesso documento pure.
func TestDueDocumentiAVersioniDiverseNonSiConfondonoSulDatabase(t *testing.T) {
	store, pool := newStore(t)
	userID := nuovoUtente(t, pool, "beta")
	quando := time.Now()

	err := store.Record(t.Context(), userID, []legal.Consent{
		consenso(legal.TermsOfService, "1.2.0", legal.English, quando),
		consenso(legal.PrivacyPolicy, "1.1.0", legal.English, quando),
		consenso(legal.CookiePolicy, "1.0.0", legal.German, quando),
		consenso(legal.AcceptableUsePolicy, "1.0.0", legal.German, quando),
	})
	if err != nil {
		t.Fatalf("registrazione: %v", err)
	}

	// La versione successiva dei Termini si aggiunge, non sostituisce: la 1.2.0
	// resta, ed è la prova di cosa vincolava allora.
	if err := store.Record(t.Context(), userID, []legal.Consent{
		consenso(legal.TermsOfService, "1.3.0", legal.Italian, quando.Add(time.Hour)),
	}); err != nil {
		t.Fatalf("registrazione della versione successiva: %v", err)
	}

	letti, err := store.ConsentsOf(t.Context(), userID)
	if err != nil {
		t.Fatalf("lettura: %v", err)
	}
	if len(letti) != 5 {
		t.Fatalf("%d consensi, attesi 5", len(letti))
	}

	var termini []legal.Consent
	for _, c := range letti {
		if c.Document == legal.TermsOfService {
			termini = append(termini, c)
		}
	}
	if len(termini) != 2 {
		t.Fatalf("%d consensi sui Termini, attesi 2", len(termini))
	}
	if termini[0].Version != "1.2.0" || termini[0].Language != legal.English {
		t.Errorf("il primo consenso è %s/%s, atteso 1.2.0/en", termini[0].Version, termini[0].Language)
	}
	if termini[1].Version != "1.3.0" || termini[1].Language != legal.Italian {
		t.Errorf("il secondo consenso è %s/%s, atteso 1.3.0/it", termini[1].Version, termini[1].Language)
	}
}

// TestRegistrareDueVolteLaStessaVersioneNonSpostaLaData è la proprietà che
// distingue una prova da un contatore.
func TestRegistrareDueVolteLaStessaVersioneNonSpostaLaData(t *testing.T) {
	store, pool := newStore(t)
	userID := nuovoUtente(t, pool, "gamma")

	primo := time.Now().Add(-48 * time.Hour).Truncate(time.Millisecond)
	if err := store.Record(t.Context(), userID,
		[]legal.Consent{consenso(legal.TermsOfService, "1.2.0", legal.English, primo)}); err != nil {
		t.Fatalf("prima registrazione: %v", err)
	}

	secondo := consenso(legal.TermsOfService, "1.2.0", legal.German, time.Now())
	secondo.Source = legal.SourceReacceptance
	if err := store.Record(t.Context(), userID, []legal.Consent{secondo}); err != nil {
		t.Fatalf("seconda registrazione: %v", err)
	}

	letti, err := store.ConsentsOf(t.Context(), userID)
	if err != nil {
		t.Fatalf("lettura: %v", err)
	}
	if len(letti) != 1 {
		t.Fatalf("%d righe per la stessa versione, attesa 1", len(letti))
	}
	if !letti[0].AcceptedAt.Equal(primo) {
		t.Errorf("data del consenso = %s, attesa %s: la seconda registrazione l'ha spostata",
			letti[0].AcceptedAt, primo)
	}
	if letti[0].Language != legal.English || letti[0].Source != legal.SourceRegistration {
		t.Errorf("la seconda registrazione ha riscritto lingua o origine: %+v", letti[0])
	}
}

// TestUnConsensoNonSiPuòRiscrivere è la verifica del trigger della 0018.
//
// Non passa dallo store — lo store non ha un UPDATE da chiamare — ma da SQL
// diretto, che è il modo in cui il consenso verrebbe riscritto davvero: da
// qualcuno con accesso al database, non dall'API.
func TestUnConsensoNonSiPuòRiscrivere(t *testing.T) {
	store, pool := newStore(t)
	userID := nuovoUtente(t, pool, "delta")

	if err := store.Record(t.Context(), userID,
		[]legal.Consent{consenso(legal.TermsOfService, "1.2.0", legal.English, time.Now())}); err != nil {
		t.Fatalf("registrazione: %v", err)
	}

	riscritture := map[string]string{
		"retrodatare l'accettazione": `UPDATE legal_consents SET accepted_at = now() - interval '1 year'`,
		"cambiare la versione":       `UPDATE legal_consents SET version = '9.9.9'`,
		"cambiare la lingua":         `UPDATE legal_consents SET language = 'de'`,
		"cambiare l'impronta":        `UPDATE legal_consents SET document_checksum = repeat('a', 64)`,
	}
	for nome, sql := range riscritture {
		_, err := pool.Exec(t.Context(), sql)
		if err == nil {
			t.Errorf("%s: riuscito. Un consenso che si può riscrivere non prova niente (R46)", nome)
			continue
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23001" {
			t.Errorf("%s: rifiutato con un errore inatteso: %v", nome, err)
		}
	}

	// E cancellarlo lasciando l'utente al suo posto è la stessa cosa vista
	// dall'altra parte: si distruggerebbe la prova senza toccare il rapporto che
	// prova.
	if _, err := pool.Exec(t.Context(), `DELETE FROM legal_consents WHERE user_id = $1::uuid`, userID); err == nil {
		t.Error("cancellazione di un consenso con l'account ancora vivo riuscita")
	}

	if n := conta(t, pool, `SELECT count(*) FROM legal_consents WHERE user_id = $1::uuid`, userID); n != 1 {
		t.Errorf("%d consensi dopo i tentativi di riscrittura, atteso 1", n)
	}
}

// TestIConsensiSeguonoLAccountCancellato verifica la decisione dichiarata nella
// 0018: la prova se ne va con l'account.
//
// La cancellazione vera passa da internal/accountpg e ha i suoi test; qui si
// verifica il pezzo che appartiene a questa tabella — la cascata — e si verifica
// **su un DELETE diretto su `users`**, che è l'ultima istruzione della purga.
func TestIConsensiSeguonoLAccountCancellato(t *testing.T) {
	store, pool := newStore(t)
	restante := nuovoUtente(t, pool, "epsilon")
	cancellato := nuovoUtente(t, pool, "zeta")

	quando := time.Now()
	for _, id := range []string{restante, cancellato} {
		if err := store.Record(t.Context(), id,
			legal.Current().ConsentsFor(quando, legal.English, legal.SourceRegistration)); err != nil {
			t.Fatalf("registrazione: %v", err)
		}
	}

	if _, err := pool.Exec(t.Context(), `DELETE FROM users WHERE id = $1::uuid`, cancellato); err != nil {
		t.Fatalf("cancellazione dell'account: %v", err)
	}

	if n := conta(t, pool, `SELECT count(*) FROM legal_consents WHERE user_id = $1::uuid`, cancellato); n != 0 {
		t.Errorf("%d consensi sopravvissuti alla cancellazione dell'account: "+
			"sono dati personali di qualcuno a cui abbiamo dichiarato di aver rimosso tutto (privacy policy §5)", n)
	}
	if n := conta(t, pool, `SELECT count(*) FROM legal_consents WHERE user_id = $1::uuid`, restante); n != len(legal.Documents()) {
		t.Errorf("l'altro account ha %d consensi invece di %d: la cascata ha portato via righe di qualcun altro",
			n, len(legal.Documents()))
	}
}

// TestLoSchemaRifiutaIValoriFuoriDominio verifica i CHECK della 0018.
//
// Non sono ridondanti rispetto alla validazione in Go: il database è l'ultimo
// posto in cui una riga sbagliata si può fermare, ed è l'unico che vale anche
// per chi non passa dall'API.
func TestLoSchemaRifiutaIValoriFuoriDominio(t *testing.T) {
	_, pool := newStore(t)
	userID := nuovoUtente(t, pool, "eta")

	casi := map[string][]any{
		"un documento che non esiste": {"marketing-consent", "1.0.0", "en", "en"},
		"una versione senza forma":    {"terms-of-service", "ultima", "en", "en"},
		"una lingua fuori da §8-bis":  {"terms-of-service", "1.2.0", "pt", "pt"},
	}
	for nome, args := range casi {
		_, err := pool.Exec(t.Context(),
			`INSERT INTO legal_consents (user_id, document, version, language, document_checksum, source)
			 VALUES ($1::uuid, $2, $3, $4, repeat('a', 64), 'registration')`,
			userID, args[0], args[1], args[2])
		if err == nil {
			t.Errorf("%s: inserimento riuscito", nome)
		}
	}

	// E un'impronta che non è un'impronta.
	_, err := pool.Exec(t.Context(),
		`INSERT INTO legal_consents (user_id, document, version, language, document_checksum, source)
		 VALUES ($1::uuid, 'terms-of-service', '1.2.0', 'en', 'non-e-una-impronta', 'registration')`, userID)
	if err == nil {
		t.Error("impronta malformata accettata")
	}
}
