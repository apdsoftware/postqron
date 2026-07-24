package composer

import (
	"slices"
	"strings"
	"testing"
)

func TestValidateReturnsIndependentDestinationOutcomes(t *testing.T) {
	image := validImage("shared", "image/png", 1080, 1080)
	content := DraftContent{
		Text:  "A shared caption",
		Media: []Media{image},
		Destinations: []Destination{
			{
				ID:          "facebook",
				ChannelID:   "page-1",
				ChannelType: ChannelFacebookPage,
				Format:      FormatImage,
			},
			{
				ID:          "instagram",
				ChannelID:   "ig-1",
				ChannelType: ChannelInstagramProfessional,
				Format:      FormatImage,
			},
		},
	}

	report := Validate(content)

	if report.Valid {
		t.Fatal("shared PNG should be invalid for Instagram")
	}
	if len(report.Destinations) != 2 {
		t.Fatalf("destinations = %d", len(report.Destinations))
	}
	if !report.Destinations[0].Valid {
		t.Fatalf("Facebook errors = %#v", report.Destinations[0].Errors)
	}
	if report.Destinations[1].Valid {
		t.Fatal("Instagram result should be invalid")
	}
	assertErrorCode(t, report.Destinations[1].Errors, "image_type_invalid")
}

func TestValidateSupportsDestinationOverrides(t *testing.T) {
	png := validImage("facebook-image", "image/png", 1080, 1080)
	jpeg := validImage("instagram-image", "image/jpeg", 1080, 1080)
	facebookMedia := []string{png.ID}
	instagramMedia := []string{jpeg.ID}
	instagramText := strings.Repeat("x", 2200)
	content := DraftContent{
		Text:  strings.Repeat("f", 5000),
		Media: []Media{png, jpeg},
		Destinations: []Destination{
			{
				ID:          "facebook",
				ChannelID:   "page-1",
				ChannelType: ChannelFacebookPage,
				Format:      FormatImage,
				MediaIDs:    &facebookMedia,
			},
			{
				ID:           "instagram",
				ChannelID:    "ig-1",
				ChannelType:  ChannelInstagramProfessional,
				Format:       FormatImage,
				TextOverride: &instagramText,
				MediaIDs:     &instagramMedia,
			},
		},
	}

	report := Validate(content)

	if !report.Valid {
		t.Fatalf("report errors = %#v, outcomes = %#v", report.Errors, report.Destinations)
	}
}

func TestValidateEnforcesChannelAndFormatRules(t *testing.T) {
	content := DraftContent{
		Text: "Instagram cannot publish text-only posts.",
		Destinations: []Destination{{
			ID:          "instagram",
			ChannelID:   "ig-1",
			ChannelType: ChannelInstagramProfessional,
			Format:      FormatText,
		}},
	}

	report := Validate(content)

	if report.Valid {
		t.Fatal("Instagram text post should be invalid")
	}
	assertErrorCode(t, report.Destinations[0].Errors, "format_unsupported")
}

func TestValidateFacebookLinkRules(t *testing.T) {
	for name, text := range map[string]string{
		"insecure":    "Read http://example.com",
		"credentials": "Read https://user:secret@example.com/post",
		"private":     "Read https://192.168.1.20/post",
		"multiple":    "Read https://example.com and https://example.org",
	} {
		t.Run(name, func(t *testing.T) {
			report := Validate(DraftContent{
				Text: text,
				Destinations: []Destination{{
					ID:          "facebook",
					ChannelID:   "page-1",
					ChannelType: ChannelFacebookPage,
					Format:      FormatText,
				}},
			})
			if report.Valid {
				t.Fatal("unsafe link should be invalid")
			}
		})
	}
}

func TestValidateInstagramCarouselRules(t *testing.T) {
	first := validImage("first", "image/jpeg", 1080, 1080)
	second := validImage("second", "image/jpeg", 1080, 1350)
	content := DraftContent{
		Text:  "Carousel",
		Media: []Media{first, second},
		Destinations: []Destination{{
			ID:          "instagram",
			ChannelID:   "ig-1",
			ChannelType: ChannelInstagramProfessional,
			Format:      FormatCarousel,
		}},
	}

	report := Validate(content)

	if report.Valid {
		t.Fatal("mixed aspect ratios should be invalid")
	}
	assertErrorCode(t, report.Destinations[0].Errors, "carousel_ratio_mismatch")
}

func TestValidateReelRules(t *testing.T) {
	reel := validReel("reel")
	content := DraftContent{
		Text:  "Valid reel",
		Media: []Media{reel},
		Destinations: []Destination{
			{
				ID:          "facebook",
				ChannelID:   "page-1",
				ChannelType: ChannelFacebookPage,
				Format:      FormatReel,
			},
			{
				ID:          "instagram",
				ChannelID:   "ig-1",
				ChannelType: ChannelInstagramProfessional,
				Format:      FormatReel,
			},
		},
	}
	if report := Validate(content); !report.Valid {
		t.Fatalf("valid reel rejected: %#v", report)
	}

	content.Media[0].DurationSeconds = 61
	content.Media[0].MoovBeforeMediaData = false
	report := Validate(content)
	if report.Valid {
		t.Fatal("invalid reel accepted")
	}
	assertErrorCode(t, report.Destinations[0].Errors, "video_duration_invalid")
	assertErrorCode(t, report.Destinations[0].Errors, "video_fast_start_required")
}

func TestValidateCountsNFCUnicodeCodePoints(t *testing.T) {
	decomposed := strings.Repeat("e\u0301", 2200)
	content := DraftContent{
		Text:  decomposed,
		Media: []Media{validImage("image", "image/jpeg", 1080, 1080)},
		Destinations: []Destination{{
			ID:          "instagram",
			ChannelID:   "ig-1",
			ChannelType: ChannelInstagramProfessional,
			Format:      FormatImage,
		}},
	}

	if report := Validate(content); !report.Valid {
		t.Fatalf("NFC caption rejected: %#v", report.Destinations[0].Errors)
	}
}

func TestValidateRequiresDestination(t *testing.T) {
	report := Validate(DraftContent{})
	if report.Valid || len(report.Errors) != 1 {
		t.Fatalf("report = %#v", report)
	}
	assertErrorCode(t, report.Errors, "destinations_required")
}

func assertErrorCode(t *testing.T, errors []ValidationError, code string) {
	t.Helper()
	codes := make([]string, len(errors))
	for index, item := range errors {
		codes[index] = item.Code
	}
	if !slices.Contains(codes, code) {
		t.Fatalf("missing error %q in %v", code, codes)
	}
}

func validImage(id, contentType string, width, height int) Media {
	return Media{
		ID:          id,
		StorageKey:  "workspace/media/" + id,
		Kind:        MediaImage,
		ContentType: contentType,
		SizeBytes:   2 * 1024 * 1024,
		Width:       width,
		Height:      height,
		ColorSpace:  "sRGB",
	}
}

func validReel(id string) Media {
	return Media{
		ID:                  id,
		StorageKey:          "workspace/media/" + id,
		Kind:                MediaVideo,
		ContentType:         "video/mp4",
		SizeBytes:           20 * 1024 * 1024,
		Width:               1080,
		Height:              1920,
		VideoCodec:          "h264",
		AudioCodec:          "aac",
		AudioSampleRate:     48000,
		FramesPerSecond:     30,
		VideoBitrate:        8_000_000,
		AudioBitrate:        128_000,
		DurationSeconds:     30,
		HasAudio:            true,
		MoovBeforeMediaData: true,
	}
}
