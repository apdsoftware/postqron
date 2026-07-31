package publishingruntime

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	staticproviders "github.com/apdsoftware/postqron/features/f08-publishing/providers/static"
)

var errOriginNotPinned = errors.New(
	"authenticated request origin is not pinned",
)

var staticResourceServers = map[socialconnections.Provider]struct {
	environment string
	official    string
	gate        string
}{
	socialconnections.ProviderX: {
		environment: "POSTQRON_F05_X_RESOURCE_SERVER",
		official:    "https://api.x.com",
		gate:        "X",
	},
	socialconnections.ProviderLinkedIn: {
		environment: "POSTQRON_F05_LINKEDIN_RESOURCE_SERVER",
		official:    "https://api.linkedin.com",
		gate:        "LINKEDIN",
	},
	socialconnections.ProviderPinterest: {
		environment: "POSTQRON_F05_PINTEREST_RESOURCE_SERVER",
		official:    "https://api.pinterest.com",
		gate:        "PINTEREST",
	},
	socialconnections.ProviderGoogleBusinessProfile: {
		environment: "POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_RESOURCE_SERVER",
		official:    "https://mybusiness.googleapis.com",
		gate:        "GOOGLE_BUSINESS_PROFILE",
	},
}

var staticProviderOrder = []socialconnections.Provider{
	socialconnections.ProviderX,
	socialconnections.ProviderLinkedIn,
	socialconnections.ProviderPinterest,
	socialconnections.ProviderGoogleBusinessProfile,
}

// NewF5AuthenticatedExecutor builds the real credential boundary used by the
// worker. With no ready F8 provider gate it returns nil so publishing remains
// unavailable. A ready gate requires complete F5 cipher, resource-server, and
// DNS-pinning configuration; partial configuration fails startup.
func NewF5AuthenticatedExecutor(
	database *sql.DB,
	clock func() time.Time,
) (*socialconnections.AuthenticatedExecutor, error) {
	active := make(map[socialconnections.Provider]string)
	for _, provider := range staticProviderOrder {
		configured := staticResourceServers[provider]
		if !gateReady(staticProviderGate(configured.gate)) {
			continue
		}
		resourceServer := strings.TrimSpace(os.Getenv(configured.environment))
		if resourceServer != configured.official {
			return nil, fmt.Errorf(
				"configure F5 publishing boundary: %s must equal %s",
				configured.environment,
				configured.official,
			)
		}
		active[provider] = resourceServer
	}
	if len(active) == 0 {
		return nil, nil
	}
	if database == nil {
		return nil, errors.New(
			"configure F5 publishing boundary: database is required",
		)
	}
	if os.Getenv("POSTQRON_F05_ENABLED") != "true" {
		return nil, errors.New(
			"configure F5 publishing boundary: POSTQRON_F05_ENABLED must be true",
		)
	}
	keyID := strings.TrimSpace(os.Getenv("POSTQRON_F05_CIPHER_KEY_ID"))
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(
		os.Getenv("POSTQRON_F05_CIPHER_KEY_BASE64"),
	))
	if err != nil || keyID == "" || len(key) != 32 {
		return nil, errors.New(
			"configure F5 publishing boundary: credential cipher is invalid",
		)
	}
	cipher, err := socialconnections.NewAESGCMCipher(keyID, key)
	if err != nil {
		return nil, fmt.Errorf("configure F5 publishing cipher: %w", err)
	}
	repository, err := socialconnections.NewPostgresRepository(database)
	if err != nil {
		return nil, fmt.Errorf("configure F5 publishing repository: %w", err)
	}
	if clock == nil {
		clock = time.Now
	}
	quota, err := socialconnections.NewPostgresChannelQuota(database, clock)
	if err != nil {
		return nil, fmt.Errorf("configure F5 publishing quota: %w", err)
	}
	adapters := make(map[socialconnections.Provider]socialconnections.Adapter)
	availability := make(
		map[socialconnections.Provider]socialconnections.ProviderAvailability,
	)
	for provider, resourceServer := range active {
		adapters[provider] = publishingCredentialAdapter{
			provider: provider,
			origin:   resourceServer,
		}
		availability[provider] = socialconnections.ProviderAvailability{
			Provider:           provider,
			Status:             socialconnections.ProviderAvailable,
			ConfigurationState: socialconnections.ProviderReady,
		}
	}
	service, err := socialconnections.NewService(socialconnections.Config{
		Repository:   repository,
		Authorizer:   denyPublishingAuthorizer{},
		Cipher:       cipher,
		Quota:        quota,
		Adapters:     adapters,
		Availability: availability,
		Now:          clock,
	})
	if err != nil {
		return nil, fmt.Errorf("configure F5 publishing service: %w", err)
	}
	transport := newDNSPinnedTransport(net.DefaultResolver)
	executor, err := socialconnections.NewAuthenticatedExecutor(
		socialconnections.AuthenticatedExecutorConfig{
			Service:         service,
			Transport:       transport,
			ResourceServers: active,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("configure F5 authenticated executor: %w", err)
	}
	return executor, nil
}

