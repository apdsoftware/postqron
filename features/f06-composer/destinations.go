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

var supportedChannelResources = map[string]map[string]map[string]struct{}{
	"facebook_pages": {
		"facebook_page": {"page": {}},
	},
	"facebook_groups": {
		"facebook_group": {"group": {}},
	},
	"instagram_professional": {
		"instagram_professional": {"business": {}, "creator": {}},
	},
	"instagram_personal": {
		"instagram_personal": {"personal": {}},
	},
	"x": {
		"x_profile": {"profile": {}},
	},
	"linkedin": {
		"linkedin_profile": {"profile": {}},
		"linkedin_page":    {"organization": {}},
	},
	"pinterest": {
		"pinterest_board": {"board": {}},
	},
	"tiktok": {
		"tiktok_profile": {"profile": {}},
	},
	"google_business_profile": {
		"google_business_profile_location": {"location": {}},
	},
	"mastodon": {
		"mastodon_account": {"profile": {}},
	},
	"youtube": {
		"youtube_channel": {"channel": {}},
	},
	"threads": {
		"threads_profile": {"profile": {}},
	},
	"bluesky": {
		"bluesky_account": {"profile": {}},
	},
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
	var actualWorkspaceID, provider, resourceType, accountType, status string
	err := resolver.database.QueryRowContext(ctx, `
		SELECT workspace_id, provider, resource_type, account_type, status::text
		  FROM f05_social_connections
		 WHERE id = $1`,
		channelID,
	).Scan(
		&actualWorkspaceID,
		&provider,
		&resourceType,
		&accountType,
		&status,
	)
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
	if !supportsChannelResource(provider, resourceType, accountType) {
		return ResolvedDestination{}, &destinationResolutionError{
			Rule:    "active_workspace_channel",
			Code:    "channel_resource_unsupported",
			Message: "The selected channel resource is not supported by the composer runtime.",
			Details: map[string]any{
				"channel_id":    channelID,
				"provider":      provider,
				"resource_type": resourceType,
				"account_type":  accountType,
			},
		}
	}
	capability, found, resolveErr := resolver.catalog.ResolveProviderResourceFormat(
		provider,
		resourceType,
		format,
	)
	if resolveErr != nil {
		return ResolvedDestination{}, fmt.Errorf(
			"resolve composer capability for provider %q resource %q format %q: %w",
			provider,
			resourceType,
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
				"channel_id":    channelID,
				"provider":      provider,
				"resource_type": resourceType,
				"format":        format,
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

func supportsChannelResource(provider, resourceType, accountType string) bool {
	resources, found := supportedChannelResources[provider]
	if !found {
		return false
	}
	accountTypes, found := resources[resourceType]
	if !found {
		return false
	}
	_, found = accountTypes[accountType]
	return found
}

func channelTypeMatchesResource(
	channelType ChannelType,
	resourceType string,
) bool {
	raw := strings.TrimSpace(string(channelType))
	resourceType = strings.TrimSpace(resourceType)
	return raw == resourceType || strings.HasPrefix(raw, resourceType+"_")
}
