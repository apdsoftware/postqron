package composer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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
	Update(context.Context, Draft, int64) (Draft, error)
	Delete(context.Context, string, string, int64) error
}

type Service struct {
	repository Repository
	authorizer ContentAuthorizer
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
	return viewOf(draft), nil
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
	return viewOf(draft), nil
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
		views[index] = viewOf(drafts[index])
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
	current, err := service.repository.Get(ctx, command.WorkspaceID, command.DraftID)
	if err != nil {
		return DraftView{}, err
	}
	current.Content = content
	current.UpdatedAt = service.now().UTC()
	draft, err := service.repository.Update(ctx, current, command.ExpectedRevision)
	if err != nil {
		return DraftView{}, err
	}
	return viewOf(draft), nil
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
	mediaIDs := make(map[string]struct{}, len(content.Media))
	for index := range content.Media {
		media := &content.Media[index]
		media.ID = strings.TrimSpace(media.ID)
		media.StorageKey = strings.TrimSpace(media.StorageKey)
		if media.ID == "" {
			return DraftContent{}, invalidField(
				fmt.Sprintf("media[%d].id", index),
				"required",
				"media_id_required",
				"Media id is required.",
			)
		}
		if media.StorageKey == "" {
			return DraftContent{}, invalidField(
				fmt.Sprintf("media[%d].storage_key", index),
				"required",
				"media_storage_key_required",
				"Media storage key is required.",
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

func viewOf(draft Draft) DraftView {
	draft = cloneDraft(draft)
	return DraftView{
		Draft:      draft,
		Validation: Validate(draft.Content),
	}
}

func cloneDraft(draft Draft) Draft {
	draft.Content = cloneContent(draft.Content)
	return draft
}

func cloneContent(content DraftContent) DraftContent {
	copyOfContent := content
	copyOfContent.Media = append([]Media(nil), content.Media...)
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
	}
	return copyOfContent
}
