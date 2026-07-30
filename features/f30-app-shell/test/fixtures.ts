import type {
  SocialBootstrap,
  SocialProvider,
  SocialResourceType,
} from '../components/core/social-connections.ts'

const providerResources: Readonly<
  Record<SocialProvider, readonly SocialResourceType[]>
> = {
  facebook_pages: ['facebook_page'],
  facebook_groups: ['facebook_group'],
  instagram_professional: ['instagram_professional'],
  instagram_personal: ['instagram_personal'],
  x: ['x_profile'],
  linkedin: ['linkedin_profile', 'linkedin_page'],
  pinterest: ['pinterest_board'],
  tiktok: ['tiktok_profile'],
  google_business_profile: ['google_business_profile_location'],
  mastodon: ['mastodon_account'],
  youtube: ['youtube_channel'],
  threads: ['threads_profile'],
  bluesky: ['bluesky_account'],
}

const accountTypeByResource = {
  facebook_page: 'page',
  facebook_group: 'group',
  instagram_professional: 'business',
  instagram_personal: 'personal',
  x_profile: 'profile',
  linkedin_profile: 'profile',
  linkedin_page: 'organization',
  pinterest_board: 'board',
  tiktok_profile: 'profile',
  google_business_profile_location: 'location',
  mastodon_account: 'profile',
  youtube_channel: 'channel',
  threads_profile: 'profile',
  bluesky_account: 'profile',
} as const

export function socialBootstrapFixture(): SocialBootstrap {
  const providers: SocialProvider[] = [
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
    'bluesky',
  ]
  return {
    catalog_version: '2026-07-30',
    providers: [
      {
        provider: 'facebook_pages',
        status: 'available',
        configuration_state: 'ready',
        retryable: false,
      },
      {
        provider: 'instagram_professional',
        status: 'unavailable',
        configuration_state: 'review_required',
        retryable: false,
      },
    ],
    catalog: providers.map((provider) => {
      const ready = provider === 'facebook_pages'
      return {
        provider,
        status: ready ? 'available' : 'unavailable',
        configuration_state: ready ? 'ready' : 'not_configured',
        retryable: false,
        resources: providerResources[provider].map(resource_type => ({
          resource_type,
          account_types: [accountTypeByResource[resource_type]],
          publishing_modes: [
            resource_type === 'facebook_group'
            || resource_type === 'instagram_personal'
              ? 'notification'
              : 'auto',
          ],
        })),
        capabilities: {
          authorization: ready,
          pkce: ready,
          resource_selection: ready,
          token_refresh: false,
          remote_revocation: ready,
        },
      }
    }),
  }
}