func gateReady(gate staticproviders.Gate) bool {
	return gate.Enabled && gate.ReviewApproved &&
		gate.AuditVerified && gate.QuotaConfigured
}

type denyPublishingAuthorizer struct{}

func (denyPublishingAuthorizer) Authorize(
	context.Context,
	string,
	string,
	socialconnections.Permission,
) error {
	return socialconnections.ErrUnauthorized
}

// publishingCredentialAdapter does not implement OAuth lifecycle operations:
// those remain owned by the F5 API runtime. It makes already-connected,
// non-expiring credentials available to AuthenticatedExecutor. A credential
// requiring refresh is failed closed and transitioned to reconnect by F5.
type publishingCredentialAdapter struct {
	provider socialconnections.Provider
	origin   string
}

func (adapter publishingCredentialAdapter) Config() socialconnections.OAuthConfig {
	return socialconnections.OAuthConfig{
		ClientID:         "worker-publishing-boundary",
		AuthorizationURL: adapter.origin + "/oauth/unsupported",
		RedirectURL:      "https://worker.invalid/oauth/unsupported",
		Scopes:           []string{"worker-publishing-boundary"},
	}
}

func (publishingCredentialAdapter) Exchange(
	context.Context,
	socialconnections.ExchangeRequest,
) (socialconnections.Credential, error) {
	return socialconnections.Credential{}, socialconnections.ErrProviderUnavailable
}

func (publishingCredentialAdapter) Discover(
	context.Context,
	socialconnections.Credential,
) ([]socialconnections.DiscoveredResource, error) {
	return nil, socialconnections.ErrProviderUnavailable
}

func (publishingCredentialAdapter) Refresh(
	context.Context,
	socialconnections.Credential,
) (socialconnections.Credential, error) {
	return socialconnections.Credential{}, &socialconnections.ProviderFailure{
		Kind: socialconnections.FailureAuthentication,
		Code: "publishing_credential_refresh_unavailable",
	}
}

func (publishingCredentialAdapter) Verify(
	context.Context,
	string,
	socialconnections.Credential,
) error {
	return socialconnections.ErrProviderUnavailable
}

func (publishingCredentialAdapter) Revoke(
	context.Context,
	string,
	socialconnections.Credential,
) error {
	return socialconnections.ErrProviderUnavailable
}

type networkResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type dnsPinnedTransport struct {
	resolver    networkResolver
	dialContext func(context.Context, string, string) (net.Conn, error)
	mutex       sync.RWMutex
	pins        map[string][]netip.Addr
}

func newDNSPinnedTransport(resolver networkResolver) *dnsPinnedTransport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: -1,
	}
	return &dnsPinnedTransport{
		resolver:    resolver,
		dialContext: dialer.DialContext,
		pins:        make(map[string][]netip.Addr),
	}
}

func (transport *dnsPinnedTransport) PinOrigin(
	ctx context.Context,
	rawOrigin string,
) error {
	target, err := url.Parse(rawOrigin)
	if err != nil || target.Scheme != "https" || target.Host == "" ||
		target.User != nil || target.RawQuery != "" || target.Fragment != "" ||
		target.Path != "" {
		return errors.New("resource server origin is invalid")
	}
	addresses, err := transport.resolver.LookupNetIP(
		ctx,
		"ip",
		target.Hostname(),
	)
	if err != nil || len(addresses) == 0 {
		return errors.New("resource server DNS lookup failed")
	}
	pinned := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !publicPublishingAddress(address) {
			return errors.New("resource server resolved to a non-public address")
		}
		pinned = append(pinned, address)
	}
	transport.mutex.Lock()
	transport.pins[rawOrigin] = pinned
	transport.mutex.Unlock()
	return nil
}

func (transport *dnsPinnedTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	if request == nil || request.URL == nil || request.URL.Scheme != "https" ||
		request.URL.User != nil || request.URL.Fragment != "" ||
		(request.Host != "" && request.Host != request.URL.Host) {
		return nil, errors.New("authenticated request target is invalid")
	}
	origin := "https://" + request.URL.Host
	transport.mutex.RLock()
	addresses := append([]netip.Addr(nil), transport.pins[origin]...)
	transport.mutex.RUnlock()
	if len(addresses) == 0 {
		return nil, errOriginNotPinned
	}
	expectedHost := request.URL.Hostname()
	expectedPort := request.URL.Port()
	if expectedPort == "" {
		expectedPort = "443"
	}
	base := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		DialContext: func(
			ctx context.Context,
			network string,
			address string,
		) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil || !strings.EqualFold(host, expectedHost) ||
				port != expectedPort {
				return nil, errors.New("authenticated request dial target changed")
			}
			var lastErr error
			for _, pinned := range addresses {
				connection, dialErr := transport.dialContext(
					ctx,
					network,
					net.JoinHostPort(pinned.String(), port),
				)
				if dialErr == nil {
					return connection, nil
				}
				lastErr = dialErr
			}
			return nil, lastErr
		},
	}
	return base.RoundTrip(request)
}

func publicPublishingAddress(address netip.Addr) bool {
	if !address.IsValid() || !address.IsGlobalUnicast() ||
		address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicPublishingPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

var nonPublicPublishingPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}
