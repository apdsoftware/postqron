package legal_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/legal"
)

// Questo file prova la cucitura fra il codice e i venti documenti di `legal/`:
// che il registro li conosca tutti, che dicano quello che il registro dichiara,
// e — la parte che conta di più — **che un testo non ancora rivisto non possa
// diventare la lingua di una prova**.
//
// La direzione conta, e vale la pena scriverla: se questi test falliscono, non
// si allinea il documento al codice. Si guarda quale dei due è quello giusto, e
// quasi sempre è il documento, perché è quello che l'utente ha letto e
// accettato.

// caricaVero carica il registro dai file veri del repository.
func caricaVero(t *testing.T) *legal.Registry {
	t.Helper()
	reg, err := legal.Load(filepath.Join(radiceDelRepository(t), "legal"))
	if err != nil {
		t.Fatalf("caricamento di legal/: %v", err)
	}
	return reg
}

// alberoVero copia `legal/` in un filesystem in memoria, così che un test possa
// cambiare una riga di un documento senza toccare il repository.
//
// Parte dai file veri e non da un finto minimo perché ciò che si vuole provare è
// il comportamento **su questi documenti**: un albero inventato proverebbe che il
// caricatore funziona su un albero inventato.
func alberoVero(t *testing.T) fstest.MapFS {
	t.Helper()
	radice := filepath.Join(radiceDelRepository(t), "legal")
	albero := fstest.MapFS{}

	for _, lang := range legal.Languages() {
		dir := filepath.Join(radice, string(lang))
		voci, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("lettura di %s: %v", dir, err)
		}
		for _, voce := range voci {
			if voce.IsDir() || !strings.HasSuffix(voce.Name(), ".md") {
				continue
			}
			contenuto, err := os.ReadFile(filepath.Join(dir, voce.Name()))
			if err != nil {
				t.Fatalf("lettura di %s: %v", voce.Name(), err)
			}
			albero[path.Join(string(lang), voce.Name())] = &fstest.MapFile{Data: contenuto}
		}
	}
	if len(albero) == 0 {
		t.Fatal("nessun documento trovato in legal/")
	}
	return albero
}

// TestIlRegistroConosceTuttiIDocumentiDiLegal è il controllo che ha reso rosso
// questo package quando sono arrivate le sedici traduzioni.
//
// Il difetto che chiude è preciso: un documento legale che il registro non
// conosce è un documento che **nessuno accetterà mai** e di cui non esisterà
// nessuna prova di consenso. Non rompe niente, non ha altri sintomi, e si scopre
// il giorno in cui qualcuno lo contesta.
//
// Adesso il controllo non è più un elenco da confrontare: [legal.Load] fallisce
// su un file che non sa collocare, quindi **caricare il repository e riuscirci è
// la verifica**. Qui si conta solo che i testi trovati siano tutti quelli che
// stanno su disco, perché un caricatore che ne saltasse metà in silenzio
// passerebbe lo stesso.
func TestIlRegistroConosceTuttiIDocumentiDiLegal(t *testing.T) {
	reg := caricaVero(t)
	adesso := time.Now()

	suDisco := len(alberoVero(t))
	var conosciuti int
	for _, dr := range reg.InForceAll(adesso) {
		conosciuti += len(dr.Release.Texts)
	}
	if conosciuti != suDisco {
		t.Errorf("il registro conosce %d testi, in legal/ ce ne sono %d", conosciuti, suDisco)
	}

	// E ogni documento in vigore ha almeno il suo inglese, che è la lingua
	// sorgente e l'unica sempre mostrabile.
	for _, dr := range reg.InForceAll(adesso) {
		testo, ok := dr.Release.Texts[legal.English]
		if !ok {
			t.Errorf("%s %s: nessun testo inglese", dr.Document, dr.Release.Version)
			continue
		}
		if testo.Status != legal.StatusSource {
			t.Errorf("%s: l'inglese ha stato %q, atteso %q", dr.Document, testo.Status, legal.StatusSource)
		}
	}
}

