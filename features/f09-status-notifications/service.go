package statusnotifications

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Authorizer interface {
	CanViewStatus(context.Context, string, string) (bool, error)
	CanRetryPublication(context.Context, string, string) (bool, error)
}

type RecipientResolver interface {
	ResolveNotificationRecipient(context.Context, Notification) (Recipient, error)
}

type EmailGateway interface {
	EnqueueEmail(context.Context, EmailCommand) error
}

// RetryGateway is the F8 boundary. Replays must be harmless: F8 keeps the
// original destination idempotency key. An adapter must return nil when a
// replay finds the same failure cycle already requeued.
type RetryGateway interface {
	RetryDestination(context.Context, string, string, string) error
}

type Service struct {
	repository Repository
	authorizer Authorizer
	recipients RecipientResolver
	email      EmailGateway
	publishing RetryGateway
	now        func() time.Time
	random     func([]byte) error
	lease      time.Duration
}

type ServiceOption func(*Service)

func WithClock(clock func() time.Time) ServiceOption {
	return func(service *Service) {
		service.now = clock
	}
}

func WithRandom(random func([]byte) error) ServiceOption {
	return func(service *Service) {
		service.random = random
	}
}

func WithLease(duration time.Duration) ServiceOption {
	return func(service *Service) {
		service.lease = duration
	}
}

func NewService(
	repository Repository,
	authorizer Authorizer,
	recipients RecipientResolver,
	email EmailGateway,
	publishing RetryGateway,
	options ...ServiceOption,
) (*Service, error) {
	if repository == nil || authorizer == nil || recipients == nil ||
		email == nil || publishing == nil {
		return nil, fmt.Errorf(
			"%w: repository and integration boundaries are required",
			ErrInvalidArgument,
		)
	}
	service := &Service{
		repository: repository,
		authorizer: authorizer,
		recipients: recipients,
		email:      email,
		publishing: publishing,
		now:        time.Now,
		random: func(destination []byte) error {
			_, err := rand.Read(destination)
			return err
		},
		lease: 30 * time.Second,
	}
	for _, option := range options {
		option(service)
	}
	if service.now == nil || service.random == nil || service.lease <= 0 {
		return nil, fmt.Errorf("%w: invalid service option", ErrInvalidArgument)
	}
	return service, nil
}

func (service *Service) ConsumeLifecycle(
	ctx context.Context,
	event LifecycleEvent,
) (ApplyResult, error) {
	event.EventID = strings.TrimSpace(event.EventID)
	event.WorkspaceID = strings.TrimSpace(event.WorkspaceID)
	event.PostID = strings.TrimSpace(event.PostID)
	event.DraftID = strings.TrimSpace(event.DraftID)
	event.OccurredAt = event.OccurredAt.UTC()
	for index := range event.Destinations {
		event.Destinations[index].ID = strings.TrimSpace(event.Destinations[index].ID)
		event.Destinations[index].ChannelID = strings.TrimSpace(
			event.Destinations[index].ChannelID,
		)
	}
	return service.repository.ApplyLifecycle(ctx, event)
}

func (service *Service) ConsumePublication(
	ctx context.Context,
	event PublicationEvent,
) (ApplyResult, error) {
	event.WorkspaceID = strings.TrimSpace(event.WorkspaceID)
	event.JobID = strings.TrimSpace(event.JobID)
	event.PostID = strings.TrimSpace(event.PostID)
	event.DestinationID = strings.TrimSpace(event.DestinationID)
	event.ChannelID = strings.TrimSpace(event.ChannelID)
	event.Status = strings.TrimSpace(event.Status)
	event.RemoteID = strings.TrimSpace(event.RemoteID)
	event.OccurredAt = event.OccurredAt.UTC()
	if strings.TrimSpace(event.EventID) == "" {
		event.EventID = derivePublicationEventID(event)
	}
	result, err := service.repository.ApplyPublication(ctx, event)
	if err != nil {
		return ApplyResult{}, err
	}
	destination := destinationByID(result.View, event.DestinationID)
	if event.Status == "dead_letter" &&
		destination.Status == DestinationFailed &&
		destination.LastEventID == event.EventID {
		_, enqueueErr := service.enqueueNotification(ctx, NotificationEvent{
			EventID:       event.EventID,
			Kind:          NotificationPublicationFailed,
			WorkspaceID:   event.WorkspaceID,
			PostID:        event.PostID,
			DestinationID: event.DestinationID,
			Subject:       "Pubblicazione non riuscita",
			Detail:        destination.Diagnostic.Message,
			OccurredAt:    event.OccurredAt,
		})
		if enqueueErr != nil {
			return ApplyResult{}, fmt.Errorf(
				"enqueue publication failure notification: %w",
				enqueueErr,
			)
		}
	}
	return result, nil
}

