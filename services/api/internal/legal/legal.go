// Package legal è il registro dei documenti legali e la prova del consenso
// (R46).
//
// # Che cosa deve poter dimostrare
//
// R46 chiede di registrare «versione, data e lingua in cui il consenso è stato
// prestato, per ogni documento». Le tre cose insieme, e per una ragione che si
// vede solo il giorno in cui serve: se un utente contesta una clausola, la
// domanda è **cosa aveva davanti quando ha accettato**, e la risposta dev'essere
// ricostruibile senza interpretazioni.
//
//   - *versione*, perché i quattro documenti non sono allineati e non lo saranno
//     mai più: oggi i Termini sono alla 1.2.0, la privacy policy alla 1.1.0, le
//     altre due alla 1.0.0. Un consenso registrato con una versione sola per
//     tutti e quattro sarebbe già falso adesso;
//   - *lingua*, perché il consenso vale su ciò che l'utente ha effettivamente
//     letto (SPEC §8-bis). Chi ha accettato leggendo l'italiano non ha accettato
//     l'inglese, anche se i due testi vogliono dire la stessa cosa;
//   - *data*, perché è l'istante in cui l'utente si è vincolato, e non coincide
//     con nessuna data del documento.
//
// A queste tre questo package ne aggiunge una quarta che R46 non chiede:
// l'**impronta del testo**. Versione e lingua identificano il documento solo
// finché ci si fida del repository; l'impronta rende la risposta verificabile
// anche da fuori, ed è ciò che rende operativa la regola di `legal/README.md` —
// «non si modifica un documento in vigore» — invece di lasciarla alla
// disciplina di chi scrive. Vedi [Release.Texts] e il test che confronta le
// impronte dichiarate con i file veri.
//
// # Che cosa questo package **non** è
//
// **Non è il consenso al marketing** (#476). Quello ha una base giuridica
// diversa — il consenso dell'Art. 6(1)(a), non l'esecuzione del contratto — è
// revocabile in qualunque momento, e la privacy policy §2.8 dice che non viene
// «mai chiesto insieme all'accettazione dei termini o alla creazione
// dell'account». Mescolare le due tracce in una tabella sola renderebbe
// impossibile revocare l'uno senza toccare l'altro, che è precisamente la
// distinzione che quella frase promette.
//
// **Non serve i testi.** Il registro sa quali documenti esistono, a che versione
// e in che lingue; il testo lo servono le pagine legali del sito. Un'API che
// restituisse il contenuto dei documenti duplicherebbe una responsabilità che è
// già di qualcun altro.
package legal

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ----------------------------------------------------------------- documenti

// Document è uno dei quattro documenti legali di `legal/`.
//
// I valori sono i nomi dei file senza estensione, e non è una comodità: sono
// anche il campo `document` del frontmatter, il valore che finisce nella colonna
// `legal_consents.document` e quello che l'API espone. Un nome solo per tutti e
// quattro i posti significa che non esiste una tabella di conversione da
// sbagliare.
type Document string

const (
	// TermsOfService è il contratto con l'utente. È il documento che si accetta
	// creando l'account: «By creating an account you accept them, together with
	// the Acceptable Use Policy and the Privacy Policy».
	TermsOfService Document = "terms-of-service"
	// PrivacyPolicy descrive il trattamento dei dati personali.
	PrivacyPolicy Document = "privacy-policy"
	// AcceptableUsePolicy dice cosa non si può fare con il servizio.
	AcceptableUsePolicy Document = "acceptable-use-policy"
	// CookiePolicy copre i cookie e le tecnologie simili.
	CookiePolicy Document = "cookie-policy"
)

// Documents elenca i documenti nell'ordine in cui hanno senso per chi legge: il
// contratto, poi ciò che vincola l'uso, poi le due informative.
func Documents() []Document {
	return []Document{TermsOfService, AcceptableUsePolicy, PrivacyPolicy, CookiePolicy}
}

// ParseDocument riconosce un documento dichiarato da un client.
func ParseDocument(s string) (Document, error) {
	doc := Document(strings.TrimSpace(s))
	if slices.Contains(Documents(), doc) {
		return doc, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownDocument, s)
}

