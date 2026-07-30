package composer

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func Validate(content DraftContent, catalogs ...CapabilityCatalog) ValidationReport {
	catalog := BlockedCapabilityCatalog()
	if len(catalogs) > 0 {
		catalog = cloneCatalog(catalogs[0])
	}
	report := ValidationReport{
		CapabilityVersion: catalog.Version,
		Valid:             true,
		Errors:            []ValidationError{},
		Destinations:      make([]DestinationValidation, 0, len(content.Destinations)),
	}
	if len(content.Destinations) == 0 {
		report.Errors = append(report.Errors, validationError(
			"", "destinations", "required", "destinations_required",
			"Select at least one destination.",
			"Choose one or more connected channels before scheduling.", nil,
		))
	}

	mediaByID := make(map[string]Media, len(content.Media))
	for _, media := range content.Media {
		mediaByID[media.ID] = media
	}
	for _, destination := range content.Destinations {
		result := validateDestination(content, destination, mediaByID, catalog)
		if !result.Valid {
			report.Valid = false
		}
		report.Destinations = append(report.Destinations, result)
	}
	if len(report.Errors) > 0 {
		report.Valid = false
	}
	return report
}

func validateDestination(
	content DraftContent,
	destination Destination,
	mediaByID map[string]Media,
	catalog CapabilityCatalog,
) DestinationValidation {
	result := DestinationValidation{
		DestinationID: destination.ID,
		ChannelID:     destination.ChannelID,
		ChannelType:   destination.ChannelType,
		CapabilityID:  destination.CapabilityID,
		Format:        destination.Format,
		Valid:         true,
		Errors:        []ValidationError{},
	}
	add := func(field, rule, code, message, remedy string, details map[string]any) {
		result.Errors = append(result.Errors, validationError(
			destination.ID, field, rule, code, message, remedy, details,
		))
	}

	capability, found := catalog.Resolve(destination.CapabilityID)
	if !found {
		add(
			"capability_id", "catalog_reference", "capability_unknown",
			"The selected publishing capability is not in the active catalog.",
			"Refresh channel capabilities and select an available format.",
			map[string]any{
				"capability_id":      destination.CapabilityID,
				"capability_version": catalog.Version,
			},
		)
		result.Valid = false
		return result
	}
	if !capability.Available {
		add(
			"capability_id", "available", "capability_unavailable",
			"The selected publishing capability is not available.",
			"Choose an available channel format or wait until its provider prerequisites are complete.",
			map[string]any{"reason": capability.UnavailableReason},
		)
	}
	if destination.ChannelType != capability.ChannelType {
		add(
			"channel_type", "matches_capability", "channel_capability_mismatch",
			"The selected channel type does not match the capability.",
			"Refresh the channel and format selection.",
			map[string]any{
				"actual":   destination.ChannelType,
				"expected": capability.ChannelType,
			},
		)
	}
	if destination.Format != capability.Format {
		add(
			"format", "matches_capability", "format_capability_mismatch",
			"The selected format does not match the capability.",
			"Select the format advertised by the channel capability.",
			map[string]any{"actual": destination.Format, "expected": capability.Format},
		)
	}

	text := content.Text
	if destination.TextOverride != nil {
		text = *destination.TextOverride
	}
	text = norm.NFC.String(text)
	link := content.Link
	if destination.LinkOverride != nil {
		link = *destination.LinkOverride
	}
	link = strings.TrimSpace(link)
	thread := content.Thread
	if destination.ThreadOverride != nil {
		thread = *destination.ThreadOverride
	}
	media := resolveDestinationMedia(content.Media, destination, mediaByID, add)

	validateText(text, capability.Text, add)
	validateLink(link, text, capability.Link, add)
	validateMedia(media, capability.Media, add)
	validateThread(thread, mediaByID, capability.Thread, add)
	validateFields(destination.Fields, capability.Fields, add)
	result.Valid = len(result.Errors) == 0
	return result
}

