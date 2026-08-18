package legal

import "sync"

// Questo file è **metà** del registro: quella che una persona decide e che non
// deve poter cambiare in silenzio. L'altra metà — quali traduzioni esistono e
// quali sono state riviste — sta nei file e la legge [Load].
//
// # Che cosa sta qui e perché
//
// Quali versioni esistono, da quando vincolano, se un cambiamento è materiale
// (Termini §9) e che impronta ha il testo inglese. Sono i fatti su cui poggia
// una prova di consenso: se cambiano, deve costare una riga di diff che qualcuno
// legge in review. L'impronta in particolare è ciò che rende meccanica la regola
// di `legal/README.md` — «non si modifica un documento in vigore» — perché
// [Load] la ricalcola dal file e fallisce se non combacia.
//
// # Che cosa non sta qui, ed è deliberato
//
// **Lo stato delle traduzioni.** `legal/README.md` stabilisce che approvarne una
// è una riga sola nel suo front matter, `pending-review` → `approved`. Se lo
// stato fosse anche qui, quella riga sarebbe due modifiche in due posti, e il
// giorno in cui qualcuno ne facesse una sola il sito mostrerebbe un testo e la
// prova ne dichiarerebbe un altro. Lo legge [Load] da dove è scritto.
//
// **L'elenco delle traduzioni.** Una lingua nuova di un documento già dichiarato
// entra da sé: è una traduzione, non una decisione. Un documento nuovo o una
// versione nuova no — quelli [Load] li rifiuta finché non compaiono qui.
//
// # Perché la storia comincia qui
//
// I Termini sono alla 1.2.0 e la privacy policy alla 1.1.0: le versioni
// precedenti sono esistite, e **non sono in questo registro**. Non è una
// dimenticanza. Un rilascio è nel registro perché qualcuno può averlo accettato
// o potrà accettarlo, e nessuno ha mai accettato la 1.0.0 dei Termini: il
// servizio non era pubblico e la tabella dei consensi non esisteva. Quelle
// versioni sono storia del repository — git le conserva — non prove del
// consenso.
//
// La conseguenza pratica è che i quattro rilasci qui sotto sono tutti
// [NoticeFirstPublication]: sono i primi che qualcuno possa accettare.

// Current è il registro dichiarato, **senza le traduzioni**: conosce i quattro
// documenti e il solo testo inglese.
//
// È ciò che il servizio usa quando `legal/` non è stato caricato, e la
// degradazione è quella giusta: senza i file non si sa quali traduzioni sono
// approvate, e l'unica cosa che si può affermare con certezza è che l'utente ha
// letto l'inglese — che è vero per costruzione, perché l'inglese è ciò che il
// sito mostra quando non ha di meglio. Chi vuole il registro completo chiama
// [Load].
//
// Si costruisce una volta sola e non può fallire in esercizio: se le invarianti
// di [NewRegistry] non reggessero, il pacchetto non compilerebbe un registro
// valido e i test lo direbbero prima di qualunque deploy — per questo qui
// l'errore diventa un panic invece di risalire a ogni chiamante. È lo stesso
// ragionamento di [regexp.MustCompile].
func Current() *Registry { return currentOnce() }

var currentOnce = sync.OnceValue(func() *Registry {
	registry, err := NewRegistry(declared)
	if err != nil {
		panic("legal: il registro dichiarato non è valido: " + err.Error())
	}
	return registry
})

// declared sono i quattro documenti di `legal/`, alle versioni approvate il
// 2026-08-18.
//
// Le versioni **non sono allineate**, ed è la ragione per cui la prova del
// consenso registra un documento per riga invece di una versione sola per
// l'insieme: un utente che accetta oggi accetta i Termini 1.2.0, la privacy
// policy 1.1.0 e le altre due 1.0.0, e un solo numero non potrebbe dire di
// quale documento è.
var declared = map[Document][]Release{
	TermsOfService: {{
		Version: "1.2.0",
		// 1.1.0: il downgrade sospende tutto e riattiva l'utente (R58).
		// 1.2.0: chiudere l'account non annulla l'abbonamento Paddle (#460).
		Effective: Date(2026, 8, 18),
		Announced: Date(2026, 8, 18),
		Notice:    NoticeFirstPublication,
		Texts: map[Language]Text{
			English: {SHA256: "aa45944991473f993e914236a56bd33503b48e5b2eb33ba872ba869bf0b5c9af"},
		},
	}},

	AcceptableUsePolicy: {{
		Version:   "1.0.0",
		Effective: Date(2026, 8, 17),
		Announced: Date(2026, 8, 17),
		Notice:    NoticeFirstPublication,
		Texts: map[Language]Text{
			English: {SHA256: "babe8d89cc5607a77ffc03bf491138a884b783285667da7e8cb0f875086c6f95"},
		},
	}},

	PrivacyPolicy: {{
		Version: "1.1.0",
		// 1.1.0: la traccia delle azioni di un admin sopravvive alla
		// cancellazione, senza più riferimenti all'utente (#460).
		Effective: Date(2026, 8, 18),
		Announced: Date(2026, 8, 18),
		Notice:    NoticeFirstPublication,
		Texts: map[Language]Text{
			English: {SHA256: "36dcd7ac2eb6d3de6eba06d6da71f2d828bf2864119264057392d00a2dcb4c1c"},
		},
	}},

	CookiePolicy: {{
		Version:   "1.0.0",
		Effective: Date(2026, 8, 17),
		Announced: Date(2026, 8, 17),
		Notice:    NoticeFirstPublication,
		Texts: map[Language]Text{
			English: {SHA256: "26b697c404e0dbe53deb96c007f2ab3daae3fc6e77d280e1b69013eb731c35be"},
		},
	}},
}