// ------------------------------------------------------------------- lingue

// Language è una delle cinque lingue di SPEC §8-bis.
//
// È lo stesso dominio del vincolo `users_language_check` della 0015, e lo stesso
// della colonna `legal_consents.language`: le lingue del prodotto e le lingue in
// cui si può prestare un consenso sono lo stesso insieme, perché l'utente legge
// nella lingua in cui gli parliamo.
type Language string

const (
	English Language = "en"
	Italian Language = "it"
	Spanish Language = "es"
	German  Language = "de"
	French  Language = "fr"
)

// SourceLanguage è l'inglese: la lingua **sorgente** dei contenuti (SPEC
// §8-bis), quella da cui si traduce e non quella in cui si arriva.
//
// Qui non è una preferenza tipografica: è il ripiego di [Release.Presented]. Un
// documento che non esiste nella lingua dell'utente si mostra in inglese, e il
// consenso registra l'inglese — perché è ciò che l'utente ha davvero letto, e
// registrare «it» su un testo inglese sarebbe una prova falsa.
const SourceLanguage = English

// Languages elenca le lingue supportate, con la sorgente per prima.
func Languages() []Language {
	return []Language{English, Italian, Spanish, German, French}
}

// ParseLanguage riconosce una lingua dichiarata da un client.
//
// Accetta anche la forma con la regione (`it-IT`, `en-GB`): è quella che arriva
// da `Accept-Language` e dalle impostazioni di un browser, e rifiutarla
// costringerebbe ogni chiamante a fare la stessa potatura.
func ParseLanguage(s string) (Language, error) {
	base, _, _ := strings.Cut(strings.TrimSpace(strings.ToLower(s)), "-")
	lang := Language(base)
	if slices.Contains(Languages(), lang) {
		return lang, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownLanguage, s)
}

// ------------------------------------------------------------------- errori

var (
	// ErrUnknownDocument indica un documento che non esiste.
	ErrUnknownDocument = errors.New("legal: documento sconosciuto")
	// ErrUnknownLanguage indica una lingua fuori dalle cinque di SPEC §8-bis.
	ErrUnknownLanguage = errors.New("legal: lingua non supportata")
	// ErrVersionNotInForce indica un'accettazione che nomina una versione
	// diversa da quella in vigore. Vedi [Service.Accept].
	ErrVersionNotInForce = errors.New("legal: la versione accettata non è quella in vigore")
)

// ------------------------------------------------------------------ rilasci

// Notice dice se un rilascio tocca materialmente i diritti dell'utente, ed è il
// campo su cui poggia l'unica promessa di calendario che i documenti fanno.
//
// Termini §9: «We may change these terms. When a change materially affects your
// rights we give you 30 days' notice. If you do not accept the change, you may
// close your account before it takes effect.»
//
// Il tipo non ha zero value valido, ed è deliberato: un rilascio dichiarato
// senza questo campo non compila un registro valido ([NewRegistry] lo rifiuta).
// L'alternativa — un `bool` con `false` per difetto — avrebbe reso «non
// materiale» la scelta che si fa **dimenticandosi di scegliere**, cioè avrebbe
// messo il preavviso dei trenta giorni a carico della memoria di chi modifica un
// documento.
type Notice string

const (
	// NoticeFirstPublication è il primo rilascio di un documento: quello prima
	// del quale non c'era niente da accettare.
	//
	// Non è un modo gentile di dire «non materiale». È l'unico caso in cui la
	// domanda del preavviso non si pone: §9 promette trenta giorni a chi è già
	// vincolato da una versione precedente, e qui una versione precedente **che
	// qualcuno possa aver accettato** non esiste. [NewRegistry] lo ammette solo
	// come primo rilascio di un documento, che è ciò che impedisce di usarlo per
	// saltare il preavviso su una modifica vera.
	NoticeFirstPublication Notice = "first_publication"
	// NoticeNotMaterial è un rilascio che non tocca materialmente i diritti:
	// una correzione, una precisazione, un dato aziendale aggiornato. Non fa
	// scattare il preavviso.
	NoticeNotMaterial Notice = "not_material"
	// NoticeMaterial è un rilascio che li tocca. [NewRegistry] pretende che fra
	// l'annuncio e l'entrata in vigore passino almeno [MaterialChangeNotice].
	NoticeMaterial Notice = "material"
)

