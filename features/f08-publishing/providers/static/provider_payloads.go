package staticproviders

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

type remoteItem struct {
	ID          string
	Text        string
	Title       string
	Description string
	Link        string
	Author      string
	Media       []media
	Permalink   string
}

func (adapter *Adapter) decodePayload(raw json.RawMessage) (payload, error) {
	var value payload
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) == nil {
		return payload{}, permanent("payload_invalid")
	}
	value.Format = strings.TrimSpace(value.Format)
	value.Text = strings.TrimSpace(value.Text)
	value.Title = strings.TrimSpace(value.Title)
	value.Description = strings.TrimSpace(value.Description)
	value.Link = strings.TrimSpace(value.Link)
	value.Author = strings.TrimSpace(value.Author)
	if adapter.provider == ProviderX {
		value.Author = strings.TrimPrefix(value.Author, "@")
	}
	value.BoardID = strings.TrimSpace(value.BoardID)
	value.Location = strings.TrimSpace(value.Location)
	value.LanguageCode = strings.TrimSpace(value.LanguageCode)
	value.TopicType = strings.ToUpper(strings.TrimSpace(value.TopicType))
	if adapter.provider == ProviderGoogleBusinessProfile {
		if location, ok := canonicalLocation(value.Location); ok {
			value.Location = location
		}
		if value.TopicType == "" {
			value.TopicType = "STANDARD"
		}
	}
	for index := range value.Media {
		value.Media[index].ID = strings.TrimSpace(value.Media[index].ID)
		value.Media[index].Ref = strings.TrimSpace(value.Media[index].Ref)
		value.Media[index].SourceURL = strings.TrimSpace(value.Media[index].SourceURL)
		value.Media[index].ContentType = strings.ToLower(strings.TrimSpace(
			value.Media[index].ContentType,
		))
		value.Media[index].SHA256 = strings.ToLower(strings.TrimSpace(
			value.Media[index].SHA256,
		))
		value.Media[index].AltText = strings.TrimSpace(value.Media[index].AltText)
	}
	if !adapter.validPayload(value) {
		return payload{}, permanent("payload_unsupported")
	}
	return value, nil
}

func (adapter *Adapter) validPayload(value payload) bool {
	switch adapter.provider {
	case ProviderX:
		if value.Format != "text" && value.Format != "image" && value.Format != "video" {
			return false
		}
		if value.Author != "" {
			if _, ok := cleanSegment(value.Author); !ok {
				return false
			}
		}
		if value.BoardID != "" || value.Location != "" {
			return false
		}
		if value.Text == "" && len(value.Media) == 0 {
			return false
		}
		for _, item := range value.Media {
			if !validUploadMedia(item) {
				return false
			}
		}
		if value.Format == "text" {
			return len(value.Media) == 0
		}
		if value.Format == "video" {
			return len(value.Media) == 1 &&
				strings.HasPrefix(value.Media[0].ContentType, "video/")
		}
		if len(value.Media) == 0 || len(value.Media) > 4 {
			return false
		}
		for _, item := range value.Media {
			if !strings.HasPrefix(item.ContentType, "image/") ||
				item.ContentType == "image/gif" {
				return false
			}
		}
		return true
	case ProviderLinkedIn:
		if value.Format != "text" && value.Format != "image" {
			return false
		}
		if value.Author != "" &&
			!strings.HasPrefix(value.Author, "urn:li:person:") &&
			!strings.HasPrefix(value.Author, "urn:li:organization:") {
			return false
		}
		if value.BoardID != "" || value.Location != "" {
			return false
		}
		if value.Text == "" && len(value.Media) == 0 {
			return false
		}
		for _, item := range value.Media {
			if !validUploadMedia(item) {
				return false
			}
		}
		if value.Format == "text" {
			return len(value.Media) == 0
		}
		return len(value.Media) == 1 &&
			strings.HasPrefix(value.Media[0].ContentType, "image/")
	case ProviderPinterest:
		if value.Format != "image" ||
			len(value.Media) != 1 || !validHTTPS(value.Media[0].SourceURL) {
			return false
		}
		if value.BoardID != "" {
			if _, ok := cleanSegment(value.BoardID); !ok {
				return false
			}
		}
		if value.Author != "" || value.Location != "" ||
			value.Media[0].ID != "" || value.Media[0].Ref != "" {
			return false
		}
		return value.Link == "" || validHTTPS(value.Link)
	case ProviderGoogleBusinessProfile:
		if value.Location != "" {
			if _, ok := canonicalLocation(value.Location); !ok {
				return false
			}
		}
		if (value.Format != "text" && value.Format != "image") ||
			value.Text == "" {
			return false
		}
		if value.Author != "" || value.BoardID != "" {
			return false
		}
		if value.TopicType != "STANDARD" || len(value.Media) > 1 {
			return false
		}
		if value.Format == "text" {
			return len(value.Media) == 0
		}
		return len(value.Media) == 1 && validHTTPS(value.Media[0].SourceURL) &&
			value.Media[0].ID == "" && value.Media[0].Ref == ""
	default:
		return false
	}
}

