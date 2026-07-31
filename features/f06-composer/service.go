package composer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

type ContentAuthorizer interface {
	CanManageContent(context.Context, string, string) (bool, error)
}

type Repository interface {
	Create(context.Context, Draft) (Draft, error)
	Get(context.Context, string, string) (Draft, error)
	List(context.Context, string) ([]Draft, error)
	Update(context.Context, Draft, int64, string) (Draft, error)
	Delete(context.Context, string, string, int64) error
	ListRevisions(context.Context, string, string) ([]DraftRevision, error)
}

type MediaResolver interface {
	Canonicalize(context.Context, string, string, []Media) ([]Media, error)
}

type Service struct {
	repository Repository
	authorizer ContentAuthorizer
	catalog    CapabilityCatalog
	media      MediaResolver
	destinations DestinationResolver
	now        func() time.Time
	random     func([]byte) error
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

func NewService(
	repository Repository,
	authorizer ContentAuthorizer,
	options ...ServiceOption,
) (*Service, error) {
	if repository == nil || authorizer == nil {
		return nil, fmt.Errorf("%w: repository and authorizer are required", ErrInvalidArgument)
	}
	service := &Service{
		repository: repository,
		authorizer: authorizer,
		catalog:    BlockedCapabilityCatalog(),
		now:        time.Now,
		random: func(destination []byte) error {
			_, err := rand.Read(destination)
			return err
		},
	}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

func WithCapabilityCatalog(catalog CapabilityCatalog) ServiceOption {
	return func(service *Service) {
		service.catalog = cloneCatalog(catalog)
	}
}

func WithMediaResolver(resolver MediaResolver) ServiceOption {
	return func(service *Service) {
		service.media = resolver
		repository, repositoryOK := service.repository.(*PostgresRepository)
		media, mediaOK := resolver.(*PostgresMediaStore)
		if repositoryOK && mediaOK {
			repository.BindMediaStore(media)
		}
	}
}

func WithDestinationResolver(resolver DestinationResolver) ServiceOption {
	return func(service *Service) {
		service.destinations = resolver
	}
}

func (service *Service) CreateDraft(
	ctx context.Context,
	command CreateDraftCommand,
) (DraftView, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return DraftView{}, err
	}
	content, err := normalizeContent(command.Content)
	if err != nil {
		return DraftView{}, err
	}
	content, err = service.canonicalizeMedia(
		ctx,
		command.WorkspaceID,
		command.ActorID,
		content,
	)
	if err != nil {
		return DraftView{}, err
	}
	content, err = service.canonicalizeDestinations(ctx, command.WorkspaceID, content)
	if err != nil {
		return DraftView{}, err
	}
	id, err := service.randomID()
	if err != nil {
		return DraftView{}, err
	}
	now := service.now().UTC()
	draft, err := service.repository.Create(ctx, Draft{
		ID:          id,
		WorkspaceID: command.WorkspaceID,
		CreatedBy:   command.ActorID,
		Content:     content,
		Revision:    1,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		return DraftView{}, err
	}
	return service.viewOf(draft), nil
}

func (service *Service) GetDraft(
	ctx context.Context,
	workspaceID, actorID, draftID string,
) (DraftView, error) {
	if err := service.authorize(ctx, workspaceID, actorID); err != nil {
		return DraftView{}, err
	}
	if strings.TrimSpace(draftID) == "" {
		return DraftView{}, fmt.Errorf("%w: draft id is required", ErrInvalidArgument)
	}
	draft, err := service.repository.Get(ctx, workspaceID, draftID)
	if err != nil {
		return DraftView{}, err
	}
	return service.viewOf(draft), nil
}

func (service *Service) ListDrafts(
	ctx context.Context,
	workspaceID, actorID string,
) ([]DraftView, error) {
	if err := service.authorize(ctx, workspaceID, actorID); err != nil {
		return nil, err
	}
	drafts, err := service.repository.List(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	views := make([]DraftView, len(drafts))
	for index := range drafts {
		views[index] = service.viewOf(drafts[index])
	}
	return views, nil
}

func (service *Service) UpdateDraft(
	ctx context.Context,
	command UpdateDraftCommand,
) (DraftView, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return DraftView{}, err
	}
	if strings.TrimSpace(command.DraftID) == "" || command.ExpectedRevision < 1 {
		return DraftView{}, fmt.Errorf("%w: draft id and positive revision are required", ErrInvalidArgument)
	}
	content, err := normalizeContent(command.Content)
	if err != nil {
		return DraftView{}, err
	}
	content, err = service.canonicalizeMedia(
		ctx,
		command.WorkspaceID,
		command.ActorID,
		content,
	)
	if err != nil {
		return DraftView{}, err
	}
	content, err = service.canonicalizeDestinations(ctx, command.WorkspaceID, content)
	if err != nil {
		return DraftView{}, err
	}
	current, err := service.repository.Get(ctx, command.WorkspaceID, command.DraftID)
	if err != nil {
		return DraftView{}, err
	}
	current.Content = content
	current.UpdatedAt = service.now().UTC()
	draft, err := service.repository.Update(
		ctx,
		current,
		command.ExpectedRevision,
		strings.TrimSpace(command.AutosaveKey),
	)
	if err != nil {
		return DraftView{}, err
	}
	return service.viewOf(draft), nil
}

func (service *Service) DeleteDraft(
	ctx context.Context,
	workspaceID, actorID, draftID string,
	expectedRevision int64,
) error {
	if err := service.authorize(ctx, workspaceID, actorID); err != nil {
		return err
	}
	if strings.TrimSpace(draftID) == "" || expectedRevision < 1 {
		return fmt.Errorf("%w: draft id and positive revision are required", ErrInvalidArgument)
	}
	return service.repository.Delete(ctx, workspaceID, draftID, expectedRevision)
}

func (service *Service) ListDraftRevisions(
	ctx context.Context,
	workspaceID, actorID, draftID string,
) ([]DraftRevision, error) {
	if err := service.authorize(ctx, workspaceID, actorID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(draftID) == "" {
		return nil, fmt.Errorf("%w: draft id is required", ErrInvalidArgument)
	}
	return service.repository.ListRevisions(ctx, workspaceID, draftID)
}

func (service *Service) CapabilityCatalog(
	ctx context.Context,
	workspaceID, actorID string,
) (CapabilityCatalog, error) {
	if err := service.authorize(ctx, workspaceID, actorID); err != nil {
		return CapabilityCatalog{}, err
	}
	return cloneCatalog(service.catalog), nil
}

func (service *Service) ValidateForScheduling(
	ctx context.Context,
	workspaceID, actorID, draftID string,
) (ValidationReport, error) {
	view, err := service.GetDraft(ctx, workspaceID, actorID, draftID)
	if err != nil {
		return ValidationReport{}, err
	}
	if !view.Validation.Valid {
		return view.Validation, &ValidationFailure{Report: view.Validation}
	}
	return view.Validation, nil
}

func (service *Service) authorize(
	ctx context.Context,
	workspaceID, actorID string,
) error {
	if strings.TrimSpace(actorID) == "" {
		return ErrUnauthenticated
	}
	if strings.TrimSpace(workspaceID) == "" {
		return fmt.Errorf("%w: workspace id is required", ErrInvalidArgument)
	}
	allowed, err := service.authorizer.CanManageContent(ctx, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("authorize content management: %w", err)
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func (service *Service) randomID() (string, error) {
	randomBytes := make([]byte, 18)
	if err := service.random(randomBytes); err != nil {
		return "", fmt.Errorf("generate draft id: %w", err)
	}
	return "draft_" + base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

func normalizeContent(content DraftContent) (DraftContent, error) {
	content = cloneContent(content)
	content.Text = norm.NFC.String(content.Text)
	content.Link = strings.TrimSpace(content.Link)
	mediaIDs := make(map[string]struct{}, len(content.Media))
	for index := range content.Media {
		media := &content.Media[index]
		media.ID = strings.TrimSpace(media.ID)
		if media.ID == "" {
			return DraftContent{}, invalidField(
				fmt.Sprintf("media[%d].id", index),
				"required",
				"media_id_required",
				"Media id is required.",
			)
		}
		if _, duplicate := mediaIDs[media.ID]; duplicate {
			return DraftContent{}, invalidField(
				fmt.Sprintf("media[%d].id", index),
				"unique",
				"media_id_duplicate",
				"Media ids must be unique within a draft.",
			)
		}
		mediaIDs[media.ID] = struct{}{}
	}
	destinationIDs := make(map[string]struct{}, len(content.Destinations))
	for index := range content.Destinations {
		destination := &content.Destinations[index]
		destination.ID = strings.TrimSpace(destination.ID)
		destination.ChannelID = strings.TrimSpace(destination.ChannelID)
		destination.CapabilityID = strings.TrimSpace(destination.CapabilityID)
		if destination.ID == "" {
			return DraftContent{}, invalidField(
				fmt.Sprintf("destinations[%d].id", index),
				"required",
				"destination_id_required",
				"Destination id is required.",
			)
		}
		if destination.ChannelID == "" {
			return DraftContent{}, invalidField(
				fmt.Sprintf("destinations[%d].channel_id", index),
				"required",
				"channel_id_required",
				"Destination channel id is required.",
			)
		}
		if strings.TrimSpace(string(destination.Format)) == "" {
			return DraftContent{}, invalidField(
				fmt.Sprintf("destinations[%d].format", index),
				"required",
				"format_required",
				"Destination format is required.",
			)
		}
		if _, duplicate := destinationIDs[destination.ID]; duplicate {
			return DraftContent{}, invalidField(
				fmt.Sprintf("destinations[%d].id", index),
				"unique",
				"destination_id_duplicate",
				"Destination ids must be unique within a draft.",
			)
		}
		destinationIDs[destination.ID] = struct{}{}
		if destination.TextOverride != nil {
			normalized := norm.NFC.String(*destination.TextOverride)
			destination.TextOverride = &normalized
		}
		if destination.LinkOverride != nil {
			normalized := strings.TrimSpace(*destination.LinkOverride)
			destination.LinkOverride = &normalized
		}
		for key, value := range destination.Fields {
			normalizedKey := strings.TrimSpace(key)
			if normalizedKey == "" || normalizedKey != key {
				return DraftContent{}, invalidField(
					fmt.Sprintf("destinations[%d].fields", index),
					"stable_non_empty_keys",
					"destination_field_name_invalid",
					"Destination field names must be non-empty and already normalized.",
				)
			}
			destination.Fields[key] = norm.NFC.String(value)
		}
	}
	for index := range content.Thread {
		content.Thread[index].Text = norm.NFC.String(content.Thread[index].Text)
		for mediaIndex := range content.Thread[index].MediaIDs {
			content.Thread[index].MediaIDs[mediaIndex] =
				strings.TrimSpace(content.Thread[index].MediaIDs[mediaIndex])
		}
	}
	return content, nil
}

func invalidField(field, rule, code, message string) error {
	return &FieldRuleError{
		Field:   field,
		Rule:    rule,
		Code:    code,
		Message: message,
	}
}

func (service *Service) viewOf(draft Draft) DraftView {
	draft = cloneDraft(draft)
	return DraftView{
		Draft:      draft,
		Validation: Validate(draft.Content, service.catalog),
	}
}

func (service *Service) canonicalizeMedia(
	ctx context.Context,
	workspaceID, actorID string,
	content DraftContent,
) (DraftContent, error) {
	if len(content.Media) == 0 || service.media == nil {
		return content, nil
	}
	canonical, err := service.media.Canonicalize(
		ctx,
		workspaceID,
		actorID,
		content.Media,
	)
	if err != nil {
		return DraftContent{}, err
	}
	content.Media = canonical
	return content, nil
}

func (service *Service) canonicalizeDestinations(
	ctx context.Context,
	workspaceID string,
	content DraftContent,
) (DraftContent, error) {
	if len(content.Destinations) == 0 || service.destinations == nil {
		return content, nil
	}
	for index := range content.Destinations {
		resolved, err := service.destinations.Resolve(
			ctx,
			workspaceID,
			content.Destinations[index].ChannelID,
			content.Destinations[index].Format,
		)
		if err != nil {
			var resolutionErr *destinationResolutionError
			if errors.As(err, &resolutionErr) {
				return DraftContent{}, &FieldRuleError{
					Field:   fmt.Sprintf("destinations[%d].channel_id", index),
					Rule:    resolutionErr.Rule,
					Code:    resolutionErr.Code,
					Message: resolutionErr.Message,
				}
			}
			return DraftContent{}, err
		}
		content.Destinations[index].ChannelType = resolved.ChannelType
		content.Destinations[index].CapabilityID = resolved.CapabilityID
		content.Destinations[index].Format = resolved.Format
	}
	return content, nil
}

func cloneDraft(draft Draft) Draft {
	draft.Content = cloneContent(draft.Content)
	return draft
}

func cloneContent(content DraftContent) DraftContent {
	copyOfContent := content
	copyOfContent.Media = append([]Media{}, content.Media...)
	copyOfContent.Thread = make([]ThreadItem, len(content.Thread))
	for index, item := range content.Thread {
		copyOfContent.Thread[index] = item
		copyOfContent.Thread[index].MediaIDs = append([]string{}, item.MediaIDs...)
	}
	copyOfContent.Destinations = make([]Destination, len(content.Destinations))
	for index, destination := range content.Destinations {
		copyOfContent.Destinations[index] = destination
		if destination.TextOverride != nil {
			text := *destination.TextOverride
			copyOfContent.Destinations[index].TextOverride = &text
		}
		if destination.MediaIDs != nil {
			mediaIDs := append([]string(nil), (*destination.MediaIDs)...)
			copyOfContent.Destinations[index].MediaIDs = &mediaIDs
		}
		if destination.ThreadOverride != nil {
			thread := make([]ThreadItem, len(*destination.ThreadOverride))
			for threadIndex, item := range *destination.ThreadOverride {
				thread[threadIndex] = item
				thread[threadIndex].MediaIDs = append([]string{}, item.MediaIDs...)
			}
			copyOfContent.Destinations[index].ThreadOverride = &thread
		}
		if destination.Fields != nil {
			fields := make(map[string]string, len(destination.Fields))
			for key, value := range destination.Fields {
				fields[key] = value
			}
			copyOfContent.Destinations[index].Fields = fields
		}
	}
	return copyOfContent
}