// MaterialChangeNotice è il preavviso dei Termini §9.
//
// Come [account.DefaultGrace], **non è un numero scelto qui**: è quello che il
// documento promette, e un test lo rilegge dal file invece di fidarsi di questo
// commento.
const MaterialChangeNotice = 30 * 24 * time.Hour

// Text è il testo di un rilascio in una lingua: dove sta e che impronta ha.
type Text struct {
	// File è il nome del file dentro `legal/<lingua>/`. Vuoto significa
	// `<documento>.md`, che è dove sta il rilascio in vigore.
	//
	// Esiste perché un preavviso dei Termini §9 richiede che **due versioni
	// siano leggibili insieme**: quella che vincola oggi e quella che vincolerà
	// fra trenta giorni. Con un file solo per documento, pubblicare la nuova
	// significherebbe cancellare quella che l'utente ha accettato proprio nel
	// mese in cui deve poterla rileggere per decidere se chiudere l'account.
	File string
	// SHA256 è l'impronta esadecimale del file, per intero e frontmatter
	// compreso.
	//
	// È ciò che rende meccanica la regola di `legal/README.md`: «non si modifica
	// un documento in vigore, si crea una versione nuova». Una virgola cambiata
	// senza cambiare versione rende rosso il test che confronta le impronte —
	// che è l'unico momento in cui qualcuno può accorgersene, perché la
	// riscrittura di un testo sotto un consenso già prestato non lascia nessuna
	// altra traccia.
	SHA256 string
}

// Release è una versione di un documento: quando è stata annunciata, da quando
// vincola, e in che lingue esiste.
type Release struct {
	// Version è il numero di versione del frontmatter, in forma semver.
	Version string
	// Effective è il giorno in cui il rilascio entra in vigore, a mezzanotte
	// UTC. È l'`effective_date` del frontmatter.
	//
	// La granularità è il giorno perché è quella che il documento dichiara: un
	// istante più preciso sarebbe una precisione che l'utente non ha letto da
	// nessuna parte.
	Effective time.Time
	// Announced è il giorno in cui il rilascio è stato annunciato. Per un
	// documento pubblicato senza preavviso coincide con [Release.Effective].
	Announced time.Time
	// Notice dice se il cambiamento è materiale (Termini §9).
	Notice Notice
	// Texts sono i testi per lingua. Deve contenere almeno [SourceLanguage]:
	// una versione senza il suo originale inglese non esiste (SPEC §8-bis).
	//
	// Una lingua assente **non è un errore**: è una traduzione che non c'è
	// ancora (#447), e l'utente vedrà l'inglese. Vedi [Release.Presented].
	Texts map[Language]Text
}

// Presented dice in che lingua l'utente vedrà davvero questo rilascio, data la
// lingua che preferisce.
//
// È la funzione che decide cosa finisce nella colonna `language` della prova. La
// regola è una sola e vale la pena scriverla per esteso: **si registra la lingua
// del testo mostrato, non quella dell'interfaccia**. Un utente con la dashboard
// in italiano che ha letto i Termini in inglese — perché la traduzione non c'è
// ancora — ha prestato il suo consenso sull'inglese, e la prova deve dire
// «en». Il contrario sarebbe una prova che afferma qualcosa che non è successo.
func (r Release) Presented(preferred Language) Language {
	if _, ok := r.Texts[preferred]; ok {
		return preferred
	}
	return SourceLanguage
}

// FileName è il nome del file di questo rilascio dentro `legal/<lingua>/`.
func (r Release) FileName(doc Document, lang Language) string {
	if text, ok := r.Texts[lang]; ok && text.File != "" {
		return text.File
	}
	return string(doc) + ".md"
}

// InForce dice se il rilascio vincola a un dato istante.
//
// `!Before` e non `After`: chi arriva esattamente a mezzanotte del giorno di
// entrata in vigore è dentro. È la stessa scelta di `purge_after <= now` della
// 0017, e per la stessa ragione — una soglia dichiarata a una data si raggiunge
// quella data, non il giorno dopo.
func (r Release) InForce(at time.Time) bool {
	return !at.Before(r.Effective)
}

