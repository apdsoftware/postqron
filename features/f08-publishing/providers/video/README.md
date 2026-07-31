# TikTok and YouTube Shorts publishing

Both adapters implement the F8 `Publisher` contract and receive only opaque
F5 connection IDs. Every provider request is executed by
`socialconnections.AuthenticatedExecutor` with a compile-time provider guard.
Neither adapter accepts or returns OAuth tokens, client credentials, upload
capability URLs, or provider secrets.

TikTok payload:

```json
{
  "video": {
    "storage_key": "immutable/object.mp4",
    "source_url": "https://verified-media.example/tiktok/lowercase-hex-sha256/1024/object.mp4",
    "content_type": "video/mp4",
    "size_bytes": 1024,
    "sha256": "lowercase-hex-sha256",
    "width": 1080,
    "height": 1920,
    "duration_seconds": 30
  },
  "metadata": {
    "title": "Caption",
    "privacy_level": "SELF_ONLY",
    "disable_duet": false,
    "disable_stitch": false,
    "disable_comment": false,
    "brand_content": false,
    "brand_organic": false,
    "ai_generated": false
  },
  "creator_consent": true
}
```

`source_url` must exactly equal the configured, externally verified TikTok URL
prefix followed by `<sha256>/<size_bytes>/<storage basename>`. This binds the
pull URL to the immutable snapshot. URLs outside that prefix, unbound URLs,
IP-address prefixes, and URLs containing user info, query, or fragment fail
closed. The media delivery service must not redirect these immutable URLs.

YouTube uses the same `video` metadata without `source_url`, plus:

```json
{
  "metadata": {
    "channel_id": "channel-id",
    "title": "Short title",
    "description": "Description",
    "tags": ["tag"],
    "category_id": "22",
    "privacy_status": "private",
    "made_for_kids": false,
    "contains_synthetic_media": false
  }
}
```

YouTube Shorts accept only square or vertical snapshots (`width <= height`)
with positive dimensions and a duration of at most 180 seconds. The media
source must reopen the immutable object. F8 spools a
`multipart/related` body to a private temporary file, verifies byte count and
SHA-256 before transport, and removes the file when F5 closes it.

YouTube multipart upload does not claim deterministic reconciliation: a crash
before the response returns a video ID cannot be looked up safely. Its adapter
therefore declares `ambiguous_fail_closed`, and the engine dead-letters an
ambiguous outcome without replaying the upload. A future resumable
implementation requires the F5 boundary to preserve and safely bind the
provider-issued session URI.