// TestUnaTraduzioneNonApprovataEsisteMaNonSiMostra tiene distinte le due
// proprietà che è comodo confondere.
//
// «Il documento esiste in italiano» e «un consenso può nascere in italiano» sono
// due fatti diversi, e oggi il primo è vero per tutte e quattro le lingue mentre
// il secondo è falso per tutte: le traduzioni sono a `pending-review`, e il sito
// mostra l'originale inglese con il suo avviso (`legal/README.md`).
//
// Se il codice li confondesse, la prova direbbe «accettato in italiano» di un
// testo che nessuno ha ancora validato in italiano — cioè affermerebbe qualcosa
// che non è successo.
func TestUnaTraduzioneNonApprovataEsisteMaNonSiMostra(t *testing.T) {
	reg := caricaVero(t)
	adesso := time.Now()

	traduzioni := reg.Translations(adesso)
	if len(traduzioni) == 0 {
		t.Fatal("nessuna traduzione trovata: o legal/ ha perso le lingue, o il caricatore non le vede")
	}

	for _, tr := range traduzioni {
		rel, ok := reg.InForce(tr.Document, adesso)
		if !ok {
			t.Fatalf("%s: nessun rilascio in vigore", tr.Document)
		}

		// Esiste: il registro lo sa, ed è ciò che permette di accorgersi del
		// giorno in cui verrà approvata.
		if !rel.Available(tr.Language) {
			t.Errorf("%s (%s): la traduzione esiste su disco e il registro non la vede", tr.Document, tr.Language)
		}

		mostrata := rel.Presented(tr.Language)
		switch tr.Status {
		case legal.StatusApproved:
			if mostrata != tr.Language {
				t.Errorf("%s (%s): traduzione approvata e non mostrata", tr.Document, tr.Language)
			}
		case legal.StatusPendingReview:
			if mostrata != legal.English {
				t.Errorf("%s (%s): traduzione a %q mostrata come %q. Il consenso vale su ciò che l'utente ha letto, "+
					"e finché nessuno l'ha rivista l'utente legge l'inglese (legal/README.md)",
					tr.Document, tr.Language, tr.Status, mostrata)
			}
		default:
			t.Errorf("%s (%s): stato %q", tr.Document, tr.Language, tr.Status)
		}
	}
}

// TestApprovareUnaTraduzioneÈUnaRigaSola è il test che difende la regola di
// #447 dal codice di questa issue.
//
// `legal/README.md`: «Approvare è quindi una riga sola — `pending-review` →
// `approved` — e non tocca il testo giuridico né la versione». Se il registro
// portasse anche lui lo stato, quella riga sarebbe due modifiche in due posti, e
// il giorno in cui qualcuno ne facesse una sola il sito mostrerebbe l'italiano
// mentre la prova continuerebbe a dire `en`.
//
// Qui si cambia **quella riga e nient'altro**, in memoria, e si verifica che il
// consenso cambi lingua da solo.
func TestApprovareUnaTraduzioneÈUnaRigaSola(t *testing.T) {
	albero := alberoVero(t)
	percorso := path.Join(string(legal.Italian), "terms-of-service.md")

	prima, err := legal.LoadFS(albero)
	if err != nil {
		t.Fatalf("caricamento: %v", err)
	}
	adesso := time.Now()
	if lingua := prima.ConsentsFor(adesso, legal.Italian, legal.SourceRegistration); lingua[0].Language != legal.English {
		t.Fatalf("con la traduzione in revisione il consenso nasce in %q", lingua[0].Language)
	}

	originale := string(albero[percorso].Data)
	approvato := strings.Replace(originale, "status: pending-review", "status: approved", 1)
	if approvato == originale {
		t.Fatalf("%s non contiene `status: pending-review`: il formato del front matter è cambiato", percorso)
	}
	albero[percorso] = &fstest.MapFile{Data: []byte(approvato)}

	dopo, err := legal.LoadFS(albero)
	if err != nil {
		t.Fatalf("caricamento dopo l'approvazione: %v", err)
	}

	rel, ok := dopo.InForce(legal.TermsOfService, adesso)
	if !ok {
		t.Fatal("nessun rilascio dei Termini in vigore")
	}
	if mostrata := rel.Presented(legal.Italian); mostrata != legal.Italian {
		t.Errorf("dopo l'approvazione la lingua mostrata è %q: approvare ha richiesto di toccare anche il registro", mostrata)
	}

	// E il consenso di chi si registra in italiano nasce in italiano, con
	// l'impronta del testo italiano — non di quello inglese.
	var consenso legal.Consent
	for _, c := range dopo.ConsentsFor(adesso, legal.Italian, legal.SourceRegistration) {
		if c.Document == legal.TermsOfService {
			consenso = c
		}
	}
	if consenso.Language != legal.Italian {
		t.Fatalf("il consenso ai Termini nasce in %q", consenso.Language)
	}
	somma := sha256.Sum256([]byte(corpoDi(t, approvato)))
	if atteso := hex.EncodeToString(somma[:]); consenso.Checksum != atteso {
		t.Errorf("l'impronta registrata non è quella del testo italiano:\n  registrata: %s\n  attesa:     %s",
			consenso.Checksum, atteso)
	}

	// Gli altri tre documenti restano in inglese: approvarne uno non approva
	// gli altri.
	for _, doc := range []legal.Document{legal.PrivacyPolicy, legal.CookiePolicy, legal.AcceptableUsePolicy} {
		altro, _ := dopo.InForce(doc, adesso)
		if mostrata := altro.Presented(legal.Italian); mostrata != legal.English {
			t.Errorf("%s: mostrata in %q dopo l'approvazione di un altro documento", doc, mostrata)
		}
	}
}

