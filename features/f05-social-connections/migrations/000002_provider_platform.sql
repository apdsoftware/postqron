ALTER TABLE f05_oauth_attempts
    DROP CONSTRAINT IF EXISTS f05_oauth_attempts_provider_check,
    ADD CONSTRAINT f05_oauth_attempts_provider_check CHECK (
        provider IN (
            'facebook_pages',
            'facebook_groups',
            'instagram_professional',
            'instagram_personal',
            'x',
            'linkedin',
            'pinterest',
            'tiktok',
            'google_business_profile',
            'mastodon',
            'youtube',
            'threads',
            'bluesky'
        )
    );

ALTER TABLE f05_resource_selections
    DROP CONSTRAINT IF EXISTS f05_resource_selections_provider_check,
    ADD CONSTRAINT f05_resource_selections_provider_check CHECK (
        provider IN (
            'facebook_pages',
            'facebook_groups',
            'instagram_professional',
            'instagram_personal',
            'x',
            'linkedin',
            'pinterest',
            'tiktok',
            'google_business_profile',
            'mastodon',
            'youtube',
            'threads',
            'bluesky'
        )
    );

ALTER TABLE f05_selection_resources
    DROP CONSTRAINT IF EXISTS f05_selection_resources_resource_type_check,
    ADD CONSTRAINT f05_selection_resources_resource_type_check CHECK (
        resource_type IN (
            'facebook_page',
            'facebook_group',
            'instagram_professional',
            'instagram_personal',
            'x_profile',
            'linkedin_profile',
            'linkedin_page',
            'pinterest_board',
            'tiktok_profile',
            'google_business_profile_location',
            'mastodon_account',
            'youtube_channel',
            'threads_profile',
            'bluesky_account'
        )
    ),
    DROP CONSTRAINT IF EXISTS f05_selection_resources_account_type_check,
    ADD CONSTRAINT f05_selection_resources_account_type_check CHECK (
        account_type IN (
            'page',
            'group',
            'business',
            'creator',
            'personal',
            'profile',
            'organization',
            'board',
            'location',
            'channel'
        )
    );

ALTER TABLE f05_social_connections
    DROP CONSTRAINT IF EXISTS f05_social_connections_provider_check,
    ADD CONSTRAINT f05_social_connections_provider_check CHECK (
        provider IN (
            'facebook_pages',
            'facebook_groups',
            'instagram_professional',
            'instagram_personal',
            'x',
            'linkedin',
            'pinterest',
            'tiktok',
            'google_business_profile',
            'mastodon',
            'youtube',
            'threads',
            'bluesky'
        )
    ),
    DROP CONSTRAINT IF EXISTS f05_social_connections_resource_type_check,
    ADD CONSTRAINT f05_social_connections_resource_type_check CHECK (
        resource_type IN (
            'facebook_page',
            'facebook_group',
            'instagram_professional',
            'instagram_personal',
            'x_profile',
            'linkedin_profile',
            'linkedin_page',
            'pinterest_board',
            'tiktok_profile',
            'google_business_profile_location',
            'mastodon_account',
            'youtube_channel',
            'threads_profile',
            'bluesky_account'
        )
    ),
    DROP CONSTRAINT IF EXISTS f05_social_connections_account_type_check,
    ADD CONSTRAINT f05_social_connections_account_type_check CHECK (
        account_type IN (
            'page',
            'group',
            'business',
            'creator',
            'personal',
            'profile',
            'organization',
            'board',
            'location',
            'channel'
        )
    );

ALTER TABLE f05_social_outbox
    DROP CONSTRAINT IF EXISTS f05_social_outbox_provider_check,
    ADD CONSTRAINT f05_social_outbox_provider_check CHECK (
        provider IN (
            'facebook_pages',
            'facebook_groups',
            'instagram_professional',
            'instagram_personal',
            'x',
            'linkedin',
            'pinterest',
            'tiktok',
            'google_business_profile',
            'mastodon',
            'youtube',
            'threads',
            'bluesky'
        )
    );