func resolveDestinationMedia(
	defaultMedia []Media,
	destination Destination,
	mediaByID map[string]Media,
	add func(string, string, string, string, string, map[string]any),
) []Media {
	if destination.MediaIDs == nil {
		return defaultMedia
	}
	media := make([]Media, 0, len(*destination.MediaIDs))
	seen := make(map[string]struct{}, len(*destination.MediaIDs))
	for index, rawID := range *destination.MediaIDs {
		id := strings.TrimSpace(rawID)
		if _, duplicate := seen[id]; duplicate {
			add(
				fmt.Sprintf("media_ids[%d]", index), "unique", "media_reference_duplicate",
				"A destination cannot reference the same media more than once.",
				"Remove the duplicate media selection.", map[string]any{"media_id": id},
			)
			continue
		}
		seen[id] = struct{}{}
		item, exists := mediaByID[id]
		if !exists {
			add(
				fmt.Sprintf("media_ids[%d]", index), "references_draft_media", "media_not_found",
				"The selected media does not belong to this draft.",
				"Upload the media or select an asset already attached to this draft.",
				map[string]any{"media_id": id},
			)
			continue
		}
		media = append(media, item)
	}
	return media
}

func validateText(
	text string,
	rules TextRules,
	add func(string, string, string, string, string, map[string]any),
) {
	count := utf8.RuneCountInString(norm.NFC.String(text))
	if !rules.Allowed && count > 0 {
		add(
			"text", "not_allowed", "text_not_allowed",
			"Text is not supported for this destination.",
			"Remove the text or choose a format that supports it.", nil,
		)
		return
	}
	minimum := rules.MinCharacters
	if rules.Required && minimum < 1 {
		minimum = 1
	}
	if count < minimum {
		add(
			"text", "minimum_characters", "text_too_short",
			"The text is shorter than this destination allows.",
			"Add text until the minimum length is reached.",
			map[string]any{"actual": count, "minimum": minimum},
		)
	}
	if rules.MaxCharacters > 0 && count > rules.MaxCharacters {
		add(
			"text", "maximum_characters", "text_too_long",
			"The text is longer than this destination allows.",
			"Shorten the text or add a destination-specific override.",
			map[string]any{"actual": count, "maximum": rules.MaxCharacters},
		)
	}
}

func validateLink(
	link, text string,
	rules LinkRules,
	add func(string, string, string, string, string, map[string]any),
) {
	if !rules.Allowed && link != "" {
		add(
			"link", "not_allowed", "link_not_allowed",
			"A separate link is not supported for this destination.",
			"Remove the link or choose a capability that accepts links.", nil,
		)
		return
	}
	if rules.Required && link == "" {
		add(
			"link", "required", "link_required",
			"A link is required for this destination.",
			"Enter an absolute public URL.", nil,
		)
	}
	urls := make([]string, 0)
	if link != "" {
		urls = append(urls, link)
	}
	for _, field := range strings.Fields(text) {
		if parsed, err := url.Parse(field); err == nil && parsed.IsAbs() {
			urls = append(urls, field)
		}
	}
	if rules.MaximumURLs > 0 && len(urls) > rules.MaximumURLs {
		add(
			"link", "maximum_urls", "too_many_urls",
			"The destination contains more links than its capability allows.",
			"Remove links or use a destination-specific text override.",
			map[string]any{"actual": len(urls), "maximum": rules.MaximumURLs},
		)
	}
	for _, rawURL := range urls {
		validateURL(rawURL, rules, add)
	}
}

