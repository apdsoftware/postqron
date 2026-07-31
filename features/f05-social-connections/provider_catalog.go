package socialconnections

import "slices"

const ProviderCatalogVersion = "2026-07-30"

var providerResourceCatalog = map[Provider][]ResourceCapability{
	ProviderFacebookPages: {
		resourceCapability(
			ResourceFacebookPage,
			[]AccountType{AccountTypePage},
			PublishingModeAuto,
		),
	},
	ProviderFacebookGroups: {
		resourceCapability(
			ResourceFacebookGroup,
			[]AccountType{AccountTypeGroup},
			PublishingModeNotification,
		),
	},
	ProviderInstagramProfessional: {
		resourceCapability(
			ResourceInstagramProfessional,
			[]AccountType{AccountTypeBusiness, AccountTypeCreator},
			PublishingModeAuto,
		),
	},
	ProviderInstagramPersonal: {
		resourceCapability(
			ResourceInstagramPersonal,
			[]AccountType{AccountTypePersonal},
			PublishingModeNotification,
		),
	},
	ProviderX: {
		resourceCapability(
			ResourceXProfile,
			[]AccountType{AccountTypeProfile},
			PublishingModeAuto,
		),
	},
	ProviderLinkedIn: {
		resourceCapability(
			ResourceLinkedInProfile,
			[]AccountType{AccountTypeProfile},
			PublishingModeAuto,
		),
		resourceCapability(
			ResourceLinkedInPage,
			[]AccountType{AccountTypeOrganization},
			PublishingModeAuto,
		),
	},
	ProviderPinterest: {
		resourceCapability(
			ResourcePinterestBoard,
			[]AccountType{AccountTypeBoard},
			PublishingModeAuto,
		),
	},
	ProviderTikTok: {
		resourceCapability(
			ResourceTikTokProfile,
			[]AccountType{AccountTypeProfile},
			PublishingModeAuto,
		),
	},
	ProviderGoogleBusinessProfile: {
		resourceCapability(
			ResourceGoogleBusinessLocation,
			[]AccountType{AccountTypeLocation},
			PublishingModeAuto,
		),
	},
	ProviderMastodon: {
		resourceCapability(
			ResourceMastodonAccount,
			[]AccountType{AccountTypeProfile},
			PublishingModeAuto,
		),
	},
	ProviderYouTube: {
		resourceCapability(
			ResourceYouTubeChannel,
			[]AccountType{AccountTypeChannel},
			PublishingModeAuto,
		),
	},
	ProviderThreads: {
		resourceCapability(
			ResourceThreadsProfile,
			[]AccountType{AccountTypeProfile},
			PublishingModeAuto,
		),
	},
	ProviderBluesky: {
		resourceCapability(
			ResourceBlueskyAccount,
			[]AccountType{AccountTypeProfile},
			PublishingModeAuto,
		),
	},
}

func resourceCapability(
	resourceType ResourceType,
	accountTypes []AccountType,
	mode PublishingMode,
) ResourceCapability {
	return ResourceCapability{
		ResourceType:    resourceType,
		AccountTypes:    append([]AccountType(nil), accountTypes...),
		PublishingModes: []PublishingMode{mode},
	}
}

func providerResources(provider Provider) []ResourceCapability {
	resources := providerResourceCatalog[provider]
	result := make([]ResourceCapability, len(resources))
	for index, resource := range resources {
		result[index] = ResourceCapability{
			ResourceType: resource.ResourceType,
			AccountTypes: append([]AccountType(nil), resource.AccountTypes...),
			PublishingModes: append(
				[]PublishingMode(nil),
				resource.PublishingModes...,
			),
		}
	}
	return result
}

func providerAcceptsResource(
	provider Provider,
	resourceType ResourceType,
	accountType AccountType,
) bool {
	for _, resource := range providerResourceCatalog[provider] {
		if resource.ResourceType == resourceType &&
			slices.Contains(resource.AccountTypes, accountType) {
			return true
		}
	}
	return false
}

func adapterCapabilities(adapter Adapter) AdapterCapabilities {
	if adapter == nil {
		return AdapterCapabilities{}
	}
	if reporter, ok := adapter.(AdapterCapabilityReporter); ok {
		return reporter.AdapterCapabilities()
	}
	return AdapterCapabilities{
		Authorization:     true,
		PKCE:              adapter.Config().SupportsPKCE,
		ResourceSelection: true,
	}
}
