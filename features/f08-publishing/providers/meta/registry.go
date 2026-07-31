package meta

import (
	"fmt"
	"strings"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

// RegistrationConfig is the only worker integration surface. The caller may
// inject an already configured public F5 AuthenticatedExecutor; this package
// never constructs F5 services or reads provider credentials.
type RegistrationConfig struct {
	Executor            *socialconnections.AuthenticatedExecutor
	GraphVersion        string
	ThreadsGraphVersion string
	AutoProviders       []socialconnections.Provider
	NotificationStore   NotificationStore
	NotificationSender  NotificationSender
}

func Register(
	registry *publishing.AdapterRegistry,
	config RegistrationConfig,
) error {
	if registry == nil {
		return publishing.ErrInvalidArgument
	}
	autoConfigured := config.Executor != nil ||
		strings.TrimSpace(config.GraphVersion) != "" ||
		strings.TrimSpace(config.ThreadsGraphVersion) != "" ||
		len(config.AutoProviders) != 0
	if autoConfigured {
		if config.Executor == nil {
			return fmt.Errorf(
				"%w: incomplete Meta authenticated executor registration",
				publishing.ErrProviderUnavailable,
			)
		}
		providers := config.AutoProviders
		if len(providers) == 0 {
			providers = []socialconnections.Provider{
				socialconnections.ProviderFacebookPages,
				socialconnections.ProviderInstagramProfessional,
				socialconnections.ProviderThreads,
			}
		}
		for _, name := range providers {
			version := config.GraphVersion
			if name == socialconnections.ProviderThreads {
				version = config.ThreadsGraphVersion
			}
			if name != socialconnections.ProviderFacebookPages &&
				name != socialconnections.ProviderInstagramProfessional &&
				name != socialconnections.ProviderThreads {
				return publishing.ErrInvalidArgument
			}
			if name != socialconnections.ProviderThreads &&
				strings.TrimSpace(version) == "" {
				return fmt.Errorf(
					"%w: missing Graph version for %s",
					publishing.ErrProviderUnavailable,
					name,
				)
			}
			provider := struct {
				name    socialconnections.Provider
				version string
			}{name: name, version: version}
			adapter, err := NewPublisher(Config{
				Executor: config.Executor, Provider: provider.name,
				GraphVersion: provider.version,
			})
			if err != nil {
				return err
			}
			if err = registry.RegisterPublisher(
				string(provider.name),
				adapter,
			); err != nil {
				return err
			}
		}
	}
	if config.NotificationStore != nil {
		for _, provider := range []string{
			string(socialconnections.ProviderFacebookGroups),
			string(socialconnections.ProviderInstagramPersonal),
		} {
			notifier, err := NewNotificationPublisher(
				provider,
				config.NotificationStore,
			)
			if err != nil {
				return err
			}
			if err = registry.RegisterNotificationPublisher(
				provider,
				notifier,
			); err != nil {
				return err
			}
		}
	}
	return nil
}