func validUploadMedia(item media) bool {
	if item.ID != "" || item.SourceURL != "" || item.Ref == "" ||
		item.Size <= 0 || len(item.SHA256) != 64 {
		return false
	}
	for _, character := range item.SHA256 {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	if _, ok := cleanSegment(item.Ref); !ok {
		return false
	}
	switch item.ContentType {
	case "image/jpeg", "image/png", "image/gif", "image/webp",
		"video/mp4", "video/webm", "video/quicktime":
		return true
	default:
		return false
	}
}

func (adapter *Adapter) createHeaders() http.Header {
	header := http.Header{"Content-Type": {"application/json"}}
	if adapter.provider == ProviderLinkedIn {
		header.Set("Linkedin-Version", adapter.linkedinVersion)
		header.Set("X-Restli-Protocol-Version", "2.0.0")
	}
	return header
}

func (adapter *Adapter) createPath(content payload) string {
	switch adapter.provider {
	case ProviderX:
		return "/2/tweets"
	case ProviderLinkedIn:
		return "/rest/posts"
	case ProviderPinterest:
		return "/v5/pins"
	case ProviderGoogleBusinessProfile:
		return "/v4/" + content.Location + "/localPosts"
	default:
		return "/"
	}
}

func (adapter *Adapter) listPath(content payload, cursor string) string {
	values := make(url.Values)
	switch adapter.provider {
	case ProviderX:
		author, _ := cleanSegment(content.Author)
		values = url.Values{
			"max_results":  {"100"},
			"tweet.fields": {"attachments"},
		}
		if cursor != "" {
			values.Set("pagination_token", cursor)
		}
		return queryPath("/2/users/"+author+"/tweets", values)
	case ProviderLinkedIn:
		values = url.Values{
			"q":      {"author"},
			"author": {content.Author},
			"count":  {"100"},
			"sortBy": {"CREATED"},
		}
		if cursor != "" {
			values.Set("start", cursor)
		}
		return queryPath("/rest/posts", values)
	case ProviderPinterest:
		board, _ := cleanSegment(content.BoardID)
		values.Set("page_size", "100")
		if cursor != "" {
			values.Set("bookmark", cursor)
		}
		return queryPath("/v5/boards/"+board+"/pins", values)
	case ProviderGoogleBusinessProfile:
		values.Set("pageSize", "100")
		if cursor != "" {
			values.Set("pageToken", cursor)
		}
		return queryPath("/v4/"+content.Location+"/localPosts", values)
	default:
		return "/"
	}
}

func (adapter *Adapter) createBody(content payload) []byte {
	switch adapter.provider {
	case ProviderX:
		ids := make([]string, 0, len(content.Media))
		for _, item := range content.Media {
			ids = append(ids, item.ID)
		}
		body := map[string]any{"text": content.Text}
		if len(ids) != 0 {
			body["media"] = map[string]any{"media_ids": ids}
		}
		return canonicalJSON(body)
	case ProviderLinkedIn:
		body := map[string]any{
			"author":     content.Author,
			"commentary": content.Text,
			"visibility": "PUBLIC",
			"distribution": map[string]any{
				"feedDistribution":               "MAIN_FEED",
				"targetEntities":                 []any{},
				"thirdPartyDistributionChannels": []any{},
			},
			"lifecycleState":            "PUBLISHED",
			"isReshareDisabledByAuthor": false,
		}
		if len(content.Media) == 1 {
			body["content"] = map[string]any{
				"media": map[string]any{
					"id":      content.Media[0].ID,
					"altText": content.Media[0].AltText,
				},
			}
		}
		return canonicalJSON(body)
	case ProviderPinterest:
		body := map[string]any{
			"board_id":    content.BoardID,
			"title":       content.Title,
			"description": content.Description,
			"media_source": map[string]any{
				"source_type": "image_url",
				"url":         content.Media[0].SourceURL,
			},
		}
		if content.Link != "" {
			body["link"] = content.Link
		}
		return canonicalJSON(body)
	case ProviderGoogleBusinessProfile:
		body := map[string]any{
			"languageCode": content.LanguageCode,
			"summary":      content.Text,
			"topicType":    "STANDARD",
		}
		if len(content.Media) == 1 {
			body["media"] = []any{map[string]any{
				"mediaFormat": "PHOTO",
				"sourceUrl":   content.Media[0].SourceURL,
			}}
		}
		return canonicalJSON(body)
	default:
		return nil
	}
}

func (adapter *Adapter) list(
	ctx context.Context,
	request publishing.PublishRequest,
	content payload,
) ([]remoteItem, error) {
	const maximumPages = 100
	items := make([]remoteItem, 0)
	cursor := ""
	seen := make(map[string]struct{})
	for page := 0; page < maximumPages; page++ {
		headers := adapter.createHeaders()
		if adapter.provider == ProviderLinkedIn {
			headers.Set("X-RestLi-Method", "FINDER")
		}
		response, err := adapter.execute(
			ctx, request, http.MethodGet, adapter.listPath(content, cursor),
			headers, nil,
		)
		if err != nil {
			return nil, err
		}
		pageItems, next, err := adapter.decodeListPage(response.Body)
		if err != nil {
			return nil, err
		}
		items = append(items, pageItems...)
		if next == "" {
			return items, nil
		}
		if _, duplicate := seen[next]; duplicate {
			return nil, permanent("provider_pagination_invalid")
		}
		seen[next] = struct{}{}
		cursor = next
	}
	return nil, &publishing.ProviderError{
		Code: "provider_pagination_incomplete", Retryable: true,
	}
}

func (adapter *Adapter) decodeList(body []byte) ([]remoteItem, error) {
	items, _, err := adapter.decodeListPage(body)
	return items, err
}

func (adapter *Adapter) decodeListPage(
	body []byte,
) ([]remoteItem, string, error) {
	switch adapter.provider {
	case ProviderX:
		var response struct {
			Data []struct {
				ID          string `json:"id"`
				Text        string `json:"text"`
				Attachments struct {
					MediaKeys []string `json:"media_keys"`
				} `json:"attachments"`
			} `json:"data"`
			Meta struct {
				NextToken string `json:"next_token"`
			} `json:"meta"`
		}
		if strictJSON(body, &response) != nil {
			return nil, "", permanent("provider_response_invalid")
		}
		items := make([]remoteItem, 0, len(response.Data))
		for _, value := range response.Data {
			items = append(items, remoteItem{ID: value.ID, Text: value.Text, Permalink: permalinkFor(adapter.provider, value.ID)})
		}
		items, err := validRemoteItems(items)
		return items, strings.TrimSpace(response.Meta.NextToken), err
	case ProviderLinkedIn:
		var response struct {
			Elements []struct {
				ID         string `json:"id"`
				Author     string `json:"author"`
				Commentary string `json:"commentary"`
			} `json:"elements"`
			Paging struct {
				Start int `json:"start"`
				Count int `json:"count"`
				Links []struct {
					Rel  string `json:"rel"`
					Href string `json:"href"`
				} `json:"links"`
			} `json:"paging"`
		}
		if strictJSON(body, &response) != nil {
			return nil, "", permanent("provider_response_invalid")
		}
		items := make([]remoteItem, 0, len(response.Elements))
		for _, value := range response.Elements {
			items = append(items, remoteItem{ID: value.ID, Author: value.Author, Text: value.Commentary, Permalink: permalinkFor(adapter.provider, value.ID)})
		}
		items, err := validRemoteItems(items)
		if err != nil {
			return nil, "", err
		}
		next := ""
		for _, link := range response.Paging.Links {
			if strings.EqualFold(link.Rel, "next") {
				target, parseErr := url.Parse(link.Href)
				if parseErr != nil {
					return nil, "", permanent("provider_pagination_invalid")
				}
				next = target.Query().Get("start")
				break
			}
		}
		if next == "" && response.Paging.Count > 0 &&
			len(response.Elements) >= response.Paging.Count {
			next = strconv.Itoa(response.Paging.Start + len(response.Elements))
		}
		return items, next, nil
	case ProviderPinterest:
		var response struct {
			Items []struct {
				ID          string `json:"id"`
				Title       string `json:"title"`
				Description string `json:"description"`
				Link        string `json:"link"`
				Media       struct {
					Images map[string]struct {
						URL string `json:"url"`
					} `json:"images"`
				} `json:"media"`
			} `json:"items"`
			Bookmark string `json:"bookmark"`
		}
		if strictJSON(body, &response) != nil {
			return nil, "", permanent("provider_response_invalid")
		}
		items := make([]remoteItem, 0, len(response.Items))
		for _, value := range response.Items {
			source := ""
			keys := make([]string, 0, len(value.Media.Images))
			for key := range value.Media.Images {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			if len(keys) != 0 {
				source = value.Media.Images[keys[0]].URL
			}
			items = append(items, remoteItem{
				ID: value.ID, Title: value.Title, Description: value.Description,
				Link: value.Link, Media: []media{{SourceURL: source}},
				Permalink: permalinkFor(adapter.provider, value.ID),
			})
		}
		items, err := validRemoteItems(items)
		return items, strings.TrimSpace(response.Bookmark), err
	case ProviderGoogleBusinessProfile:
		var response struct {
			LocalPosts []struct {
				Name      string `json:"name"`
				Summary   string `json:"summary"`
				SearchURL string `json:"searchUrl"`
				Media     []struct {
					SourceURL string `json:"sourceUrl"`
				} `json:"media"`
			} `json:"localPosts"`
			NextPageToken string `json:"nextPageToken"`
		}
		if strictJSON(body, &response) != nil {
			return nil, "", permanent("provider_response_invalid")
		}
		items := make([]remoteItem, 0, len(response.LocalPosts))
		for _, value := range response.LocalPosts {
			mediaItems := make([]media, 0, len(value.Media))
			for _, remoteMedia := range value.Media {
				mediaItems = append(mediaItems, media{SourceURL: remoteMedia.SourceURL})
			}
			items = append(items, remoteItem{
				ID: value.Name, Text: value.Summary, Media: mediaItems,
				Permalink: value.SearchURL,
			})
		}
		items, err := validRemoteItems(items)
		return items, strings.TrimSpace(response.NextPageToken), err
	default:
		return nil, "", permanent("provider_response_invalid")
	}
}

func (adapter *Adapter) createdResult(
	response socialconnections.PublishingResponse,
	content payload,
) (string, string, error) {
	switch adapter.provider {
	case ProviderX:
		var value struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if strictJSON(response.Body, &value) != nil ||
			!validRemoteID(adapter.provider, value.Data.ID) {
			return "", "", permanent("provider_response_invalid")
		}
		return value.Data.ID, permalinkFor(adapter.provider, value.Data.ID), nil
	case ProviderLinkedIn:
		var value struct {
			ID string `json:"id"`
		}
		if strictJSON(response.Body, &value) != nil ||
			!validRemoteID(adapter.provider, value.ID) {
			return "", "", permanent("provider_response_invalid")
		}
		return value.ID, permalinkFor(adapter.provider, value.ID), nil
	case ProviderPinterest:
		var value struct {
			ID string `json:"id"`
		}
		if strictJSON(response.Body, &value) != nil ||
			!validRemoteID(adapter.provider, value.ID) {
			return "", "", permanent("provider_response_invalid")
		}
		return value.ID, permalinkFor(adapter.provider, value.ID), nil
	case ProviderGoogleBusinessProfile:
		var value struct {
			Name      string `json:"name"`
			SearchURL string `json:"searchUrl"`
		}
		if strictJSON(response.Body, &value) != nil ||
			!validRemoteID(adapter.provider, value.Name) ||
			!validHTTPS(value.SearchURL) {
			return "", "", permanent("provider_response_invalid")
		}
		return value.Name, value.SearchURL, nil
	default:
		return "", "", permanent("provider_response_invalid")
	}
}

func (adapter *Adapter) matches(content payload, item remoteItem) bool {
	switch adapter.provider {
	case ProviderX:
		return item.Text == content.Text
	case ProviderLinkedIn:
		return item.Author == content.Author && item.Text == content.Text
	case ProviderPinterest:
		return item.Title == content.Title &&
			item.Description == content.Description &&
			item.Link == content.Link && len(item.Media) == 1 &&
			item.Media[0].SourceURL == content.Media[0].SourceURL
	case ProviderGoogleBusinessProfile:
		if item.Text != content.Text || len(item.Media) != len(content.Media) {
			return false
		}
		return len(content.Media) == 0 ||
			item.Media[0].SourceURL == content.Media[0].SourceURL
	default:
		return false
	}
}

func strictJSON(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(new(any)) == nil {
		return publishing.ErrInvalidArgument
	}
	return nil
}

func validRemoteItems(items []remoteItem) ([]remoteItem, error) {
	for _, item := range items {
		if !validRemoteID(providerForPermalink(item.Permalink, item.ID), item.ID) ||
			(item.Permalink != "" && !validHTTPS(item.Permalink)) {
			return nil, permanent("provider_response_invalid")
		}
	}
	return items, nil
}

func providerForPermalink(permalink, id string) string {
	switch {
	case strings.HasPrefix(permalink, "https://x.com/"):
		return ProviderX
	case strings.HasPrefix(permalink, "https://www.linkedin.com/"):
		return ProviderLinkedIn
	case strings.HasPrefix(permalink, "https://www.pinterest.com/"):
		return ProviderPinterest
	case strings.Contains(id, "/localPosts/"):
		return ProviderGoogleBusinessProfile
	default:
		return ""
	}
}
