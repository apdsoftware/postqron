package composer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type ResolvedDestination struct {
	Provider     string
	ChannelType  ChannelType
	CapabilityID string
	Format       Format
}

type DestinationResolver interface {
	Resolve(context.Context, string, string, Format) (ResolvedDestination, error)
}

type destinationResolutionError struct {
	Rule    string
	Code    string
	Message string
	Details map[string]any
}

func (err *destinationResolutionError) Error() string {
	return err.Code
}

type PostgresDestinationResolver struct {
	database *sql.DB
	catalog  CapabilityCatalog
}

func NewPostgresDestinationResolver(
	database *sql.DB,
	catalog CapabilityCatalog,
) (*PostgresDestinationResolver, error) {
	if database == nil {
		return nil, errors.New("composer destination resolver database is required")
	}
	return &PostgresDestinationResolver{
		database: database,
		catalog:  cloneCatalog(catalog),
	}, nil
}

func (resolver *PostgresDestinationResolver) Resolve(
	ctx context.Context,
	workspaceID, channelID string,
	format Format,
) (ResolvedDestination, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return ResolvedDestination{}, &destinationResolutionError{
			Rule:    "required",
			Code:    "channel_id_required",
			Message: "Destination channel id is required.",
		}
	}
	if strings.TrimSpace(string(format)) == "" {
		return ResolvedDestination{}, &destinationResolutionError{
			Rule:    "required",
			Code:    "format_required",
			Message: "Destination format is required.",
		}
	}
	var actualWorkspaceID, provider, status string
	err := resolver.database.QueryRowContext(ctx, `
		SELECT workspace_id, provider, status::text
		  FROM f05_social_connections
		 WHERE id = $1`,
		channelID,
	).Scan(&actualWorkspaceID, &provider, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ResolvedDestination{}, &destinationResolutionError{
			Rule:    "active_workspace_channel",
			Code:    "channel_unknown",
			Message: "The selected channel does not exist.",
			Details: map[string]any{"channel_id": channelID},
		}
	}
	if err != nil {
		return ResolvedDestination{}, fmt.Errorf("resolve composer channel: %w", err)
	}
	if actualWorkspaceID != workspaceID {
		return ResolvedDestination{}, &destinationResolutionError{
			Rule:    "active_workspace_channel",
			Code:    "channel_workspace_mismatch",
			Message: "The selected channel belongs to a different workspace.",
			Details: map[string]any{"channel_id": channelID},
		}
	}
	if status != "connected" {
		return ResolvedDestination{}, &destinationResolutionError{
			Rule:    "active_workspace_channel",
			Code:    "channel_disconnected",
			Message: "Reconnect the selected channel before using it in the composer.",
			Details: map[string]any{
				"channel_id": channelID,
				"status":     status,
			},
		}
	}
	capability, found, resolveErr := resolver.catalog.ResolveProviderFormat(
		provider,
		format,
	)
	if resolveErr != nil {
		return ResolvedDestination{}, fmt.Errorf(
			"resolve composer capability for provider %q format %q: %w",
			provider,
			format,
			resolveErr,
		)
	}
	if !found {
		return ResolvedDestination{}, &destinationResolutionError{
			Rule:    "provider_format_capability",
			Code:    "channel_format_unsupported",
			Message: "The selected channel does not advertise this format in the active capability catalog.",
			Details: map[string]any{
				"channel_id": channelID,
				"provider":   provider,
				"format":     format,
			},
		}
	}
	return ResolvedDestination{
		Provider:     provider,
		ChannelType:  capability.ChannelType,
		CapabilityID: capability.ID,
		Format:       capability.Format,
	}, nil
}