func validateURL(
	rawURL string,
	rules LinkRules,
	add func(string, string, string, string, string, map[string]any),
) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		add(
			"link", "absolute_url", "url_invalid",
			"The link must be an absolute URL.",
			"Enter a complete URL including its scheme and host.",
			map[string]any{"url": rawURL},
		)
		return
	}
	if parsed.User != nil {
		add(
			"link", "no_credentials", "url_credentials_not_allowed",
			"Links cannot contain credentials.",
			"Remove the username and password from the URL.", nil,
		)
	}
	if rules.RequireHTTPS && !strings.EqualFold(parsed.Scheme, "https") {
		add(
			"link", "https", "url_must_be_https",
			"The destination requires an HTTPS link.",
			"Use the HTTPS version of the URL.", map[string]any{"url": rawURL},
		)
	}
	if rules.RequirePublicHost && !publicURLHost(parsed.Hostname()) {
		add(
			"link", "public_host", "url_host_not_public",
			"The link targets a private or local address.",
			"Use a publicly reachable URL.", map[string]any{"host": parsed.Hostname()},
		)
	}
}

func publicURLHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return false
	}
	address := net.ParseIP(host)
	return address == nil || (address.IsGlobalUnicast() &&
		!address.IsPrivate() &&
		!address.IsLoopback() &&
		!address.IsLinkLocalUnicast() &&
		!address.IsLinkLocalMulticast() &&
		!address.IsUnspecified())
}

func validateMedia(
	media []Media,
	rules MediaRules,
	add func(string, string, string, string, string, map[string]any),
) {
	if !rules.Allowed && len(media) > 0 {
		add(
			"media", "not_allowed", "media_not_allowed",
			"Media is not supported for this destination.",
			"Remove the media or choose a capability that supports it.", nil,
		)
		return
	}
	if len(media) < rules.MinimumItems {
		add(
			"media", "minimum_items", "media_too_few",
			"The destination does not have enough media items.",
			"Add media until the minimum is reached.",
			map[string]any{"actual": len(media), "minimum": rules.MinimumItems},
		)
	}
	if rules.MaximumItems > 0 && len(media) > rules.MaximumItems {
		add(
			"media", "maximum_items", "media_too_many",
			"The destination has too many media items.",
			"Remove media or choose a compatible format.",
			map[string]any{"actual": len(media), "maximum": rules.MaximumItems},
		)
	}
	var total int64
	for index, item := range media {
		total += item.SizeBytes
		field := fmt.Sprintf("media[%d]", index)
		if item.InspectionStatus != InspectionReady {
			add(
				field+".inspection_status", "ready", "media_not_inspected",
				"The media upload has not passed server inspection.",
				"Wait for inspection or replace the rejected upload.",
				map[string]any{"actual": item.InspectionStatus},
			)
		}
		if len(rules.AllowedKinds) > 0 && !slices.Contains(rules.AllowedKinds, item.Kind) {
			add(
				field+".kind", "allowed_kind", "media_kind_invalid",
				"The media kind is not supported for this destination.",
				"Replace it with an allowed media kind.",
				map[string]any{"actual": item.Kind, "allowed": rules.AllowedKinds},
			)
		}
		contentType := strings.ToLower(item.ContentType)
		if len(rules.AllowedContentTypes) > 0 &&
			!containsFold(rules.AllowedContentTypes, contentType) {
			add(
				field+".content_type", "allowed_content_type", "media_type_invalid",
				"The inspected media type is not supported for this destination.",
				"Upload media using one of the allowed content types.",
				map[string]any{"actual": contentType, "allowed": rules.AllowedContentTypes},
			)
		}
		validateMediaNumbers(item, field, rules, add)
	}
	if rules.MaximumBytesTotal > 0 && total > rules.MaximumBytesTotal {
		add(
			"media", "maximum_total_bytes", "media_total_too_large",
			"The combined media size exceeds the destination capability.",
			"Reduce the media size or number of items.",
			map[string]any{"actual": total, "maximum": rules.MaximumBytesTotal},
		)
	}
}