func (service *Service) ConsumeNotification(
	ctx context.Context,
	event NotificationEvent,
) (EnqueueResult, error) {
	if event.Kind == NotificationPublicationFailed {
		return EnqueueResult{}, fmt.Errorf(
			"%w: publication failures come from F8 status events",
			ErrInvalidArgument,
		)
	}
	return service.enqueueNotification(ctx, event)
}

func (service *Service) enqueueNotification(
	ctx context.Context,
	event NotificationEvent,
) (EnqueueResult, error) {
	event.EventID = strings.TrimSpace(event.EventID)
	event.AccountID = strings.TrimSpace(event.AccountID)
	event.WorkspaceID = strings.TrimSpace(event.WorkspaceID)
	event.PostID = strings.TrimSpace(event.PostID)
	event.DestinationID = strings.TrimSpace(event.DestinationID)
	event.Subject = Redact(event.Subject)
	event.Detail = Redact(event.Detail)
	event.ActionLabel = strings.TrimSpace(event.ActionLabel)
	event.ActionURL = strings.TrimSpace(event.ActionURL)
	event.OccurredAt = event.OccurredAt.UTC()
	if event.EventID == "" || !event.Kind.Valid() || event.OccurredAt.IsZero() {
		return EnqueueResult{}, fmt.Errorf(
			"%w: invalid notification event",
			ErrInvalidArgument,
		)
	}
	now := service.now().UTC()
	notification := Notification{
		ID:             stableID("notification", string(event.Kind), event.EventID),
		SourceEventID:  event.EventID,
		Kind:           event.Kind,
		AccountID:      event.AccountID,
		WorkspaceID:    event.WorkspaceID,
		PostID:         event.PostID,
		DestinationID:  event.DestinationID,
		Subject:        event.Subject,
		Detail:         event.Detail,
		ActionLabel:    event.ActionLabel,
		ActionURL:      event.ActionURL,
		IdempotencyKey: notificationIdempotencyKey(event.Kind, event.EventID),
		State:          QueuePending,
		NextAttemptAt:  now,
		CreatedAt:      now,
	}
	return service.repository.EnqueueNotification(ctx, notification)
}

func (service *Service) GetStatus(
	ctx context.Context,
	workspaceID, actorID, postID string,
) (PostView, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	actorID = strings.TrimSpace(actorID)
	postID = strings.TrimSpace(postID)
	if workspaceID == "" || actorID == "" || postID == "" {
		return PostView{}, fmt.Errorf("%w: status identifiers", ErrInvalidArgument)
	}
	allowed, err := service.authorizer.CanViewStatus(ctx, workspaceID, actorID)
	if err != nil {
		return PostView{}, fmt.Errorf("authorize status view: %w", err)
	}
	if !allowed {
		return PostView{}, ErrForbidden
	}
	return service.repository.GetPost(ctx, workspaceID, postID)
}

func (service *Service) RequestManualRetry(
	ctx context.Context,
	request ManualRetryRequest,
) (EnqueueResult, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.PostID = strings.TrimSpace(request.PostID)
	request.DestinationID = strings.TrimSpace(request.DestinationID)
	request.ActorID = strings.TrimSpace(request.ActorID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.WorkspaceID == "" || request.PostID == "" ||
		request.DestinationID == "" || request.ActorID == "" ||
		request.IdempotencyKey == "" || len(request.IdempotencyKey) > 255 {
		return EnqueueResult{}, fmt.Errorf("%w: manual retry request", ErrInvalidArgument)
	}
	allowed, err := service.authorizer.CanRetryPublication(
		ctx,
		request.WorkspaceID,
		request.ActorID,
	)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("authorize manual retry: %w", err)
	}
	if !allowed {
		return EnqueueResult{}, ErrForbidden
	}
	view, err := service.repository.GetPost(
		ctx,
		request.WorkspaceID,
		request.PostID,
	)
	if err != nil {
		return EnqueueResult{}, err
	}
	destination := destinationByID(view, request.DestinationID)
	if destination.ID == "" {
		return EnqueueResult{}, ErrNotFound
	}
	if destination.Status != DestinationFailed ||
		destination.LastEventID == "" {
		return EnqueueResult{}, ErrConflict
	}
	now := service.now().UTC()
	return service.repository.EnqueueManualRetry(ctx, ManualRetry{
		ID: stableID(
			"manual_retry",
			request.WorkspaceID,
			request.IdempotencyKey,
		),
		WorkspaceID:    request.WorkspaceID,
		PostID:         request.PostID,
		DestinationID:  request.DestinationID,
		FailureEventID: destination.LastEventID,
		ActorID:        request.ActorID,
		IdempotencyKey: request.IdempotencyKey,
		State:          QueuePending,
		NextAttemptAt:  now,
		CreatedAt:      now,
	})
}