// corpoDi restituisce il testo dopo il front matter, che è ciò che entra
// nell'impronta.
func corpoDi(t *testing.T, sorgente string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)\A---\n.*?\n---\n+`).FindStringIndex(sorgente)
	if m == nil {
		t.Fatal("front matter non riconosciuto")
	}
	return sorgente[m[1]:]
}

// TestIlCaricatoreRifiutaCiòCheNonSaCollocare elenca i modi in cui `legal/` può
// smettere di corrispondere al registro.
//
// Ognuno è un difetto che non ha altri sintomi: il documento in più che nessuno
// accetterà mai, la versione che il registro non conosce, il testo riscritto
// sotto la stessa versione, la traduzione senza stato. Tutti si scoprono qui o
// non si scoprono affatto.
func TestIlCaricatoreRifiutaCiòCheNonSaCollocare(t *testing.T) {
	casi := map[string]struct {
		guasta func(fstest.MapFS)
		attesa string
	}{
		"un quinto documento legale": {
			guasta: func(albero fstest.MapFS) {
				albero["en/data-processing-agreement.md"] = &fstest.MapFile{Data: []byte(
					"---\ndocument: data-processing-agreement\nversion: 1.0.0\neffective_date: 2026-08-18\nlanguage: en\n---\n\n# DPA\n")}
			},
			attesa: "nessuno accetterà mai",
		},
		"una versione che il registro non dichiara": {
			guasta: func(albero fstest.MapFS) {
				sostituisci(albero, "en/cookie-policy.md", "version: 1.0.0", "version: 1.4.0")
			},
			attesa: "è una decisione",
		},
		"una data che non è quella dichiarata": {
			guasta: func(albero fstest.MapFS) {
				sostituisci(albero, "en/cookie-policy.md", "effective_date: 2026-08-17", "effective_date: 2026-09-01")
			},
			attesa: "entra in vigore il",
		},
		"il testo inglese riscritto sotto la stessa versione": {
			guasta: func(albero fstest.MapFS) {
				sostituisci(albero, "en/cookie-policy.md", "# Cookie Policy", "# Cookie Policy (rivista)")
			},
			attesa: "senza cambiare versione",
		},
		"una traduzione senza stato": {
			guasta: func(albero fstest.MapFS) {
				sostituisci(albero, "it/cookie-policy.md", "status: pending-review\n", "")
			},
			attesa: "non dichiara `status`",
		},
		"una traduzione con uno stato inventato": {
			guasta: func(albero fstest.MapFS) {
				sostituisci(albero, "it/cookie-policy.md", "status: pending-review", "status: quasi")
			},
			attesa: "non è né `pending-review` né `approved`",
		},
		"l'inglese che dichiara uno stato": {
			guasta: func(albero fstest.MapFS) {
				sostituisci(albero, "en/cookie-policy.md", "language: en", "language: en\nstatus: approved")
			},
			attesa: "lingua sorgente",
		},
		"una traduzione nella directory sbagliata": {
			guasta: func(albero fstest.MapFS) {
				sostituisci(albero, "de/cookie-policy.md", "language: de", "language: fr")
			},
			attesa: "ma sta in",
		},
	}

	for nome, caso := range casi {
		t.Run(nome, func(t *testing.T) {
			albero := alberoVero(t)
			caso.guasta(albero)

			_, err := legal.LoadFS(albero)
			if err == nil {
				t.Fatal("caricamento riuscito su un albero che non corrisponde al registro")
			}
			if !strings.Contains(err.Error(), caso.attesa) {
				t.Errorf("l'errore non spiega cosa è successo: %v", err)
			}
		})
	}
}

func sostituisci(albero fstest.MapFS, percorso, vecchio, nuovo string) {
	file, ok := albero[percorso]
	if !ok {
		panic("percorso assente dall'albero di prova: " + percorso)
	}
	albero[percorso] = &fstest.MapFile{Data: []byte(strings.Replace(string(file.Data), vecchio, nuovo, 1))}
}

// TestIlCaricamentoTolleraUnaLinguaAssente: una lingua non ancora tradotta non è
// un guasto, è il caso normale prima che la traduzione arrivi.
func TestIlCaricamentoTolleraUnaLinguaAssente(t *testing.T) {
	albero := alberoVero(t)
	for percorso := range albero {
		if strings.HasPrefix(percorso, "fr/") {
			delete(albero, percorso)
		}
	}

	reg, err := legal.LoadFS(albero)
	if err != nil {
		t.Fatalf("caricamento senza il francese: %v", err)
	}
	rel, _ := reg.InForce(legal.TermsOfService, time.Now())
	if rel.Available(legal.French) {
		t.Error("il registro dichiara disponibile una traduzione che non esiste")
	}
	if mostrata := rel.Presented(legal.French); mostrata != legal.English {
		t.Errorf("senza il francese la lingua mostrata è %q", mostrata)
	}
}

// TestSenzaLIngleseNonSiCarica: la lingua sorgente non è opzionale. Senza,
// non esisterebbe nessun testo su cui ripiegare, e ogni consenso sarebbe una
// dichiarazione su un documento che non c'è.
func TestSenzaLIngleseNonSiCarica(t *testing.T) {
	albero := alberoVero(t)
	for percorso := range albero {
		if strings.HasPrefix(percorso, "en/") {
			delete(albero, percorso)
		}
	}
	if _, err := legal.LoadFS(albero); err == nil {
		t.Fatal("caricamento riuscito senza i testi inglesi")
	}
}

// TestIlPreavvisoÈQuelloDeiTermini rilegge dai Termini §9 il numero di giorni,
// invece di fidarsi della costante.
//
// È lo stesso controllo che internal/account fa sulla grazia della privacy
// policy, sull'altra promessa di calendario che i documenti fanno: se una
// revisione legale porta il preavviso da trenta a sessanta giorni, il registro
// deve accorgersene — continuare a validare rilasci con trenta giorni di
// preavviso significherebbe pubblicare una modifica materiale senza il
// preavviso che il documento promette.
func TestIlPreavvisoÈQuelloDeiTermini(t *testing.T) {
	percorso := filepath.Join(radiceDelRepository(t), "legal", "en", "terms-of-service.md")
	testo, err := os.ReadFile(percorso)
	if err != nil {
		t.Fatalf("Termini non leggibili (%s): %v", percorso, err)
	}

	// Il documento spezza la frase su più righe: «we give you\n30 days'\nnotice».
	// L'espressione tollera qualunque spaziatura proprio per questo — la
	// formattazione del paragrafo cambia a ogni riedizione, e un test legato agli
	// a capo fallirebbe per un motivo che non interessa a nessuno.
	preavviso := regexp.MustCompile(`(?is)we\s+give\s+you\s+(\d+)\s+days'?\s+notice`)
	m := preavviso.FindSubmatch(testo)
	if m == nil {
		t.Fatalf("in %s non si trova più la frase «we give you N days' notice» (§9): "+
			"o il documento è cambiato e il registro va riallineato, o questo test guarda il file sbagliato", percorso)
	}

	giorni, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatalf("numero di giorni non leggibile: %v", err)
	}
	dichiarato := time.Duration(giorni) * 24 * time.Hour

	if legal.MaterialChangeNotice != dichiarato {
		t.Fatalf("i Termini §9 promettono %d giorni di preavviso, il registro ne pretende %v.\n"+
			"Non allineare il documento al codice senza guardare: il numero che l'utente ha letto e accettato è quello del documento (R46).",
			giorni, legal.MaterialChangeNotice)
	}
}

