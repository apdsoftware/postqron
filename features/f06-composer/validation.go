package composer

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	maxImageBytes    int64 = 8 * 1024 * 1024
	maxCarouselBytes int64 = 80 * 1024 * 1024
	maxReelBytes     int64 = 100 * 1024 * 1024
	maxVideoBitrate  int64 = 25_000_000
	maxAudioBitrate  int64 = 128_000
	minImageRatio          = 4.0 / 5.0
	maxImageRatio          = 1.91
	reelRatio              = 9.0 / 16.0
	ratioTolerance         = 0.001
)

var absoluteURLPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s]+`)

func Validate(content DraftContent) ValidationReport {
	report := ValidationReport{
		Valid:        true,
		Errors:       make([]ValidationError, 0),
		Destinations: make([]DestinationValidation, 0, len(content.Destinations)),
	}
	if len(content.Destinations) == 0 {
		report.Errors = append(report.Errors, newValidationError(
			"",
			"destinations",
			"required",
			"destinations_required",
			"Select at least one destination.",
			nil,
		))
	}

	mediaByID := make(map[string]Media, len(content.Media))
	for _, media := range content.Media {
		mediaByID[media.ID] = media
	}
	for _, destination := range content.Destinations {
		result := validateDestination(content, destination, mediaByID)
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
) DestinationValidation {
	result := DestinationValidation{
		DestinationID: destination.ID,
		ChannelID:     destination.ChannelID,
		ChannelType:   destination.ChannelType,
		Format:        destination.Format,
		Valid:         true,
		Errors:        make([]ValidationError, 0),
	}
	add := func(field, rule, code, message string, details map[string]any) {
		result.Errors = append(result.Errors, newValidationError(
			destination.ID,
			field,
			rule,
			code,
			message,
			details,
		))
	}

	if !supportedChannel(destination.ChannelType) {
		add(
			"channel_type",
			"supported",
			"channel_unsupported",
			"The selected channel is not supported.",
			map[string]any{"actual": destination.ChannelType},
		)
	}
	if !supportedFormat(destination.ChannelType, destination.Format) {
		add(
			"format",
			"supported_for_channel",
			"format_unsupported",
			"The selected format is not supported by this channel.",
			map[string]any{
				"channel_type": destination.ChannelType,
				"actual":       destination.Format,
			},
		)
	}

	text := content.Text
	if destination.TextOverride != nil {
		text = *destination.TextOverride
	}
	text = norm.NFC.String(text)

	media := content.Media
	if destination.MediaIDs != nil {
		media = make([]Media, 0, len(*destination.MediaIDs))
		for index, mediaID := range *destination.MediaIDs {
			item, exists := mediaByID[mediaID]
			if !exists {
				add(
					fmt.Sprintf("media_ids[%d]", index),
					"references_draft_media",
					"media_not_found",
					"The selected media does not belong to this draft.",
					map[string]any{"media_id": mediaID},
				)
				continue
			}
			media = append(media, item)
		}
	}

	switch destination.Format {
	case FormatText:
		validateTextPost(text, media, add)
	case FormatImage:
		validateImagePost(destination.ChannelType, text, media, add)
	case FormatCarousel:
		validateCarousel(text, media, add)
	case FormatReel:
		validateReel(destination.ChannelType, text, media, add)
	}

	result.Valid = len(result.Errors) == 0
	return result
}

func validateTextPost(
	text string,
	media []Media,
	add func(string, string, string, string, map[string]any),
) {
	validateTextLength(text, 1, 5000, add)
	if len(media) != 0 {
		add(
			"media",
			"none",
			"media_not_allowed",
			"A Facebook text/link post cannot include media.",
			map[string]any{"actual_count": len(media)},
		)
	}
	urls := absoluteURLPattern.FindAllString(text, -1)
	if len(urls) > 1 {
		add(
			"text",
			"maximum_one_url",
			"too_many_urls",
			"Text/link posts can contain at most one absolute URL.",
			map[string]any{"actual_count": len(urls), "maximum": 1},
		)
	}
	for _, rawURL := range urls {
		validatePublicHTTPSURL(rawURL, add)
	}
}

func validatePublicHTTPSURL(
	rawURL string,
	add func(string, string, string, string, map[string]any),
) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.IsAbs() == false || parsed.Hostname() == "" ||
		!strings.EqualFold(parsed.Scheme, "https") {
		add(
			"text",
			"absolute_https_url",
			"url_must_be_https",
			"Links must be absolute HTTPS URLs.",
			map[string]any{"url": rawURL},
		)
		return
	}
	if parsed.User != nil {
		add(
			"text",
			"no_url_credentials",
			"url_credentials_not_allowed",
			"Links cannot contain credentials.",
			map[string]any{"url": rawURL},
		)
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		add(
			"text",
			"public_url_host",
			"url_host_not_public",
			"Links must use a public host.",
			map[string]any{"host": host},
		)
		return
	}
	if address := net.ParseIP(host); address != nil &&
		(!address.IsGlobalUnicast() ||
			address.IsPrivate() ||
			address.IsLoopback() ||
			address.IsLinkLocalUnicast() ||
			address.IsLinkLocalMulticast() ||
			address.IsUnspecified()) {
		add(
			"text",
			"public_url_host",
			"url_host_not_public",
			"Links must not target private, loopback, or link-local addresses.",
			map[string]any{"host": host},
		)
	}
}

func validateImagePost(
	channel ChannelType,
	text string,
	media []Media,
	add func(string, string, string, string, map[string]any),
) {
	maximumText := 5000
	if channel == ChannelInstagramProfessional {
		maximumText = 2200
	}
	validateTextLength(text, 0, maximumText, add)
	if !validateMediaCount(media, 1, 1, add) {
		return
	}
	allowedTypes := map[string]bool{"image/jpeg": true}
	if channel == ChannelFacebookPage {
		allowedTypes["image/png"] = true
	}
	validateImage(media[0], "media[0]", allowedTypes, add)
}

func validateCarousel(
	text string,
	media []Media,
	add func(string, string, string, string, map[string]any),
) {
	validateTextLength(text, 0, 2200, add)
	if !validateMediaCount(media, 2, 10, add) {
		return
	}
	var totalSize int64
	for index, item := range media {
		totalSize += item.SizeBytes
		validateImage(
			item,
			fmt.Sprintf("media[%d]", index),
			map[string]bool{"image/jpeg": true},
			add,
		)
	}
	if totalSize > maxCarouselBytes {
		add(
			"media",
			"maximum_total_size_bytes",
			"carousel_too_large",
			"The carousel exceeds the total size limit.",
			map[string]any{"actual": totalSize, "maximum": maxCarouselBytes},
		)
	}
	first := media[0]
	for index := 1; index < len(media); index++ {
		item := media[index]
		if first.Width <= 0 || first.Height <= 0 || item.Width <= 0 || item.Height <= 0 {
			continue
		}
		if int64(first.Width)*int64(item.Height) != int64(item.Width)*int64(first.Height) {
			add(
				fmt.Sprintf("media[%d].aspect_ratio", index),
				"same_carousel_ratio",
				"carousel_ratio_mismatch",
				"All carousel images must have the same aspect ratio.",
				map[string]any{
					"expected_width":  first.Width,
					"expected_height": first.Height,
					"actual_width":    item.Width,
					"actual_height":   item.Height,
				},
			)
		}
	}
}

func validateImage(
	media Media,
	field string,
	allowedTypes map[string]bool,
	add func(string, string, string, string, map[string]any),
) {
	if media.Kind != MediaImage {
		add(
			field+".kind",
			"image",
			"media_kind_invalid",
			"An image is required.",
			map[string]any{"actual": media.Kind},
		)
	}
	contentType := strings.ToLower(media.ContentType)
	if !allowedTypes[contentType] {
		allowed := make([]string, 0, len(allowedTypes))
		for value := range allowedTypes {
			allowed = append(allowed, value)
		}
		sort.Strings(allowed)
		add(
			field+".content_type",
			"allowed_image_type",
			"image_type_invalid",
			"The image type is not supported for this destination.",
			map[string]any{"actual": contentType, "allowed": allowed},
		)
	}
	if media.SizeBytes <= 0 || media.SizeBytes > maxImageBytes {
		add(
			field+".size_bytes",
			"range",
			"image_size_invalid",
			"Images must be non-empty and no larger than 8 MiB.",
			map[string]any{"actual": media.SizeBytes, "maximum": maxImageBytes},
		)
	}
	if !strings.EqualFold(media.ColorSpace, "sRGB") {
		add(
			field+".color_space",
			"srgb",
			"image_color_space_invalid",
			"Images must use the sRGB color space.",
			map[string]any{"actual": media.ColorSpace},
		)
	}
	if media.Width < 320 || media.Width > 1440 {
		add(
			field+".width",
			"range",
			"image_width_invalid",
			"Image width must be between 320 and 1,440 pixels.",
			map[string]any{"actual": media.Width, "minimum": 320, "maximum": 1440},
		)
	}
	if media.Height <= 0 {
		add(
			field+".height",
			"positive",
			"image_height_invalid",
			"Image height must be positive.",
			map[string]any{"actual": media.Height},
		)
		return
	}
	ratio := float64(media.Width) / float64(media.Height)
	if ratio < minImageRatio-ratioTolerance || ratio > maxImageRatio+ratioTolerance {
		add(
			field+".aspect_ratio",
			"range",
			"image_ratio_invalid",
			"Image aspect ratio must be between 4:5 and 1.91:1.",
			map[string]any{
				"actual":  ratio,
				"minimum": minImageRatio,
				"maximum": maxImageRatio,
			},
		)
	}
}

func validateReel(
	channel ChannelType,
	text string,
	media []Media,
	add func(string, string, string, string, map[string]any),
) {
	maximumText := 5000
	if channel == ChannelInstagramProfessional {
		maximumText = 2200
	}
	validateTextLength(text, 0, maximumText, add)
	if !validateMediaCount(media, 1, 1, add) {
		return
	}
	item := media[0]
	field := "media[0]"
	checkEqual(add, field+".kind", "video", "media_kind_invalid", item.Kind, MediaVideo)
	checkEqual(
		add,
		field+".content_type",
		"video/mp4",
		"video_type_invalid",
		strings.ToLower(item.ContentType),
		"video/mp4",
	)
	checkEqual(
		add,
		field+".video_codec",
		"h264",
		"video_codec_invalid",
		strings.ToLower(item.VideoCodec),
		"h264",
	)
	if item.SizeBytes <= 0 || item.SizeBytes > maxReelBytes {
		add(
			field+".size_bytes",
			"range",
			"video_size_invalid",
			"Reels must be non-empty and no larger than 100 MiB.",
			map[string]any{"actual": item.SizeBytes, "maximum": maxReelBytes},
		)
	}
	if item.HasAudio {
		checkEqual(
			add,
			field+".audio_codec",
			"aac",
			"audio_codec_invalid",
			strings.ToLower(item.AudioCodec),
			"aac",
		)
		if item.AudioSampleRate != 48000 {
			add(
				field+".audio_sample_rate",
				"equals",
				"audio_sample_rate_invalid",
				"Reel audio must use a 48 kHz sample rate.",
				map[string]any{"actual": item.AudioSampleRate, "expected": 48000},
			)
		}
		if item.AudioBitrate <= 0 || item.AudioBitrate > maxAudioBitrate {
			add(
				field+".audio_bitrate",
				"range",
				"audio_bitrate_invalid",
				"Reel audio bitrate must be at most 128 kbps.",
				map[string]any{"actual": item.AudioBitrate, "maximum": maxAudioBitrate},
			)
		}
	}
	if item.FramesPerSecond < 23 || item.FramesPerSecond > 60 {
		add(
			field+".frames_per_second",
			"range",
			"frame_rate_invalid",
			"Reel frame rate must be between 23 and 60 fps.",
			map[string]any{"actual": item.FramesPerSecond, "minimum": 23, "maximum": 60},
		)
	}
	if item.VideoBitrate <= 0 || item.VideoBitrate > maxVideoBitrate {
		add(
			field+".video_bitrate",
			"range",
			"video_bitrate_invalid",
			"Reel video bitrate must be at most 25 Mbps.",
			map[string]any{"actual": item.VideoBitrate, "maximum": maxVideoBitrate},
		)
	}
	if item.Width < 720 || item.Width > 1080 || item.Height < 1280 || item.Height > 1920 {
		add(
			field+".resolution",
			"range",
			"video_resolution_invalid",
			"Reel resolution must be between 720×1280 and 1080×1920.",
			map[string]any{
				"width": item.Width, "height": item.Height,
				"minimum_width": 720, "maximum_width": 1080,
				"minimum_height": 1280, "maximum_height": 1920,
			},
		)
	}
	if item.Height > 0 {
		ratio := float64(item.Width) / float64(item.Height)
		if math.Abs(ratio-reelRatio) > ratioTolerance {
			add(
				field+".aspect_ratio",
				"equals",
				"video_ratio_invalid",
				"Reels must use a 9:16 aspect ratio.",
				map[string]any{"actual": ratio, "expected": reelRatio},
			)
		}
	}
	if item.DurationSeconds < 4 || item.DurationSeconds > 60 {
		add(
			field+".duration_seconds",
			"range",
			"video_duration_invalid",
			"Reel duration must be between 4 and 60 seconds.",
			map[string]any{"actual": item.DurationSeconds, "minimum": 4, "maximum": 60},
		)
	}
	if item.HasEditList {
		add(
			field+".has_edit_list",
			"false",
			"video_edit_list_not_allowed",
			"Reels cannot contain an edit list.",
			nil,
		)
	}
	if !item.MoovBeforeMediaData {
		add(
			field+".moov_before_media_data",
			"true",
			"video_fast_start_required",
			"The MP4 moov atom must precede media data.",
			nil,
		)
	}
}

func validateTextLength(
	text string,
	minimum, maximum int,
	add func(string, string, string, string, map[string]any),
) {
	length := utf8.RuneCountInString(norm.NFC.String(text))
	if length < minimum {
		add(
			"text",
			"minimum_code_points",
			"text_too_short",
			"Text is required for this destination.",
			map[string]any{"actual": length, "minimum": minimum},
		)
	}
	if length > maximum {
		add(
			"text",
			"maximum_code_points",
			"text_too_long",
			"Text exceeds the limit for this destination.",
			map[string]any{"actual": length, "maximum": maximum},
		)
	}
}

func validateMediaCount(
	media []Media,
	minimum, maximum int,
	add func(string, string, string, string, map[string]any),
) bool {
	if len(media) < minimum || len(media) > maximum {
		add(
			"media",
			"count_range",
			"media_count_invalid",
			"The number of media items is invalid for this format.",
			map[string]any{
				"actual": len(media), "minimum": minimum, "maximum": maximum,
			},
		)
		return false
	}
	return true
}

func checkEqual(
	add func(string, string, string, string, map[string]any),
	field, expected, code string,
	actual, wanted any,
) {
	if actual == wanted {
		return
	}
	add(
		field,
		"equals",
		code,
		fmt.Sprintf("%s must be %s.", field, expected),
		map[string]any{"actual": actual, "expected": wanted},
	)
}

func supportedChannel(channel ChannelType) bool {
	return channel == ChannelFacebookPage ||
		channel == ChannelInstagramProfessional
}

func supportedFormat(channel ChannelType, format Format) bool {
	switch channel {
	case ChannelFacebookPage:
		return format == FormatText || format == FormatImage || format == FormatReel
	case ChannelInstagramProfessional:
		return format == FormatImage || format == FormatCarousel || format == FormatReel
	default:
		return false
	}
}

func newValidationError(
	destinationID, field, rule, code, message string,
	details map[string]any,
) ValidationError {
	return ValidationError{
		DestinationID: destinationID,
		Field:         field,
		Rule:          rule,
		Code:          code,
		Message:       message,
		Details:       details,
	}
}
