package legal

import (
	"slices"
	"time"
)

// Source dice in quale momento un consenso è nato.
//
// Non è decorazione: i due momenti hanno una forma giuridica diversa. Alla
// registrazione l'accettazione è implicita nell'atto — i Termini si aprono con
// «By creating an account you accept them» — mentre una riaccettazione è un atto
// separato, compiuto da chi ha già un account davanti a un testo cambiato. Se
// domani si discute *come* il consenso è stato prestato, questa colonna è la
// differenza fra «ha creato l'account» e «gli abbiamo mostrato la versione nuova
// e ha premuto accetto».
type Source string

const (
	// SourceRegistration è il consenso nato con l'account.
	SourceRegistration Source = "registration"
	// SourceReacceptance è il consenso prestato su un documento cambiato dopo
	// la registrazione.
	SourceReacceptance Source = "reacceptance"
)

// Sources elenca i modi in cui un consenso può nascere.
func Sources() []Source { return []Source{SourceRegistration, SourceReacceptance} }

// Consent è la prova che un utente ha accettato una versione di un documento.
//
// È **immutabile per costruzione**, e non solo per disciplina: la 0018 vieta
// l'UPDATE con un trigger. Una prova che si può riscrivere non prova niente, e
// la sola forma di correzione che ha senso su un consenso è registrarne un altro
// — che è quello che succede quando il documento cambia.
type Consent struct {
	Document Document
	// Version è la versione accettata, non «l'ultima»: due documenti a versioni
	// diverse accettati nello stesso istante restano distinti.
	Version string
	// Language è la lingua del **testo mostrato**, che può non essere quella
	// dell'interfaccia: vedi [Release.Presented].
	Language Language
	// Checksum è l'impronta del testo accettato. Rende la prova verificabile
	// senza doversi fidare del repository.
	Checksum string
	// Source dice se il consenso è nato con l'account o dopo.
	Source Source
	// AcceptedAt è l'istante dell'accettazione: l'unico dei quattro campi che
	// non viene dal documento, e l'unico che dice quando l'utente si è
	// vincolato.
	AcceptedAt time.Time
}

// ConsentsFor compone i consensi di chi accetta **tutti** i documenti in vigore
// a un dato istante, nella lingua che preferisce.
//
// È ciò che serve alla registrazione: i Termini dicono che creare un account
// significa accettare i Termini, la Acceptable Use Policy e la Privacy Policy, e
// il banner dei cookie fa la sua parte sulla quarta. Il registro non prova a
// distinguerli qui: chi si registra ha davanti tutti e quattro, e la prova
// registra tutti e quattro con la versione che aveva ciascuno in quel momento.
func (r *Registry) ConsentsFor(at time.Time, preferred Language, source Source) []Consent {
	inForce := r.InForceAll(at)
	out := make([]Consent, 0, len(inForce))
	for _, dr := range inForce {
		out = append(out, dr.Release.consent(dr.Document, preferred, source, at))
	}
	return out
}

// consent compone la prova di un singolo rilascio.
func (rel Release) consent(doc Document, preferred Language, source Source, at time.Time) Consent {
	lang := rel.Presented(preferred)
	return Consent{
		Document:   doc,
		Version:    rel.Version,
		Language:   lang,
		Checksum:   rel.Texts[lang].SHA256,
		Source:     source,
		AcceptedAt: at,
	}
}

// SortConsents mette i consensi nell'ordine di [Documents], e a parità di
// documento dal più vecchio: è l'ordine in cui si legge una storia.
func SortConsents(consents []Consent) {
	ordine := Documents()
	slices.SortStableFunc(consents, func(a, b Consent) int {
		if d := slices.Index(ordine, a.Document) - slices.Index(ordine, b.Document); d != 0 {
			return d
		}
		return compareVersions(a.Version, b.Version)
	})
}
