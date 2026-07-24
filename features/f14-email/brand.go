package email

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// Brand contains only resolved F1 values. F14 deliberately has no fallback
// palette or product name, so F1 remains the single source of brand truth.
type Brand struct {
	Name          string
	LogoURL       string
	Canvas        string
	Surface       string
	Text          string
	TextMuted     string
	Brand         string
	TextInverse   string
	Border        string
	Danger        string
	DangerSurface string
	FontFamily    string
}

type tokenValue struct {
	Value any `json:"$value"`
}

type f1Tokens struct {
	Color struct {
		Semantic struct {
			Light map[string]tokenValue `json:"light"`
		} `json:"semantic"`
	} `json:"color"`
	Font struct {
		Family struct {
			Sans tokenValue `json:"sans"`
		} `json:"family"`
	} `json:"font"`
}

// LoadBrandFromF1 resolves the email theme from F1's tokens.json. productName
// and logoURL must come from F1 runtime discovery/asset publication.
func LoadBrandFromF1(source io.Reader, productName, logoURL string) (Brand, error) {
	var tokens f1Tokens
	decoder := json.NewDecoder(source)
	if err := decoder.Decode(&tokens); err != nil {
		return Brand{}, fmt.Errorf("decode F1 brand tokens: %w", err)
	}

	color := func(name string) (string, error) {
		token, ok := tokens.Color.Semantic.Light[name]
		value, typeOK := token.Value.(string)
		if !ok || !typeOK || !validHexColor(value) {
			return "", fmt.Errorf("F1 semantic color %q is missing or invalid", name)
		}
		return value, nil
	}

	canvas, err := color("canvas")
	if err != nil {
		return Brand{}, err
	}
	surface, err := color("surface")
	if err != nil {
		return Brand{}, err
	}
	text, err := color("text")
	if err != nil {
		return Brand{}, err
	}
	textMuted, err := color("textMuted")
	if err != nil {
		return Brand{}, err
	}
	brandColor, err := color("brand")
	if err != nil {
		return Brand{}, err
	}
	textInverse, err := color("textInverse")
	if err != nil {
		return Brand{}, err
	}
	border, err := color("border")
	if err != nil {
		return Brand{}, err
	}
	danger, err := color("danger")
	if err != nil {
		return Brand{}, err
	}
	dangerSurface, err := color("dangerSurface")
	if err != nil {
		return Brand{}, err
	}

	fontValues, ok := tokens.Font.Family.Sans.Value.([]any)
	if !ok || len(fontValues) == 0 {
		return Brand{}, errors.New("F1 sans font family is missing or invalid")
	}
	fonts := make([]string, 0, len(fontValues))
	for _, raw := range fontValues {
		font, ok := raw.(string)
		if !ok || strings.ContainsAny(font, "{};") {
			return Brand{}, errors.New("F1 sans font family is missing or invalid")
		}
		fonts = append(fonts, quoteFont(font))
	}
	if strings.TrimSpace(productName) == "" {
		return Brand{}, errors.New("F1 product name is required")
	}
	parsedLogo, err := url.ParseRequestURI(logoURL)
	if err != nil || parsedLogo.Scheme != "https" || parsedLogo.Host == "" {
		return Brand{}, errors.New("published F1 logo URL must use HTTPS")
	}

	return Brand{
		Name:          productName,
		LogoURL:       logoURL,
		Canvas:        canvas,
		Surface:       surface,
		Text:          text,
		TextMuted:     textMuted,
		Brand:         brandColor,
		TextInverse:   textInverse,
		Border:        border,
		Danger:        danger,
		DangerSurface: dangerSurface,
		FontFamily:    strings.Join(fonts, ", "),
	}, nil
}

func validHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, character := range value[1:] {
		if character < '0' || character > '9' {
			lower := character | 0x20
			if lower < 'a' || lower > 'f' {
				return false
			}
		}
	}
	return true
}

func quoteFont(value string) string {
	if strings.ContainsAny(value, " \t") {
		return `"` + strings.ReplaceAll(value, `"`, "") + `"`
	}
	return value
}
