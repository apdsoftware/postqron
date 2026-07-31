# Meta notification resources (F5)

Verified on 2026-07-30 against the official provider documentation required by
issue #309.

## Facebook Groups

Graph API v19 deprecated `publish_to_groups`,
`groups_access_member_info`, the Groups API, and installation of apps in
groups. The removal applied to all Graph API versions on 2024-04-22. There is
therefore no official connection/discovery/verification lifecycle that F5 can
mount for notification publishing.

Result: `facebook_groups` stays `unavailable`; no OAuth scopes, token exchange,
resource discovery, or simulated adapter are exposed.

Source:
<https://developers.facebook.com/docs/graph-api/changelog/version19.0/>

## Instagram personal accounts

The current Instagram Platform APIs support professional accounts (Business
and Creator). They do not expose the connection and content-publishing
lifecycle for personal/consumer accounts.

Result: `instagram_personal` stays `unavailable`; the verified
`instagram_professional` adapter is unchanged. Notification delivery, if added
by a later product feature, must not be represented as an official F5 provider
connection.

Source: <https://developers.facebook.com/docs/instagram-platform/>
