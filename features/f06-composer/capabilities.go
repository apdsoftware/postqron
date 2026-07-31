package composer

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type TextRules struct {
	Allowed       bool `json:"allowed"`
	Required      bool `json:"required"`
	MinCharacters int  `json:"min_characters,omitempty"`
	MaxCharacters int  `json:"max_characters,omitempty"`
}

type LinkRules struct {
	Allowed           bool `json:"allowed"`
	Required          bool `json:"required"`
	MaximumURLs       int  `json:"maximum_urls,omitempty"`
	RequireHTTPS      bool `json:"require_https,omitempty"`
	RequirePublicHost bool `json:"require_public_host,omitempty"`
}

type MediaRules struct {
	Allowed             bool        `json:"allowed"`
	MinimumItems        int         `json:"minimum_items,omitempty"`
	MaximumItems        int         `json:"maximum_items,omitempty"`
	AllowedKinds        []MediaKind `json:"allowed_kinds,omitempty"`
	AllowedContentTypes []string    `json:"allowed_content_types,omitempty"`
	MaximumBytesEach    int64       `json:"maximum_bytes_each,omitempty"`
	MaximumBytesTotal   int64       `json:"maximum_bytes_total,omitempty"`
	MinimumWidth        int         `json:"minimum_width,omitempty"`
	MaximumWidth        int         `json:"maximum_width,omitempty"`
	MinimumHeight       int         `json:"minimum_height,omitempty"`
	MaximumHeight       int         `json:"maximum_height,omitempty"`
	MinimumAspectRatio  float64     `json:"minimum_aspect_ratio,omitempty"`
	MaximumAspectRatio  float64     `json:"maximum_aspect_ratio,omitempty"`
	MinimumDuration     float64     `json:"minimum_duration_seconds,omitempty"`
	MaximumDuration     float64     `json:"maximum_duration_seconds,omitempty"`
	AllowedVideoCodecs  []string    `json:"allowed_video_codecs,omitempty"`
	AllowedAudioCodecs  []string    `json:"allowed_audio_codecs,omitempty"`
	RequireAudio        bool        `json:"require_audio,omitempty"`
}

type ThreadRules struct {
	Allowed           bool `json:"allowed"`
	Required          bool `json:"required"`
	MinimumItems      int  `json:"minimum_items,omitempty"`
	MaximumItems      int  `json:"maximum_items,omitempty"`
	MaxItemCharacters int  `json:"max_item_characters,omitempty"`
	MaxMediaPerItem   int  `json:"max_media_per_item,omitempty"`
}

type FieldRules struct {
	Name      string   `json:"name"`
	Required  bool     `json:"required"`
	MaxLength int      `json:"max_length,omitempty"`
	Allowed   []string `json:"allowed_values,omitempty"`
}

type ContentCapability struct {
	ID                string       `json:"id"`
	Provider          string       `json:"provider"`
	ChannelType       ChannelType  `json:"channel_type"`
	Format            Format       `json:"format"`
	Available         bool         `json:"available"`
	UnavailableReason string       `json:"unavailable_reason,omitempty"`
	Text              TextRules    `json:"text"`
	Link              LinkRules    `json:"link"`
	Media             MediaRules   `json:"media"`
	Thread            ThreadRules  `json:"thread"`
	Fields            []FieldRules `json:"fields,omitempty"`
}

type CapabilityCatalog struct {
	Version      string              `json:"version"`
	Status       string              `json:"status"`
	Blocker      string              `json:"blocker,omitempty"`
	Capabilities []ContentCapability `json:"capabilities"`
}

func BlockedCapabilityCatalog() CapabilityCatalog {
	return CapabilityCatalog{
		Version:      "pending-d02-301",
		Status:       "blocked",
		Blocker:      "Provider/channel availability and limits require the versioned matrix from issue #301.",
		Capabilities: []ContentCapability{},
	}
}

func ParseCapabilityCatalog(raw string) (CapabilityCatalog, error) {
	if strings.TrimSpace(raw) == "" {
		return BlockedCapabilityCatalog(), nil
	}
	var catalog CapabilityCatalog
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return CapabilityCatalog{}, fmt.Errorf("decode F6 capability catalog: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return CapabilityCatalog{}, err
	}
	return cloneCatalog(catalog), nil
}

