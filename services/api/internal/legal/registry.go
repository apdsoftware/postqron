package legal

import "sync"

// Questo file è il registro dei documenti così come sono oggi. È **l'unica
// copia in Go** di ciò che sta nel frontmatter di `legal/`, e la copia non è
// libera di divergere: TestIlRegistroDescriveIFileVeri rilegge i file,
// confronta versione, data, lingua e impronta, e diventa rosso al primo
// disallineamento.
//
// # Perché una copia e non i file
//
// Perché il binario dell'API non porta con sé `legal/`. La directory sta nella
// radice del monorepo, fuori dal modulo Go, quindi `//go:embed` non la
// raggiunge; leggerla dal disco a runtime significherebbe che un deploy senza
// quei file registra consensi su versioni inventate — o non ne registra
// affatto. Una costante compilata dentro il binario non ha quel modo di
// fallire: se il registro è sbagliato, è sbagliato in CI, dove qualcuno lo
// vede.
//
// È la stessa forma di internal/account: una costante nel codice, un documento
// nel repository e un test che li tiene insieme leggendo il documento.
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

// Current è il registro dichiarato dal repository.
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
			English: {SHA256: "1692f61d85fc243307da45b65501497a5196d0f27de0ebc6d6a7cfab0da94bc9"},
		},
	}},

	AcceptableUsePolicy: {{
		Version:   "1.0.0",
		Effective: Date(2026, 8, 17),
		Announced: Date(2026, 8, 17),
		Notice:    NoticeFirstPublication,
		Texts: map[Language]Text{
			English: {SHA256: "cbec067fa447e3bf3be0f7d6d0fbce59d872f18318f6355bb6795a5fed90bb6f"},
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
			English: {SHA256: "6d45bdf3971631c2896ee067df988ecd9131536d8db82d8b5c195eb9b90851d0"},
		},
	}},

	CookiePolicy: {{
		Version:   "1.0.0",
		Effective: Date(2026, 8, 17),
		Announced: Date(2026, 8, 17),
		Notice:    NoticeFirstPublication,
		Texts: map[Language]Text{
			English: {SHA256: "6368673eec3dd591ae0fb83673eb1b2f131fd86d5f2f20dc624b6c8c93f690f3"},
		},
	}},
}
