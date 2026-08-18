package legal_test

import (
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/legal"
)

// Le invarianti del registro sono controlli che girano alla costruzione, non in
// un test: un registro incoerente non è un difetto di qualità, è una prova del
// consenso che non prova quello che dice. Questi test verificano che i controlli
// ci siano, e soprattutto **che rifiutino** — un validatore che accetta tutto è
// indistinguibile da nessun validatore.

const impronta = "0000000000000000000000000000000000000000000000000000000000000000"

func testi() map[legal.Language]legal.Text {
	return map[legal.Language]legal.Text{legal.English: {SHA256: impronta}}
}

// rilascio compone un rilascio valido su cui i test cambiano una cosa alla volta.
func rilascio(versione string, effettivo time.Time, notice legal.Notice) legal.Release {
	return legal.Release{
		Version:   versione,
		Effective: effettivo,
		Announced: effettivo,
		Notice:    notice,
		Texts:     testi(),
	}
}

// registro riempie i documenti che non interessano al test con un rilascio
// minimo: [legal.NewRegistry] li pretende tutti e quattro, perché un documento
// senza rilasci è un documento che nessuno può accettare.
func registro(t *testing.T, doc legal.Document, rilasci ...legal.Release) (*legal.Registry, error) {
	t.Helper()
	base := legal.Date(2026, 1, 1)
	m := map[legal.Document][]legal.Release{}
	for _, altro := range legal.Documents() {
		m[altro] = []legal.Release{rilascio("1.0.0", base, legal.NoticeFirstPublication)}
	}
	m[doc] = rilasci
	return legal.NewRegistry(m)
}

func TestUnaModificaMaterialeSenzaTrentaGiorniDiPreavvisoNonSiPuòDichiarare(t *testing.T) {
	primo := rilascio("1.0.0", legal.Date(2026, 1, 1), legal.NoticeFirstPublication)

	secondo := rilascio("1.1.0", legal.Date(2026, 2, 1), legal.NoticeMaterial)
	secondo.Announced = legal.Date(2026, 1, 20) // dodici giorni
	secondo.Texts[legal.English] = legal.Text{File: "terms-of-service.1.1.0.md", SHA256: impronta}

	_, err := registro(t, legal.TermsOfService, primo, secondo)
	if err == nil {
		t.Fatal("registro accettato con dodici giorni di preavviso su una modifica materiale: " +
			"i Termini §9 ne promettono trenta, e questo è il controllo che impedisce di pubblicarla lo stesso")
	}
	if !strings.Contains(err.Error(), "§9") {
		t.Errorf("l'errore non nomina il paragrafo che si sta violando: %v", err)
	}

	// Con i trenta giorni pieni lo stesso rilascio passa.
	secondo.Announced = secondo.Effective.Add(-legal.MaterialChangeNotice)
	if _, err := registro(t, legal.TermsOfService, primo, secondo); err != nil {
		t.Fatalf("registro rifiutato con il preavviso previsto: %v", err)
	}
}

func TestLaPrimaPubblicazioneNonSiPuòDichiarareDueVolte(t *testing.T) {
	primo := rilascio("1.0.0", legal.Date(2026, 1, 1), legal.NoticeFirstPublication)
	secondo := rilascio("1.1.0", legal.Date(2026, 2, 1), legal.NoticeFirstPublication)
	secondo.Texts[legal.English] = legal.Text{File: "cookie-policy.1.1.0.md", SHA256: impronta}

	// È la scorciatoia che questo controllo esiste per chiudere: dichiarare
	// «prima pubblicazione» su una modifica vera farebbe saltare il preavviso
	// dei trenta giorni senza che nessuno debba dire che sta saltando qualcosa.
	if _, err := registro(t, legal.CookiePolicy, primo, secondo); err == nil {
		t.Fatal("registro accettato con due prime pubblicazioni dello stesso documento")
	}
}

func TestUnRilascioSenzaNoticeNonSiPuòDichiarare(t *testing.T) {
	muto := legal.Release{
		Version:   "1.0.0",
		Effective: legal.Date(2026, 1, 1),
		Announced: legal.Date(2026, 1, 1),
		Texts:     testi(),
	}
	// Il valore zero di Notice non è «non materiale»: è «non dichiarato». La
	// differenza è che il preavviso dei trenta giorni non deve poter saltare
	// perché qualcuno si è dimenticato di scegliere.
	if _, err := registro(t, legal.PrivacyPolicy, muto); err == nil {
		t.Fatal("registro accettato con un rilascio che non dice se la modifica è materiale")
	}
}

func TestLeVersioniDevonoCrescere(t *testing.T) {
	primo := rilascio("1.10.0", legal.Date(2026, 1, 1), legal.NoticeFirstPublication)
	secondo := rilascio("1.9.0", legal.Date(2026, 2, 1), legal.NoticeNotMaterial)
	secondo.Texts[legal.English] = legal.Text{File: "privacy-policy.1.9.0.md", SHA256: impronta}

	if _, err := registro(t, legal.PrivacyPolicy, primo, secondo); err == nil {
		t.Fatal("registro accettato con la 1.9.0 dopo la 1.10.0")
	}

	// E l'ordine è quello dei numeri, non delle stringhe: `1.10.0` viene dopo
	// `1.9.0`, che in ordine alfabetico sarebbe il contrario.
	crescente := rilascio("1.9.0", legal.Date(2026, 1, 1), legal.NoticeFirstPublication)
	dopo := rilascio("1.10.0", legal.Date(2026, 2, 1), legal.NoticeNotMaterial)
	dopo.Texts[legal.English] = legal.Text{File: "privacy-policy.1.10.0.md", SHA256: impronta}
	if _, err := registro(t, legal.PrivacyPolicy, crescente, dopo); err != nil {
		t.Fatalf("registro rifiutato con versioni crescenti: %v", err)
	}
}

