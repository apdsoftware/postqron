package scheduler

import (
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/schedule"
)

// scanLimit è il tetto di occorrenze che il recupero attraversa una per una
// prima di saltare direttamente dentro la finestra.
//
// Serve a un solo caso, ma è quello che decide se il motore riparte: un job a un
// secondo fermo per una settimana ha 604.800 occorrenze da scavalcare, e
// contarle una per una a ogni passata terrebbe occupato lo scheduler mentre
// tutti gli altri job aspettano. Oltre il tetto si salta all'occorrenza giusta
// con una sola chiamata e il conteggio si dichiara troncato ([Dropped.Truncated]):
// il numero esatto di occorrenze mai avvenute è meno importante del fatto che il
// motore riparta subito.
const scanLimit = 100_000

// plan è ciò che lo scheduler decide per un singolo job in una singola passata.
type plan struct {
	// due sono le occorrenze da accodare, in ordine crescente. Tutte
	// antecedenti o pari a `now`: un'occorrenza futura non si accoda in
	// anticipo, o il ritardo di R47 non vorrebbe più dire niente.
	due []time.Time

	// next è il nuovo `jobs.next_run_at`, valido solo se hasNext è vero. Quando
	// hasNext è falso il job non ha più occorrenze — succede alle espressioni
	// cron su date impossibili, mai agli intervalli — e la colonna va messa a
	// NULL, che è ciò che lo toglie dall'indice del dispatch.
	next    time.Time
	hasNext bool

	// capped è vero quando il tetto per passata ha lasciato indietro del lavoro
	// già dovuto. Il job resta dovuto e la passata successiva riprende da dove
	// questa si è fermata: è così che un arretrato si smaltisce a ondate invece
	// che in un colpo solo.
	capped bool

	// Le occorrenze scartate perché fuori dalla finestra di recupero. Vedi [Dropped].
	droppedCount     int
	droppedFrom      time.Time
	droppedTo        time.Time
	droppedTruncated bool
}

// planOccurrences calcola, per un job la cui prossima occorrenza è `cursor`,
// che cosa è dovuto a `now`.
//
// È la parte del motore che non tocca il database, ed è deliberato: il
// comportamento sul cambio d'ora, sul riavvio dopo un fermo lungo e sul tetto
// per passata si prova qui, in memoria, senza PostgreSQL in mezzo.
//
// Le tre decisioni che prende, nell'ordine:
//
//  1. **Fin dove si recupera.** Le occorrenze antecedenti a `now - CatchUp` non
//     si eseguono. È la scelta scomoda ma inevitabile: dopo un fermo, o si
//     dichiara una finestra o si accetta che un job a un secondo scarichi tutto
//     l'arretrato sul bersaglio nel momento peggiore, cioè appena è tornato su.
//     Ciò che si scarta viene contato e segnalato, non perso in silenzio.
//  2. **Quante se ne prendono per volta.** Dentro la finestra si accodano al
//     massimo MaxPerJob occorrenze per passata. Nulla va perso: `next_run_at`
//     avanza solo fino a dove si è arrivati.
//  3. **Dove riprendere.** Il nuovo `next_run_at` è sempre la prima occorrenza
//     non ancora accodata. È l'unico stato che sopravvive al riavvio, ed è
//     quello che rende il riavvio indistinguibile da una pausa.
func planOccurrences(s schedule.Schedule, cursor, now time.Time, p Policy) plan {
	out := plan{hasNext: true}
	floor := now.Add(-p.CatchUp)

	// ---------------------------------------------------- fuori dalla finestra
	if cursor.Before(floor) {
		out.droppedFrom = cursor
		for cursor.Before(floor) {
			out.droppedCount++
			out.droppedTo = cursor

			if out.droppedCount >= scanLimit {
				out.droppedTruncated = true
				// La prima occorrenza **non** antecedente al bordo. Il
				// nanosecondo tolto serve a non perdere l'occorrenza che cade
				// esattamente sul bordo: Next è strettamente successiva.
				next, ok := s.Next(floor.Add(-time.Nanosecond))
				if !ok {
					out.hasNext = false
					return out
				}
				cursor = next
				break
			}

			next, ok := s.Next(cursor)
			if !ok {
				// Il job non ha più occorrenze: quelle scavalcate restano
				// scartate e non c'è un `next_run_at` da scrivere.
				out.hasNext = false
				return out
			}
			cursor = next
		}
	}

	// ------------------------------------------------- dentro la finestra
	for len(out.due) < p.MaxPerJob && !cursor.After(now) {
		out.due = append(out.due, cursor)

		next, ok := s.Next(cursor)
		if !ok {
			out.hasNext = false
			return out
		}
		cursor = next
	}

	out.next = cursor
	out.capped = len(out.due) == p.MaxPerJob && !cursor.After(now)
	return out
}

// firstOccurrence è la prima occorrenza di un job che non ne ha ancora una.
//
// È strettamente successiva a `now`: un job appena creato non deve partire
// nell'istante in cui viene salvato, e soprattutto non deve recuperare le
// occorrenze del passato che la sua espressione descriverebbe.
func firstOccurrence(s schedule.Schedule, now time.Time) (time.Time, bool) {
	return s.Next(now)
}