// ------------------------------------------------------------------ registro

// Registry è l'insieme dei documenti con la loro storia di rilasci.
//
// Va costruito con [NewRegistry], che ne verifica le invarianti; quello
// dichiarato dal repository è [Current].
type Registry struct {
	releases map[Document][]Release
}

// DocumentRelease è un rilascio insieme al documento a cui appartiene: la forma
// in cui il registro risponde quando la domanda riguarda più documenti insieme.
type DocumentRelease struct {
	Document Document
	Release  Release
}

// NewRegistry costruisce un registro e ne verifica le invarianti.
//
// I controlli sono qui e non in un test perché un registro incoerente non è un
// difetto di qualità: è una prova del consenso che non prova quello che dice. Il
// servizio non parte con un registro che non si valida, ed è la reazione giusta
// — l'alternativa sarebbe registrare consensi su versioni che non esistono.
func NewRegistry(releases map[Document][]Release) (*Registry, error) {
	if len(releases) == 0 {
		return nil, errors.New("legal: registro vuoto")
	}
	for _, doc := range Documents() {
		if len(releases[doc]) == 0 {
			return nil, fmt.Errorf("legal: %s non ha nessun rilascio", doc)
		}
	}

	copied := make(map[Document][]Release, len(releases))
	for doc, list := range releases {
		if !slices.Contains(Documents(), doc) {
			return nil, fmt.Errorf("legal: %w: %q", ErrUnknownDocument, doc)
		}
		if err := validateReleases(doc, list); err != nil {
			return nil, err
		}
		copied[doc] = slices.Clone(list)
	}
	return &Registry{releases: copied}, nil
}

func validateReleases(doc Document, list []Release) error {
	files := map[string]string{}
	for i, rel := range list {
		if _, err := parseVersion(rel.Version); err != nil {
			return fmt.Errorf("legal: %s: %w", doc, err)
		}
		if rel.Effective.IsZero() || rel.Announced.IsZero() {
			return fmt.Errorf("legal: %s %s: annuncio o entrata in vigore mancanti", doc, rel.Version)
		}
		if rel.Announced.After(rel.Effective) {
			return fmt.Errorf("legal: %s %s: annunciato dopo essere entrato in vigore", doc, rel.Version)
		}

		switch rel.Notice {
		case NoticeFirstPublication:
			if i > 0 {
				return fmt.Errorf(
					"legal: %s %s: dichiarato `first_publication`, ma segue %s. "+
						"Un documento ha una prima pubblicazione sola: è materiale (Termini §9) o no?",
					doc, rel.Version, list[i-1].Version)
			}
		case NoticeNotMaterial:
		case NoticeMaterial:
			// Termini §9. Il controllo è qui, sul rilascio, perché è qui che il
			// preavviso esiste: al momento in cui la modifica prende effetto non
			// c'è più niente da annunciare.
			if preavviso := rel.Effective.Sub(rel.Announced); preavviso < MaterialChangeNotice {
				return fmt.Errorf(
					"legal: %s %s: modifica materiale con %v di preavviso, i Termini §9 ne promettono %v",
					doc, rel.Version, preavviso, MaterialChangeNotice)
			}
		default:
			return fmt.Errorf(
				"legal: %s %s: `Notice` non dichiarato. È materiale (Termini §9: trenta giorni di preavviso) o no?",
				doc, rel.Version)
		}

		if _, ok := rel.Texts[SourceLanguage]; !ok {
			return fmt.Errorf("legal: %s %s: manca il testo in %s, che è la lingua sorgente (SPEC §8-bis)",
				doc, rel.Version, SourceLanguage)
		}
		for lang, text := range rel.Texts {
			if !slices.Contains(Languages(), lang) {
				return fmt.Errorf("legal: %s %s: %w: %q", doc, rel.Version, ErrUnknownLanguage, lang)
			}
			if !sha256Pattern.MatchString(text.SHA256) {
				return fmt.Errorf("legal: %s %s (%s): impronta assente o malformata", doc, rel.Version, lang)
			}
		}

		// Due rilasci dello stesso documento non possono puntare allo stesso
		// file: sarebbero due versioni con un testo solo, e la seconda
		// smentirebbe la prima.
		for _, lang := range Languages() {
			if _, ok := rel.Texts[lang]; !ok {
				continue
			}
			chiave := string(lang) + "/" + rel.FileName(doc, lang)
			if altra, collide := files[chiave]; collide {
				return fmt.Errorf("legal: %s: %s e %s dichiarano lo stesso file %s",
					doc, altra, rel.Version, chiave)
			}
			files[chiave] = rel.Version
		}

		if i == 0 {
			continue
		}
		precedente := list[i-1]
		if compareVersions(precedente.Version, rel.Version) >= 0 {
			return fmt.Errorf("legal: %s: la versione %s non segue %s", doc, rel.Version, precedente.Version)
		}
		if rel.Effective.Before(precedente.Effective) {
			return fmt.Errorf("legal: %s: %s entra in vigore prima di %s", doc, rel.Version, precedente.Version)
		}
	}
	return nil
}

