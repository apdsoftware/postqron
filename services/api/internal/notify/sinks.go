package notify

import (
	"context"
	"errors"

	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
	"github.com/apdsoftware/postqron/services/api/internal/billing"
	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
)

// Questo file è dove i package di dominio si attaccano alla coda.
//
// La direzione delle dipendenze è deliberata e vale la pena dirla per esteso:
// **nessun package di dominio importa questo**. Ciascuno dichiara l'interfaccia
// che gli serve, con i propri tipi — [billing.Notifier], [dispatch.Alerter],
// [auth.Mailer] — e qui ci sono gli adattatori che la soddisfano. È la stessa
// forma di [paddle.EntitlementSink], che internal/billing implementa senza che
// internal/paddle sappia della fatturazione.
//
// Il vantaggio non è estetico. Significa che il motore, la fatturazione e
// l'autenticazione restano provabili senza template, senza client HTTP e senza
// una coda, e che il giorno in cui gli avvisi andranno anche su Slack (R29) il
// posto in cui aggiungerli è uno solo.
//
// Gli adattatori sono tipi distinti e non metodi in più su [Service] per una
// ragione pratica: due domini hanno lo stesso verbo con parametri diversi —
// «piano cambiato» esiste sia come [billing.PlanChangeNotice] sia come
// [PlanChange] — e in Go due metodi con lo stesso nome non convivono.

// PlanSink adatta il servizio a [billing.Notifier].
type PlanSink struct{ service *Service }

// NewPlanSink costruisce l'adattatore per la fatturazione.
func NewPlanSink(service *Service) (*PlanSink, error) {
	if service == nil {
		return nil, errors.New("notify: NewPlanSink richiede un Service")
	}
	return &PlanSink{service: service}, nil
}

var _ billing.Notifier = (*PlanSink)(nil)

// PlanChanged implementa [billing.Notifier].
func (s *PlanSink) PlanChanged(ctx context.Context, notice billing.PlanChangeNotice) error {
	return s.service.PlanChanged(ctx, PlanChange{
		UserID:                notice.UserID,
		PreviousPlan:          notice.PreviousPlan,
		NewPlan:               notice.NewPlan,
		EffectiveAt:           notice.EffectiveAt,
		SuspendedByJobLimit:   notice.SuspendedByJobLimit,
		SuspendedByResolution: notice.SuspendedByResolution,
	})
}

// KeySink adatta il servizio a [apikeys.SecurityNotifier].
type KeySink struct{ service *Service }

// NewKeySink costruisce l'adattatore per le chiavi API.
func NewKeySink(service *Service) (*KeySink, error) {
	if service == nil {
		return nil, errors.New("notify: NewKeySink richiede un Service")
	}
	return &KeySink{service: service}, nil
}

var _ apikeys.SecurityNotifier = (*KeySink)(nil)

// SecurityEvent implementa [apikeys.SecurityNotifier].
func (s *KeySink) SecurityEvent(ctx context.Context, notice apikeys.SecurityNotice) error {
	return s.service.Security(ctx, SecurityEvent{
		UserID:       notice.UserID,
		Kind:         SecurityKind(notice.Kind),
		OccurredAt:   notice.OccurredAt,
		ResourceName: notice.ResourceName,
	})
}

// FailureSink adatta il servizio a [dispatch.Alerter].
type FailureSink struct{ service *Service }

// NewFailureSink costruisce l'adattatore per il motore.
func NewFailureSink(service *Service) (*FailureSink, error) {
	if service == nil {
		return nil, errors.New("notify: NewFailureSink richiede un Service")
	}
	return &FailureSink{service: service}, nil
}

var _ dispatch.Alerter = (*FailureSink)(nil)

// JobFailed implementa [dispatch.Alerter].
//
// La classificazione del motore è più grossolana di quella che [FailureKind]
// ammette — il motore distingue timeout, status HTTP e «non classificato» — e la
// traduzione è per nome, senza tabelle: i valori sono gli stessi da entrambe le
// parti, e un test lo verifica.
func (s *FailureSink) JobFailed(ctx context.Context, failure dispatch.Failure) error {
	return s.service.JobFailed(ctx, JobFailure{
		UserID:       failure.UserID,
		JobID:        failure.JobID,
		JobName:      failure.JobName,
		Environment:  failure.Environment,
		OccurredAt:   failure.OccurredAt,
		ScheduledFor: failure.ScheduledFor,
		Attempt:      failure.Attempt,
		Kind:         FailureKind(failure.Kind),
		HTTPStatus:   failure.HTTPStatus,
	})
}