func (service *Service) DispatchNotification(
	ctx context.Context,
) (bool, error) {
	now := service.now().UTC()
	leaseToken, err := service.leaseToken()
	if err != nil {
		return false, err
	}
	notification, found, err := service.repository.ClaimNotification(
		ctx,
		now,
		now.Add(service.lease),
		leaseToken,
	)
	if err != nil || !found {
		return found, err
	}
	recipient, err := service.recipients.ResolveNotificationRecipient(
		ctx,
		notification,
	)
	if err == nil {
		err = service.email.EnqueueEmail(
			ctx,
			emailCommand(notification, recipient, now),
		)
	}
	if err != nil {
		next := now.Add(retryDelay(notification.AttemptCount))
		if markErr := service.repository.MarkNotificationRetry(
			ctx,
			notification.ID,
			notification.LeaseToken,
			next,
		); markErr != nil {
			return true, errors.Join(err, markErr)
		}
		return true, fmt.Errorf("dispatch notification: %w", err)
	}
	if err := service.repository.MarkNotificationDelivered(
		ctx,
		notification.ID,
		notification.LeaseToken,
		now,
	); err != nil {
		return true, err
	}
	return true, nil
}

func (service *Service) DispatchManualRetry(
	ctx context.Context,
) (bool, error) {
	now := service.now().UTC()
	leaseToken, err := service.leaseToken()
	if err != nil {
		return false, err
	}
	retry, found, err := service.repository.ClaimManualRetry(
		ctx,
		now,
		now.Add(service.lease),
		leaseToken,
	)
	if err != nil || !found {
		return found, err
	}
	if err := service.publishing.RetryDestination(
		ctx,
		retry.WorkspaceID,
		retry.ActorID,
		retry.DestinationID,
	); err != nil {
		next := now.Add(retryDelay(retry.AttemptCount))
		if markErr := service.repository.MarkManualRetryRetry(
			ctx,
			retry.ID,
			retry.LeaseToken,
			next,
		); markErr != nil {
			return true, errors.Join(err, markErr)
		}
		return true, fmt.Errorf("dispatch manual retry: %w", err)
	}
	if err := service.repository.MarkManualRetryDelivered(
		ctx,
		retry.ID,
		retry.LeaseToken,
		now,
	); err != nil {
		return true, err
	}
	return true, nil
}

func (service *Service) leaseToken() (string, error) {
	var value [18]byte
	if err := service.random(value[:]); err != nil {
		return "", fmt.Errorf("create work-item lease: %w", err)
	}
	return "lease_" + base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Minute * time.Duration(1<<(attempt-1))
}

func destinationByID(view PostView, id string) DestinationView {
	for _, destination := range view.Destinations {
		if destination.ID == id {
			return destination
		}
	}
	return DestinationView{}
}

func notificationIdempotencyKey(kind NotificationKind, eventID string) string {
	return "f09:" + string(kind) + ":" + hashJSON(eventID)
}

func emailCommand(
	notification Notification,
	recipient Recipient,
	now time.Time,
) EmailCommand {
	heading := notification.Subject
	intro := notification.Detail
	body := ""
	switch notification.Kind {
	case NotificationWelcome:
		if heading == "" {
			heading = "Benvenuto in Postqron"
		}
		if intro == "" {
			intro = "Il tuo account è pronto. Puoi iniziare a pianificare i contenuti."
		}
	case NotificationPlanChanged:
		if heading == "" {
			heading = "Il tuo piano è stato aggiornato"
		}
		if intro == "" {
			intro = "Le funzionalità e i limiti del workspace sono cambiati."
		}
	case NotificationPublicationFailed:
		if heading == "" {
			heading = "Pubblicazione non riuscita"
		}
		if intro == "" {
			intro = "Una destinazione non ha completato la pubblicazione."
		}
		body = "Apri Postqron per controllare la destinazione e riprovare."
	case NotificationSecurityAlert:
		if heading == "" {
			heading = "Attività di sicurezza sul tuo account"
		}
		if intro == "" {
			intro = "È stata completata un'azione rilevante per la sicurezza."
		}
	}
	return EmailCommand{
		IdempotencyKey:  notification.IdempotencyKey,
		Channel:         "transactional",
		TemplateID:      string(notification.Kind),
		TemplateVersion: "1.0.0",
		Recipient:       recipient,
		Data: EmailData{
			Heading:     heading,
			Intro:       intro,
			Body:        body,
			Detail:      notification.Detail,
			ActionLabel: notification.ActionLabel,
			ActionURL:   notification.ActionURL,
		},
		OccurredAt: now,
	}
}