func TestDueRilasciNonPossonoCondividereIlTesto(t *testing.T) {
	primo := rilascio("1.0.0", legal.Date(2026, 1, 1), legal.NoticeFirstPublication)
	secondo := rilascio("1.1.0", legal.Date(2026, 2, 1), legal.NoticeNotMaterial)
	// Entrambi puntano al file predefinito: sarebbero due versioni con un testo
	// solo, e la seconda smentirebbe la prima.
	if _, err := registro(t, legal.TermsOfService, primo, secondo); err == nil {
		t.Fatal("registro accettato con due versioni che dichiarano lo stesso file")
	}
}

// TestUnRilascioAnnunciatoNonVincolaPrimaDelGiorno è la proprietà su cui poggia
// tutto il preavviso dei Termini §9: fra l'annuncio e l'entrata in vigore, la
// versione nuova **esiste e non obbliga nessuno**.
func TestUnRilascioAnnunciatoNonVincolaPrimaDelGiorno(t *testing.T) {
	inVigore := rilascio("1.0.0", legal.Date(2026, 1, 1), legal.NoticeFirstPublication)
	annunciato := rilascio("2.0.0", legal.Date(2026, 3, 15), legal.NoticeMaterial)
	annunciato.Announced = legal.Date(2026, 2, 1)
	annunciato.Texts[legal.English] = legal.Text{File: "terms-of-service.2.0.0.md", SHA256: impronta}

	reg, err := registro(t, legal.TermsOfService, inVigore, annunciato)
	if err != nil {
		t.Fatalf("registro: %v", err)
	}

	casi := []struct {
		quando time.Time
		attesa string
		perché string
		futuri int
	}{
		{legal.Date(2026, 2, 10), "1.0.0", "durante il preavviso vincola ancora la versione vecchia", 1},
		{legal.Date(2026, 3, 14), "1.0.0", "il giorno prima non è ancora entrata in vigore", 1},
		{legal.Date(2026, 3, 15), "2.0.0", "il giorno dichiarato la versione nuova vincola", 0},
	}
	for _, caso := range casi {
		rel, ok := reg.InForce(legal.TermsOfService, caso.quando)
		if !ok || rel.Version != caso.attesa {
			t.Errorf("%s: in vigore %q (trovata: %v), attesa %q — %s",
				caso.quando.Format(time.DateOnly), rel.Version, ok, caso.attesa, caso.perché)
		}
		if n := len(reg.Upcoming(caso.quando)); n != caso.futuri {
			t.Errorf("%s: %d cambiamenti annunciati, attesi %d",
				caso.quando.Format(time.DateOnly), n, caso.futuri)
		}
	}
}

// TestLaLinguaMostrataÈQuellaCheEsiste verifica il ripiego di §8-bis: un
// documento non tradotto si mostra in inglese, e la prova deve dire «en» — non
// la lingua dell'interfaccia.
func TestLaLinguaMostrataÈQuellaCheEsiste(t *testing.T) {
	solaInglese := rilascio("1.0.0", legal.Date(2026, 1, 1), legal.NoticeFirstPublication)
	if got := solaInglese.Presented(legal.Italian); got != legal.English {
		t.Errorf("con la sola traduzione inglese, mostrata %q invece di %q", got, legal.English)
	}

	// La traduzione esiste ma nessuno l'ha ancora rivista: sullo schermo c'è
	// comunque l'inglese, e la prova deve dire quello. È il caso di tutte e
	// sedici le traduzioni di oggi.
	inRevisione := solaInglese
	inRevisione.Texts = map[legal.Language]legal.Text{
		legal.English: {SHA256: impronta},
		legal.Italian: {SHA256: impronta, Status: legal.StatusPendingReview},
	}
	if got := inRevisione.Presented(legal.Italian); got != legal.English {
		t.Errorf("con la traduzione in revisione, mostrata %q invece di %q", got, legal.English)
	}
	if !inRevisione.Available(legal.Italian) {
		t.Error("la traduzione in revisione risulta inesistente: esistere e potersi mostrare sono due cose diverse")
	}

	tradotto := solaInglese
	tradotto.Texts = map[legal.Language]legal.Text{
		legal.English: {SHA256: impronta},
		legal.Italian: {SHA256: impronta, Status: legal.StatusApproved},
	}
	if got := tradotto.Presented(legal.Italian); got != legal.Italian {
		t.Errorf("con la traduzione approvata, mostrata %q", got)
	}
}

func TestParseLanguageAccettaLaFormaConLaRegione(t *testing.T) {
	casi := map[string]legal.Language{
		"it":    legal.Italian,
		"it-IT": legal.Italian,
		"EN-gb": legal.English,
		" fr ":  legal.French,
	}
	for input, atteso := range casi {
		got, err := legal.ParseLanguage(input)
		if err != nil || got != atteso {
			t.Errorf("ParseLanguage(%q) = %q, %v; atteso %q", input, got, err, atteso)
		}
	}
	if _, err := legal.ParseLanguage("pt"); err == nil {
		t.Error("il portoghese non è fra le cinque lingue di SPEC §8-bis e non deve essere accettato")
	}
}

// TestIlRegistroDichiaratoÈValido è il controllo che [legal.Current] esegue in
// esercizio, eseguito qui perché fallisca in CI invece che all'avvio.
func TestIlRegistroDichiaratoÈValido(t *testing.T) {
	reg := legal.Current()
	inVigore := reg.InForceAll(time.Now())
	if len(inVigore) != len(legal.Documents()) {
		t.Fatalf("%d documenti in vigore su %d: uno dei quattro non vincola nessuno",
			len(inVigore), len(legal.Documents()))
	}
}
