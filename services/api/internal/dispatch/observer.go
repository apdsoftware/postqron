package dispatch

import (
	"context"
)

// Observer riceve i fallimenti che il pool dichiara **definitivi**, per
// contarli (R7).
//
// # Perché non è [Alerter]
//
// Ricevono lo stesso fatto e ne fanno due cose che non si possono unire.
//
// Chi **avvisa** deve decidere se quel guasto merita l'attenzione di una
// persona, e ha una politica per farlo: raggruppa, limita, aspetta cinque minuti
// per poter dire «è fallito ventitré volte» invece di mandare ventitré email
// (vedi internal/notify). Chi **conta** deve vedere tutti i fallimenti, compresi
// i ventidue che nessuna email racconterà mai: una metrica che si fermasse agli
// avvisi misurerebbe la politica anti-spam invece dei guasti, e sarebbe piatta
// esattamente nel momento in cui il servizio sta andando peggio.
//
// La [Failure] che ricevono è la stessa struttura e la stessa istanza: è lo
// stesso guasto, e due descrizioni separate divergerebbero al primo campo
// aggiunto a una delle due.
//
// # Perché solo i fallimenti definitivi
//
// Un'occorrenza può fallire e ritentare (R5): il primo tentativo andato male non
// è un fatto del job, è un fatto della rete di cinque secondi fa. Contarlo qui
// significherebbe contare i guasti che il motore stava già rimediando, ed è per
// quello che c'è [Stats.Failed], che conta i **tentativi**. Questa conta le
// occorrenze che nessuno eseguirà più.
//
// Restano fuori, deliberatamente, i tentativi interrotti dall'arresto del
// processo ([Stats.RetryAbandoned]): quello è un fatto nostro, non del job del
// cliente, e confondere i due è precisamente l'errore che R7 chiede di non fare.
//
// # Il metodo deve tornare subito
//
// È chiamato dal worker che ha appena chiuso la riga, e quel worker è uno dei
// [DefaultWorkers] che il servizio ha in tutto. Un osservatore che scrive sul
// database dentro questa chiamata occupa un worker per il tempo di quella
// scrittura: chi ha bisogno di andare sul database accoda e torna — che è
// esattamente ciò che fa [Alerter].
type Observer interface {
	Failed(ctx context.Context, f Failure)
}

// nopObserver è l'osservatore di default: non guarda niente.
type nopObserver struct{}

func (nopObserver) Failed(context.Context, Failure) {}