// TestLeQuattroVersioniNonSonoAllineate è la verifica del fatto che ha deciso la
// forma di questa traccia.
//
// I quattro documenti stanno a versioni diverse — 1.2.0, 1.1.0, 1.0.0, 1.0.0 — e
// finché è così, un consenso registrato con **una versione sola per tutti e
// quattro** è falso.
func TestLeQuattroVersioniNonSonoAllineate(t *testing.T) {
	versioni := map[string][]legal.Document{}
	for _, dr := range caricaVero(t).InForceAll(time.Now()) {
		versioni[dr.Release.Version] = append(versioni[dr.Release.Version], dr.Document)
	}
	switch len(versioni) {
	case 0:
		t.Fatal("nessun documento in vigore: il registro non descrive niente")
	case 1:
		t.Skip("i quattro documenti sono tutti alla stessa versione: oggi una versione sola basterebbe, " +
			"ma la prima riedizione di uno solo di loro li disallinea di nuovo")
	default:
		t.Logf("versioni in vigore: %v", versioni)
	}
}

// TestLaTraduzioneRipeteVersioneEDataDellOriginale è la regola di
// `legal/README.md` verificata sul lato Go.
//
// Il caricatore la applica già — una data diversa fa fallire il caricamento — e
// questo test dice che l'ha applicata a tutte e sedici, non a nessuna.
func TestLaTraduzioneRipeteVersioneEDataDellOriginale(t *testing.T) {
	radice := radiceDelRepository(t)
	reg := caricaVero(t)
	adesso := time.Now()

	for _, tr := range reg.Translations(adesso) {
		rel, _ := reg.InForce(tr.Document, adesso)
		percorso := filepath.Join(radice, "legal", string(tr.Language), rel.FileName(tr.Document, tr.Language))
		contenuto, err := os.ReadFile(percorso)
		if err != nil {
			t.Errorf("%s: %v", percorso, err)
			continue
		}
		testo := string(contenuto)
		if !strings.Contains(testo, "version: "+rel.Version) {
			t.Errorf("%s: non dichiara `version: %s`", percorso, rel.Version)
		}
		if !strings.Contains(testo, "effective_date: "+rel.Effective.Format(time.DateOnly)) {
			t.Errorf("%s: non dichiara `effective_date: %s`", percorso, rel.Effective.Format(time.DateOnly))
		}
	}
}

// radiceDelRepository risale dalla directory del test fino a trovare `legal/`.
//
// Il numero di livelli non è scritto a mano perché cambierebbe al primo
// spostamento del package: si risale finché la directory cercata non compare.
func radiceDelRepository(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("directory corrente: %v", err)
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, "legal", "en")); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("radice del repository non trovata: nessuna directory `legal/en` risalendo da qui")
		}
		dir = parent
	}
}
