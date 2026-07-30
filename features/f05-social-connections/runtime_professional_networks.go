package socialconnections

const (
	configLinkedInEnabled   = "social.linkedin.enabled"
	configLinkedInID        = "social.linkedin.client_id"
	configLinkedInSecret    = "social.linkedin.client_secret"
	configLinkedInRedirect  = "social.linkedin.redirect_url"
	configLinkedInVersion   = "social.linkedin.api_version"
	configLinkedInReviewed  = "social.linkedin.review_approved"
	configLinkedInAudited   = "social.linkedin.runtime_audit_verified"
	configLinkedInSmoke     = "social.linkedin.smoke_verified"
	configLinkedInRefresh   = "social.linkedin.programmatic_refresh_approved"
	configGoogleGBPEnabled  = "social.google_business_profile.enabled"
	configGoogleGBPID       = "social.google_business_profile.client_id"
	configGoogleGBPSecret   = "social.google_business_profile.client_secret"
	configGoogleGBPRedirect = "social.google_business_profile.redirect_url"
	configGoogleGBPAccess   = "social.google_business_profile.api_access_approved"
	configGoogleGBPReviewed = "social.google_business_profile.oauth_review_approved"
	configGoogleGBPAudited  = "social.google_business_profile.runtime_audit_verified"
	configGoogleGBPSmoke    = "social.google_business_profile.smoke_verified"
)

func configureProfessionalNetworksRuntime(
	values map[string]string,
	cipher CredentialCipher,
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	configureLinkedInRuntime(values, cipher, adapters, availability)
	configureGoogleBusinessProfileRuntime(
		values,
		cipher,
		adapters,
		availability,
	)
}

func configureLinkedInRuntime(
	values map[string]string,
	cipher CredentialCipher,
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	if runtimeProviderValue(
		values,
		configLinkedInEnabled,
		"POSTQRON_F05_LINKEDIN_ENABLED",
	) != "true" {
		return
	}
	clientID := runtimeProviderValue(
		values,
		configLinkedInID,
		"POSTQRON_F05_LINKEDIN_CLIENT_ID",
	)
	clientSecret := runtimeProviderValue(
		values,
		configLinkedInSecret,
		"POSTQRON_F05_LINKEDIN_CLIENT_SECRET",
	)
	redirectURL := runtimeProviderValue(
		values,
		configLinkedInRedirect,
		"POSTQRON_F05_LINKEDIN_REDIRECT_URL",
	)
	apiVersion := runtimeProviderValue(
		values,
		configLinkedInVersion,
		"POSTQRON_F05_LINKEDIN_API_VERSION",
	)
	if clientID == "" || clientSecret == "" ||
		redirectURL == "" || apiVersion == "" {
		return
	}
	if runtimeProviderValue(
		values,
		configLinkedInReviewed,
		"POSTQRON_F05_LINKEDIN_REVIEW_APPROVED",
	) != "true" {
		availability[ProviderLinkedIn] = unavailableProvider(
			ProviderLinkedIn,
			ProviderReviewRequired,
		)
		return
	}
	if runtimeProviderValue(
		values,
		configLinkedInAudited,
		"POSTQRON_F05_LINKEDIN_RUNTIME_AUDIT_VERIFIED",
	) != "true" ||
		runtimeProviderValue(
			values,
			configLinkedInSmoke,
			"POSTQRON_F05_LINKEDIN_SMOKE_VERIFIED",
		) != "true" {
		availability[ProviderLinkedIn] = unavailableProvider(
			ProviderLinkedIn,
			ProviderAuditRequired,
		)
		return
	}
	// The provider-neutral runtime from #318 supplies the shared cipher. Never
	// mount a professional-network adapter without it.
	if cipher == nil {
		return
	}
	adapter, err := NewLinkedInAdapter(LinkedInAdapterConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		APIVersion:   apiVersion,
		ProgrammaticRefreshApproved: runtimeProviderValue(
			values,
			configLinkedInRefresh,
			"POSTQRON_F05_LINKEDIN_PROGRAMMATIC_REFRESH_APPROVED",
		) == "true",
	})
	if err != nil {
		return
	}
	adapters[ProviderLinkedIn] = adapter
	availability[ProviderLinkedIn] = ProviderAvailability{
		Provider:           ProviderLinkedIn,
		Status:             ProviderAvailable,
		ConfigurationState: ProviderReady,
	}
}

func configureGoogleBusinessProfileRuntime(
	values map[string]string,
	cipher CredentialCipher,
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	if runtimeProviderValue(
		values,
		configGoogleGBPEnabled,
		"POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_ENABLED",
	) != "true" {
		return
	}
	clientID := runtimeProviderValue(
		values,
		configGoogleGBPID,
		"POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_CLIENT_ID",
	)
	clientSecret := runtimeProviderValue(
		values,
		configGoogleGBPSecret,
		"POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_CLIENT_SECRET",
	)
	redirectURL := runtimeProviderValue(
		values,
		configGoogleGBPRedirect,
		"POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_REDIRECT_URL",
	)
	if clientID == "" || clientSecret == "" || redirectURL == "" {
		return
	}
	if runtimeProviderValue(
		values,
		configGoogleGBPAccess,
		"POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_API_ACCESS_APPROVED",
	) != "true" ||
		runtimeProviderValue(
			values,
			configGoogleGBPReviewed,
			"POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_OAUTH_REVIEW_APPROVED",
		) != "true" {
		availability[ProviderGoogleBusinessProfile] = unavailableProvider(
			ProviderGoogleBusinessProfile,
			ProviderReviewRequired,
		)
		return
	}
	if runtimeProviderValue(
		values,
		configGoogleGBPAudited,
		"POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_RUNTIME_AUDIT_VERIFIED",
	) != "true" ||
		runtimeProviderValue(
			values,
			configGoogleGBPSmoke,
			"POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_SMOKE_VERIFIED",
		) != "true" {
		availability[ProviderGoogleBusinessProfile] = unavailableProvider(
			ProviderGoogleBusinessProfile,
			ProviderAuditRequired,
		)
		return
	}
	if cipher == nil {
		return
	}
	adapter, err := NewGoogleBusinessProfileAdapter(
		GoogleBusinessProfileAdapterConfig{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
		},
	)
	if err != nil {
		return
	}
	adapters[ProviderGoogleBusinessProfile] = adapter
	availability[ProviderGoogleBusinessProfile] = ProviderAvailability{
		Provider:           ProviderGoogleBusinessProfile,
		Status:             ProviderAvailable,
		ConfigurationState: ProviderReady,
	}
}