// Releases è la storia dei rilasci di un documento, dal più vecchio.
func (r *Registry) Releases(doc Document) []Release {
	return slices.Clone(r.releases[doc])
}

// InForce restituisce il rilascio che vincola a un dato istante.
//
// È l'ultimo la cui entrata in vigore è arrivata: un rilascio annunciato per il
// mese prossimo esiste nel registro, e **non vincola nessuno** finché quel
// giorno non arriva. È la metà di Termini §9 che il codice deve saper
// rappresentare — l'altra metà, «puoi chiudere l'account prima», è R45.
func (r *Registry) InForce(doc Document, at time.Time) (Release, bool) {
	var found Release
	var ok bool
	for _, rel := range r.releases[doc] {
		if rel.InForce(at) {
			found, ok = rel, true
		}
	}
	return found, ok
}

// InForceAll è [Registry.InForce] per tutti i documenti, nell'ordine di
// [Documents].
func (r *Registry) InForceAll(at time.Time) []DocumentRelease {
	out := make([]DocumentRelease, 0, len(r.releases))
	for _, doc := range Documents() {
		if rel, ok := r.InForce(doc, at); ok {
			out = append(out, DocumentRelease{Document: doc, Release: rel})
		}
	}
	return out
}

// Upcoming elenca i rilasci annunciati che non sono ancora in vigore.
//
// È ciò che rende esigibile la seconda metà dei Termini §9: l'utente può
// chiudere l'account «before it takes effect» solo se sa che c'è un «it» e
// quando prende effetto. Il registro lo sa; comunicarlo è di chi manda le email
// (R19–R21), e questa funzione è il posto da cui prenderà l'elenco.
func (r *Registry) Upcoming(at time.Time) []DocumentRelease {
	var out []DocumentRelease
	for _, doc := range Documents() {
		for _, rel := range r.releases[doc] {
			if !rel.InForce(at) {
				out = append(out, DocumentRelease{Document: doc, Release: rel})
			}
		}
	}
	return out
}

// ------------------------------------------------------------------ versioni

var (
	versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)
	sha256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func parseVersion(v string) ([3]int, error) {
	m := versionPattern.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, fmt.Errorf("versione %q: attesa la forma maggiore.minore.patch", v)
	}
	var out [3]int
	for i := range out {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, fmt.Errorf("versione %q: %w", v, err)
		}
		out[i] = n
	}
	return out, nil
}

// compareVersions ordina due versioni per numero e non per stringa: `1.10.0`
// viene dopo `1.9.0`, che nell'ordine alfabetico sarebbe il contrario.
func compareVersions(a, b string) int {
	va, errA := parseVersion(a)
	vb, errB := parseVersion(b)
	if errA != nil || errB != nil {
		return strings.Compare(a, b)
	}
	return slices.Compare(va[:], vb[:])
}

// Date compone la mezzanotte UTC di un giorno.
//
// I documenti dichiarano un giorno, non un istante: questa funzione è l'unico
// posto in cui quel giorno diventa un istante, così che il registro e il test
// che rilegge i file lo interpretino allo stesso modo.
func Date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
