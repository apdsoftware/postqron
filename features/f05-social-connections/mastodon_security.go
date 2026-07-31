package socialconnections

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	decentralizedResponseLimit  = 1 << 20
	decentralizedRequestTimeout = 10 * time.Second
)

var errUnsafeProviderURL = errors.New("unsafe decentralized provider URL")

type mastodonResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type mastodonDialer func(context.Context, string, string) (net.Conn, error)

type mastodonSafeHTTP struct {
	resolver  mastodonResolver
	dialer    mastodonDialer
	transport *http.Transport
	timeout   time.Duration
}

func newMastodonSafeHTTP() *mastodonSafeHTTP {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &mastodonSafeHTTP{
		resolver:  net.DefaultResolver,
		dialer:    dialer.DialContext,
		transport: http.DefaultTransport.(*http.Transport),
		timeout:   decentralizedRequestTimeout,
	}
}

func (client *mastodonSafeHTTP) do(
	ctx context.Context,
	request *http.Request,
) (*http.Response, error) {
	if client == nil || client.resolver == nil || client.dialer == nil {
		return nil, fmt.Errorf("%w: HTTP dependencies are missing", errUnsafeProviderURL)
	}
	parsed, addresses, err := client.validate(ctx, request.URL)
	if err != nil {
		return nil, err
	}
	request = request.Clone(ctx)
	request.URL = parsed
	request.Host = parsed.Host
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	baseTransport := client.transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport.(*http.Transport)
	}
	transport := baseTransport.Clone()
	transport.Proxy = nil
	transport.DialContext = func(
		dialContext context.Context,
		network, _ string,
	) (net.Conn, error) {
		var lastErr error
		for _, address := range addresses {
			target := net.JoinHostPort(address.String(), port)
			connection, dialErr := client.dialer(dialContext, network, target)
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
	}
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   client.timeout,
		CheckRedirect: func(
			_ *http.Request,
			_ []*http.Request,
		) error {
			// Dynamic provider redirects are never followed. This avoids
			// cross-origin token forwarding and forces every accepted endpoint
			// to be the exact URL that was DNS/IP validated and pinned.
			return http.ErrUseLastResponse
		},
	}
	return httpClient.Do(request)
}

func (client *mastodonSafeHTTP) validate(
	ctx context.Context,
	target *url.URL,
) (*url.URL, []netip.Addr, error) {
	if target == nil ||
		target.Scheme != "https" ||
		target.Host == "" ||
		target.User != nil ||
		target.Fragment != "" {
		return nil, nil, fmt.Errorf("%w: HTTPS URL required", errUnsafeProviderURL)
	}
	if target.Port() != "" {
		port, err := strconv.Atoi(target.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, nil, fmt.Errorf("%w: invalid port", errUnsafeProviderURL)
		}
	}
	host := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	if host == "" || host == "localhost" {
		return nil, nil, fmt.Errorf("%w: invalid host", errUnsafeProviderURL)
	}
	addresses, err := client.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, nil, fmt.Errorf("%w: DNS lookup failed", errUnsafeProviderURL)
	}
	safe := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !mastodonPublicAddress(address) {
			return nil, nil, fmt.Errorf(
				"%w: host resolves to blocked address",
				errUnsafeProviderURL,
			)
		}
		safe = append(safe, address)
	}
	clone := *target
	clone.Host = target.Host
	return &clone, safe, nil
}

var mastodonBlockedPrefixes = mustMastodonPrefixes(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"::/128",
	"::1/128",
	"64:ff9b:1::/48",
	"100::/64",
	"2001:2::/48",
	"2001:db8::/32",
	"2001:10::/28",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
)

func mastodonPublicAddress(address netip.Addr) bool {
	if !address.IsValid() ||
		!address.IsGlobalUnicast() ||
		address.IsPrivate() ||
		address.IsLoopback() ||
		address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() ||
		address.IsMulticast() ||
		address.IsUnspecified() {
		return false
	}
	for _, prefix := range mastodonBlockedPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func mustMastodonPrefixes(values ...string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefixes = append(prefixes, netip.MustParsePrefix(value))
	}
	return prefixes
}

func mastodonOrigin(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.Host == "" ||
		parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return nil, fmt.Errorf("%w: provider origin must be an HTTPS origin", errUnsafeProviderURL)
	}
	parsed.Path = ""
	return parsed, nil
}

func mastodonEndpoint(origin *url.URL, path string) *url.URL {
	clone := *origin
	clone.Path = path
	clone.RawPath = ""
	clone.RawQuery = ""
	clone.Fragment = ""
	return &clone
}

func mastodonReadLimited(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, decentralizedResponseLimit+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(body) > decentralizedResponseLimit {
		return nil, errors.New("decentralized provider response exceeds limit")
	}
	return body, nil
}