func validateMediaNumbers(
	item Media,
	field string,
	rules MediaRules,
	add func(string, string, string, string, string, map[string]any),
) {
	if item.SizeBytes <= 0 ||
		(rules.MaximumBytesEach > 0 && item.SizeBytes > rules.MaximumBytesEach) {
		add(
			field+".size_bytes", "range", "media_size_invalid",
			"The inspected media size is outside the allowed range.",
			"Upload a non-empty file within the advertised size limit.",
			map[string]any{"actual": item.SizeBytes, "maximum": rules.MaximumBytesEach},
		)
	}
	checkIntegerRange(field+".width", item.Width, rules.MinimumWidth, rules.MaximumWidth, add)
	checkIntegerRange(field+".height", item.Height, rules.MinimumHeight, rules.MaximumHeight, add)
	if item.Width > 0 && item.Height > 0 {
		ratio := float64(item.Width) / float64(item.Height)
		if (rules.MinimumAspectRatio > 0 && ratio < rules.MinimumAspectRatio) ||
			(rules.MaximumAspectRatio > 0 && ratio > rules.MaximumAspectRatio) {
			add(
				field+".aspect_ratio", "range", "media_aspect_ratio_invalid",
				"The inspected media aspect ratio is outside the allowed range.",
				"Crop or replace the media using an advertised aspect ratio.",
				map[string]any{
					"actual":  ratio,
					"minimum": rules.MinimumAspectRatio,
					"maximum": rules.MaximumAspectRatio,
				},
			)
		}
	}
	if (rules.MinimumDuration > 0 && item.DurationSeconds < rules.MinimumDuration) ||
		(rules.MaximumDuration > 0 && item.DurationSeconds > rules.MaximumDuration) {
		add(
			field+".duration_seconds", "range", "media_duration_invalid",
			"The inspected media duration is outside the allowed range.",
			"Trim or replace the video using the advertised duration.",
			map[string]any{
				"actual":  item.DurationSeconds,
				"minimum": rules.MinimumDuration,
				"maximum": rules.MaximumDuration,
			},
		)
	}
	if len(rules.AllowedVideoCodecs) > 0 &&
		!containsFold(rules.AllowedVideoCodecs, item.VideoCodec) {
		add(
			field+".video_codec", "allowed_codec", "video_codec_invalid",
			"The inspected video codec is not supported.",
			"Transcode the video to an advertised codec.",
			map[string]any{"actual": item.VideoCodec, "allowed": rules.AllowedVideoCodecs},
		)
	}
	if rules.RequireAudio && !item.HasAudio {
		add(
			field+".has_audio", "required", "audio_required",
			"This destination requires an audio track.",
			"Upload a video containing audio.", nil,
		)
	}
	if item.HasAudio && len(rules.AllowedAudioCodecs) > 0 &&
		!containsFold(rules.AllowedAudioCodecs, item.AudioCodec) {
		add(
			field+".audio_codec", "allowed_codec", "audio_codec_invalid",
			"The inspected audio codec is not supported.",
			"Transcode audio to an advertised codec.",
			map[string]any{"actual": item.AudioCodec, "allowed": rules.AllowedAudioCodecs},
		)
	}
}

func checkIntegerRange(
	field string,
	actual, minimum, maximum int,
	add func(string, string, string, string, string, map[string]any),
) {
	if (minimum > 0 && actual < minimum) || (maximum > 0 && actual > maximum) {
		add(
			field, "range", "media_dimension_invalid",
			"The inspected media dimension is outside the allowed range.",
			"Resize or replace the media using an advertised dimension.",
			map[string]any{"actual": actual, "minimum": minimum, "maximum": maximum},
		)
	}
}

