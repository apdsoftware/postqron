package accountprivacyruntime

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

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
