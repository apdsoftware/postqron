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
    "source_url": "https://verified-media.example/immutable/object.mp4",
    "content_type": "video/mp4",
    "size_bytes": 1024,
    "sha256": "lowercase-hex-sha256",
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

`source_url` must be HTTPS without user info, query, redirect state, or
fragment. The configured TikTok application must own the domain/prefix.

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

The media source must reopen the immutable object. F8 spools a
`multipart/related` body to a private temporary file, verifies byte count and
SHA-256 before transport, and removes the file when F5 closes it.