func validateThread(
	thread []ThreadItem,
	mediaByID map[string]Media,
	rules ThreadRules,
	add func(string, string, string, string, string, map[string]any),
) {
	if !rules.Allowed && len(thread) > 0 {
		add(
			"thread", "not_allowed", "thread_not_allowed",
			"A thread is not supported for this destination.",
			"Remove the thread or choose a thread capability.", nil,
		)
		return
	}
	minimum := rules.MinimumItems
	if rules.Required && minimum < 1 {
		minimum = 1
	}
	if len(thread) < minimum {
		add(
			"thread", "minimum_items", "thread_too_short",
			"The thread has too few items.",
			"Add thread items until the minimum is reached.",
			map[string]any{"actual": len(thread), "minimum": minimum},
		)
	}
	if rules.MaximumItems > 0 && len(thread) > rules.MaximumItems {
		add(
			"thread", "maximum_items", "thread_too_long",
			"The thread has too many items.",
			"Remove thread items until the advertised limit is met.",
			map[string]any{"actual": len(thread), "maximum": rules.MaximumItems},
		)
	}
	for index, item := range thread {
		count := utf8.RuneCountInString(norm.NFC.String(item.Text))
		if rules.MaxItemCharacters > 0 && count > rules.MaxItemCharacters {
			add(
				fmt.Sprintf("thread[%d].text", index), "maximum_characters",
				"thread_item_text_too_long",
				"A thread item is longer than the destination allows.",
				"Shorten that thread item.",
				map[string]any{"actual": count, "maximum": rules.MaxItemCharacters},
			)
		}
		if rules.MaxMediaPerItem >= 0 && len(item.MediaIDs) > rules.MaxMediaPerItem {
			add(
				fmt.Sprintf("thread[%d].media_ids", index), "maximum_items",
				"thread_item_media_too_many",
				"A thread item has too many media attachments.",
				"Remove media from that thread item.",
				map[string]any{"actual": len(item.MediaIDs), "maximum": rules.MaxMediaPerItem},
			)
		}
		for mediaIndex, mediaID := range item.MediaIDs {
			if _, found := mediaByID[mediaID]; !found {
				add(
					fmt.Sprintf("thread[%d].media_ids[%d]", index, mediaIndex),
					"references_draft_media", "media_not_found",
					"The thread references media that is not attached to the draft.",
					"Upload or attach the referenced media.", map[string]any{"media_id": mediaID},
				)
			}
		}
	}
}

func validateFields(
	values map[string]string,
	rules []FieldRules,
	add func(string, string, string, string, string, map[string]any),
) {
	known := make(map[string]FieldRules, len(rules))
	for _, rule := range rules {
		known[rule.Name] = rule
		value := strings.TrimSpace(values[rule.Name])
		if rule.Required && value == "" {
			add(
				"fields."+rule.Name, "required", "destination_field_required",
				"A destination-specific field is required.",
				"Complete the field using the capability definition.",
				map[string]any{"field": rule.Name},
			)
		}
		if rule.MaxLength > 0 && utf8.RuneCountInString(value) > rule.MaxLength {
			add(
				"fields."+rule.Name, "maximum_characters", "destination_field_too_long",
				"A destination-specific field is too long.",
				"Shorten the value to the advertised limit.",
				map[string]any{"maximum": rule.MaxLength},
			)
		}
		if value != "" && len(rule.Allowed) > 0 && !slices.Contains(rule.Allowed, value) {
			add(
				"fields."+rule.Name, "allowed_value", "destination_field_invalid",
				"A destination-specific field has an unsupported value.",
				"Choose one of the values advertised by the capability.",
				map[string]any{"actual": value, "allowed": rule.Allowed},
			)
		}
	}
	for name := range values {
		if _, found := known[name]; !found {
			add(
				"fields."+name, "declared_by_capability", "destination_field_unknown",
				"The destination-specific field is not declared by the capability.",
				"Remove the field or refresh the capability catalog.", nil,
			)
		}
	}
}

func containsFold(values []string, candidate string) bool {
	for _, value := range values {
		if strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}

func validationError(
	destinationID, field, rule, code, message, remedy string,
	details map[string]any,
) ValidationError {
	return ValidationError{
		DestinationID: destinationID,
		Field:         field,
		Rule:          rule,
		Code:          code,
		Message:       message,
		Remedy:        remedy,
		Details:       details,
	}
}
