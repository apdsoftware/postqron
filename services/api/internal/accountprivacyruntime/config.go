package accountprivacyruntime

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

func privacyAllowedOrigins(
	raw, fallback string,
	production bool,
) (map[string]struct{}, error) {
	if strings.TrimSpace(raw) == "" {
		if production {
			return nil, errors.New("POSTQRON_PRIVACY_ALLOWED_ORIGINS is required in production")
		}
		raw = fallback
	}
	origins := make(map[string]struct{})
	for _, value := range strings.Split(raw, ",") {
		normalized, err := normalizePrivacyOrigin(value)
		if err != nil {
			return nil, errors.New("POSTQRON_PRIVACY_ALLOWED_ORIGINS must contain absolute HTTP(S) origins")
		}
		if production && !strings.HasPrefix(normalized, "https://") {
			return nil, errors.New("POSTQRON_PRIVACY_ALLOWED_ORIGINS must use HTTPS in production")
		}
		origins[normalized] = struct{}{}
	}
	return origins, nil
}

func normalizePrivacyOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("invalid origin")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validateDownloadBaseURL(raw string, production bool) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return "", errors.New("POSTQRON_PRIVACY_DOWNLOAD_BASE_URL must be absolute")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("POSTQRON_PRIVACY_DOWNLOAD_BASE_URL must be an origin")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
	case "http":
		if production || !loopbackHost(parsed.Hostname()) {
			return "", errors.New("privacy download HTTP is allowed only on loopback outside production")
		}
	default:
		return "", errors.New("privacy download URL must use HTTPS")
	}
	if production && parsed.Scheme != "https" {
		return "", errors.New("privacy download URL must use HTTPS in production")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