func (catalog CapabilityCatalog) Validate() error {
	if strings.TrimSpace(catalog.Version) == "" || strings.TrimSpace(catalog.Status) == "" {
		return errors.New("F6 capability catalog version and status are required")
	}
	seen := make(map[string]struct{}, len(catalog.Capabilities))
	for index, capability := range catalog.Capabilities {
		if strings.TrimSpace(capability.ID) == "" ||
			strings.TrimSpace(capability.Provider) == "" ||
			strings.TrimSpace(string(capability.ChannelType)) == "" ||
			strings.TrimSpace(string(capability.Format)) == "" {
			return fmt.Errorf("F6 capability %d identity is incomplete", index)
		}
		if _, duplicate := seen[capability.ID]; duplicate {
			return fmt.Errorf("duplicate F6 capability id %q", capability.ID)
		}
		seen[capability.ID] = struct{}{}
		if capability.Available && strings.TrimSpace(capability.UnavailableReason) != "" {
			return fmt.Errorf("available F6 capability %q has an unavailable reason", capability.ID)
		}
		if !capability.Available && strings.TrimSpace(capability.UnavailableReason) == "" {
			return fmt.Errorf("unavailable F6 capability %q requires a reason", capability.ID)
		}
		if err := validateCapabilityRanges(capability); err != nil {
			return fmt.Errorf("F6 capability %q: %w", capability.ID, err)
		}
	}
	return nil
}

func validateCapabilityRanges(capability ContentCapability) error {
	if capability.Text.MinCharacters < 0 ||
		capability.Text.MaxCharacters < capability.Text.MinCharacters {
		return errors.New("invalid text range")
	}
	if capability.Media.MinimumItems < 0 ||
		capability.Media.MaximumItems < capability.Media.MinimumItems {
		return errors.New("invalid media item range")
	}
	if capability.Thread.MinimumItems < 0 ||
		capability.Thread.MaximumItems < capability.Thread.MinimumItems {
		return errors.New("invalid thread item range")
	}
	for _, field := range capability.Fields {
		if strings.TrimSpace(field.Name) == "" || field.MaxLength < 0 {
			return errors.New("invalid destination field rule")
		}
	}
	return nil
}

func (catalog CapabilityCatalog) Resolve(id string) (ContentCapability, bool) {
	for _, capability := range catalog.Capabilities {
		if capability.ID == id {
			return capability, true
		}
	}
	return ContentCapability{}, false
}

func (catalog CapabilityCatalog) ResolveProviderFormat(
	provider string,
	format Format,
) (ContentCapability, bool, error) {
	var resolved ContentCapability
	found := false
	for _, capability := range catalog.Capabilities {
		if capability.Provider != provider || capability.Format != format {
			continue
		}
		if found {
			return ContentCapability{}, false, fmt.Errorf(
				"duplicate provider/format capability for %q and %q",
				provider,
				format,
			)
		}
		resolved = capability
		found = true
	}
	return resolved, found, nil
}

func (catalog CapabilityCatalog) ResolveProviderResourceFormat(
	provider, resourceType string,
	format Format,
) (ContentCapability, bool, error) {
	var resolved ContentCapability
	found := false
	for _, capability := range catalog.Capabilities {
		if capability.Provider != provider ||
			capability.Format != format ||
			!channelTypeMatchesResource(capability.ChannelType, resourceType) {
			continue
		}
		if found {
			return ContentCapability{}, false, fmt.Errorf(
				"duplicate provider/resource/format capability for %q, %q, and %q",
				provider,
				resourceType,
				format,
			)
		}
		resolved = capability
		found = true
	}
	return resolved, found, nil
}

func cloneCatalog(catalog CapabilityCatalog) CapabilityCatalog {
	copyOfCatalog := catalog
	copyOfCatalog.Capabilities = slices.Clone(catalog.Capabilities)
	for index := range copyOfCatalog.Capabilities {
		capability := &copyOfCatalog.Capabilities[index]
		capability.Media.AllowedKinds = slices.Clone(capability.Media.AllowedKinds)
		capability.Media.AllowedContentTypes = slices.Clone(capability.Media.AllowedContentTypes)
		capability.Media.AllowedVideoCodecs = slices.Clone(capability.Media.AllowedVideoCodecs)
		capability.Media.AllowedAudioCodecs = slices.Clone(capability.Media.AllowedAudioCodecs)
		capability.Fields = slices.Clone(capability.Fields)
		for fieldIndex := range capability.Fields {
			capability.Fields[fieldIndex].Allowed = slices.Clone(capability.Fields[fieldIndex].Allowed)
		}
	}
	return copyOfCatalog
}
