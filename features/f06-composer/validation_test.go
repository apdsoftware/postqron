package composer

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestValidateCapabilityFixtureCoversEveryContentFamily(t *testing.T) {
	catalog := fixtureCatalog(t)
	imageOne := validImage("image-1", "image/jpeg", 1080, 1080)
	imageTwo := validImage("image-2", "image/jpeg", 1080, 1080)
	video := validVideo("video-1")
	tests := []struct {
		name    string
		content DraftContent
	}{
		{
			name: "text",
			content: DraftContent{
				Text:         "A text post",
				Destinations: []Destination{fixtureDestination("text")},
			},
		},
		{
			name: "link",
			content: DraftContent{
				Text:         "A link post",
				Link:         "https://example.com/post",
				Destinations: []Destination{fixtureDestination("link")},
			},
		},
		{
			name: "image",
			content: DraftContent{
				Text:         "An image",
				Media:        []Media{imageOne},
				Destinations: []Destination{fixtureDestination("image")},
			},
		},
		{
			name: "carousel",
			content: DraftContent{
				Text:         "A carousel",
				Media:        []Media{imageOne, imageTwo},
				Destinations: []Destination{fixtureDestination("carousel")},
			},
		},
		{
			name: "video with capability-specific field",
			content: DraftContent{
				Text:  "A video",
				Media: []Media{video},
				Destinations: []Destination{func() Destination {
					destination := fixtureDestination("video")
					destination.Fields = map[string]string{"visibility": "public"}
					return destination
				}()},
			},
		},
		{
			name: "short video",
			content: DraftContent{
				Text:         "A short",
				Media:        []Media{video},
				Destinations: []Destination{fixtureDestination("short_video")},
			},
		},
		{
			name: "thread",
			content: DraftContent{
				Thread:       []ThreadItem{{Text: "one"}, {Text: "two"}},
				Destinations: []Destination{fixtureDestination("thread")},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := Validate(test.content, catalog)
			if !report.Valid {
				t.Fatalf("valid fixture rejected: %#v", report)
			}
			if report.CapabilityVersion != catalog.Version {
				t.Fatalf("capability version = %q", report.CapabilityVersion)
			}
		})
	}
}

func TestValidateReturnsIndependentDestinationErrorsWithRemedies(t *testing.T) {
	catalog := fixtureCatalog(t)
	content := DraftContent{
		Text:  "Shared",
		Media: []Media{validImage("image", "image/png", 1080, 1080)},
		Destinations: []Destination{
			fixtureDestination("image"),
			fixtureDestination("carousel"),
		},
	}
	report := Validate(content, catalog)
	if report.Valid || !report.Destinations[0].Valid || report.Destinations[1].Valid {
		t.Fatalf("destination outcomes = %#v", report.Destinations)
	}
	error := report.Destinations[1].Errors[0]
	if error.DestinationID == "" || error.Field == "" ||
		error.Rule == "" || error.Remedy == "" {
		t.Fatalf("error is not actionable: %#v", error)
	}
}

func TestValidateFailsClosedForUnknownOrUnavailableCapabilities(t *testing.T) {
	catalog := fixtureCatalog(t)
	catalog.Capabilities = append(catalog.Capabilities, ContentCapability{
		ID: "fixture:blocked", Provider: "fixture",
		ChannelType: "blocked", Format: FormatText,
		Available: false, UnavailableReason: "external review pending",
		Text: TextRules{Allowed: true},
	})
	for name, destination := range map[string]Destination{
		"unknown": {
			ID: "unknown", ChannelID: "channel",
			ChannelType: "unknown", CapabilityID: "missing", Format: FormatText,
		},
		"unavailable": {
			ID: "blocked", ChannelID: "channel",
			ChannelType: "blocked", CapabilityID: "fixture:blocked", Format: FormatText,
		},
	} {
		t.Run(name, func(t *testing.T) {
			report := Validate(DraftContent{
				Text: "content", Destinations: []Destination{destination},
			}, catalog)
			if report.Valid {
				t.Fatal("fail-closed capability accepted")
			}
		})
	}
}

func TestValidateLinkAndCapabilitySpecificFieldRules(t *testing.T) {
	catalog := fixtureCatalog(t)
	linkDestination := fixtureDestination("link")
	report := Validate(DraftContent{
		Link:         "https://192.168.1.2/private",
		Destinations: []Destination{linkDestination},
	}, catalog)
	assertErrorCode(t, report.Destinations[0].Errors, "url_host_not_public")

	videoDestination := fixtureDestination("video")
	videoDestination.Fields = map[string]string{
		"visibility": "friends",
		"undeclared": "value",
	}
	report = Validate(DraftContent{
		Media:        []Media{validVideo("video")},
		Destinations: []Destination{videoDestination},
	}, catalog)
	assertErrorCode(t, report.Destinations[0].Errors, "destination_field_invalid")
	assertErrorCode(t, report.Destinations[0].Errors, "destination_field_unknown")
}

func TestValidateCountsNFCUnicodeCodePoints(t *testing.T) {
	catalog := fixtureCatalog(t)
	destination := fixtureDestination("text")
	report := Validate(DraftContent{
		Text:         strings.Repeat("e\u0301", 280),
		Destinations: []Destination{destination},
	}, catalog)
	if !report.Valid {
		t.Fatalf("NFC text rejected: %#v", report.Destinations[0].Errors)
	}
}

func TestValidateRequiresDestination(t *testing.T) {
	report := Validate(DraftContent{}, fixtureCatalog(t))
	if report.Valid || len(report.Errors) != 1 {
		t.Fatalf("report = %#v", report)
	}
	assertErrorCode(t, report.Errors, "destinations_required")
}

func fixtureCatalog(t *testing.T) CapabilityCatalog {
	t.Helper()
	encoded, err := os.ReadFile("test/fixtures/capabilities.json")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := ParseCapabilityCatalog(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func fixtureDestination(family string) Destination {
	format := Format(family)
	channelFamily := family
	if family == "short_video" {
		channelFamily = "short"
	}
	return Destination{
		ID:           "destination-" + family,
		ChannelID:    "channel-" + family,
		ChannelType:  ChannelType("fixture_" + channelFamily + "_channel"),
		CapabilityID: "fixture:" + family,
		Format:       format,
	}
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
		ID:               id,
		Kind:             MediaImage,
		ContentType:      contentType,
		SizeBytes:        2 * 1024 * 1024,
		Width:            width,
		Height:           height,
		InspectionStatus: InspectionReady,
		URL:              "/api/v1/media/" + id,
	}
}

func validVideo(id string) Media {
	return Media{
		ID:               id,
		Kind:             MediaVideo,
		ContentType:      "video/mp4",
		SizeBytes:        20 * 1024 * 1024,
		Width:            1080,
		Height:           1920,
		VideoCodec:       "h264",
		AudioCodec:       "aac",
		DurationSeconds:  30,
		HasAudio:         true,
		InspectionStatus: InspectionReady,
		URL:              "/api/v1/media/" + id,
	}
}
