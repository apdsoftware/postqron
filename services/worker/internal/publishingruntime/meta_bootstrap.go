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
	"os"
	"strings"
	"sync"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	metapublishing "github.com/apdsoftware/postqron/features/f08-publishing/providers/meta"
)

type metaPinnedTransport interface {
	http.RoundTripper
	PinOrigin(context.Context, string) error
}

var newMetaRepository = func(
	database *sql.DB,
) (socialconnections.Repository, error) {
	return socialconnections.NewPostgresRepository(database)
}

var newMetaQuota = func(
	database *sql.DB,
	clock func() time.Time,
) (socialconnections.ChannelQuota, error) {
	return socialconnections.NewPostgresChannelQuota(database, clock)
}

var newMetaTransport = func() metaPinnedTransport {
	return newPinnedMetaTransport()
}

var newMetaAuthenticatedExecutor = socialconnections.NewAuthenticatedExecutor

const (
	metaFacebookOrigin  = "https://graph.facebook.com"
	metaInstagramOrigin = "https://graph.instagram.com"
	metaThreadsOrigin   = "https://graph.threads.net"
)

func productionMetaAutoConfig(
	database *sql.DB,
	clock func() time.Time,
) (metapublishing.RegistrationConfig, error) {
	if exactTrue("POSTQRON_F08_META_AUTO_ENABLED") == false {
		return metapublishing.RegistrationConfig{}, nil
	}
	if !exactTrue("POSTQRON_F05_ENABLED") ||
		!exactTrue("POSTQRON_F05_META_ENABLED") {
		return metapublishing.RegistrationConfig{}, errors.New(
			"Meta publishing requires the F5 credential and Meta gates",
		)
	}
	key, err := base64.StdEncoding.DecodeString(
		strings.TrimSpace(os.Getenv("POSTQRON_F05_CIPHER_KEY_BASE64")),
	)
	if err != nil || len(key) != 32 {
		return metapublishing.RegistrationConfig{}, errors.New(
			"Meta publishing requires a valid F5 AES-256 key",
		)
	}
	keyID := strings.TrimSpace(os.Getenv("POSTQRON_F05_CIPHER_KEY_ID"))
	cipher, err := socialconnections.NewAESGCMCipher(keyID, key)
	if err != nil {
		return metapublishing.RegistrationConfig{}, err
	}
	repository, err := newMetaRepository(database)
	if err != nil {
		return metapublishing.RegistrationConfig{}, err
	}
	quota, err := newMetaQuota(database, clock)
	if err != nil {
		return metapublishing.RegistrationConfig{}, err
	}
	graphVersion := strings.TrimSpace(os.Getenv("POSTQRON_F05_META_GRAPH_VERSION"))
	adapters := make(map[socialconnections.Provider]socialconnections.Adapter)
	origins := make(map[socialconnections.Provider]string)
	var providers []socialconnections.Provider
	if exactTrue("POSTQRON_F08_FACEBOOK_PAGES_ENABLED") {
		if !exactTrue("POSTQRON_F05_FACEBOOK_APP_REVIEW_APPROVED") ||
			!exactTrue("POSTQRON_F05_FACEBOOK_RUNTIME_AUDIT_VERIFIED") {
			return metapublishing.RegistrationConfig{}, errors.New(
				"Facebook Pages publishing requires F5 review and audit gates",
			)
		}
		adapter, adapterErr := socialconnections.NewMetaAdapter(
			socialconnections.MetaAdapterConfig{
				Provider: socialconnections.ProviderFacebookPages,
				ClientID: strings.TrimSpace(
					os.Getenv("POSTQRON_F05_FACEBOOK_CLIENT_ID"),
				),
				ClientSecret: strings.TrimSpace(
					os.Getenv("POSTQRON_F05_FACEBOOK_CLIENT_SECRET"),
				),
				RedirectURL: strings.TrimSpace(
					os.Getenv("POSTQRON_F05_FACEBOOK_REDIRECT_URL"),
				),
				GraphVersion: graphVersion,
				FacebookLoginConfigID: strings.TrimSpace(
					os.Getenv("POSTQRON_F05_FACEBOOK_LOGIN_CONFIG_ID"),
				),
				SupportsPKCE: true,
			},
		)
		if adapterErr != nil {
			return metapublishing.RegistrationConfig{}, adapterErr
		}
		adapters[socialconnections.ProviderFacebookPages] = adapter
		origins[socialconnections.ProviderFacebookPages] = metaFacebookOrigin
		providers = append(providers, socialconnections.ProviderFacebookPages)
	}
	if exactTrue("POSTQRON_F08_INSTAGRAM_PROFESSIONAL_ENABLED") {
		if !exactTrue("POSTQRON_F05_INSTAGRAM_APP_REVIEW_APPROVED") ||
			!exactTrue("POSTQRON_F05_INSTAGRAM_RUNTIME_AUDIT_VERIFIED") {
			return metapublishing.RegistrationConfig{}, errors.New(
				"Instagram publishing requires F5 review and audit gates",
			)
		}
		adapter, adapterErr := socialconnections.NewMetaAdapter(
			socialconnections.MetaAdapterConfig{
				Provider: socialconnections.ProviderInstagramProfessional,
				ClientID: strings.TrimSpace(
					os.Getenv("POSTQRON_F05_INSTAGRAM_CLIENT_ID"),
				),
				ClientSecret: strings.TrimSpace(
					os.Getenv("POSTQRON_F05_INSTAGRAM_CLIENT_SECRET"),
				),
				RedirectURL: strings.TrimSpace(
					os.Getenv("POSTQRON_F05_INSTAGRAM_REDIRECT_URL"),
				),
				GraphVersion: graphVersion,
				SupportsPKCE: true,
			},
		)
		if adapterErr != nil {
			return metapublishing.RegistrationConfig{}, adapterErr
		}
		adapters[socialconnections.ProviderInstagramProfessional] = adapter
		origins[socialconnections.ProviderInstagramProfessional] = metaInstagramOrigin
		providers = append(
			providers,
			socialconnections.ProviderInstagramProfessional,
		)
	}
	if exactTrue("POSTQRON_F08_THREADS_ENABLED") {
		if !allPresent(
			map[string]string{
				"POSTQRON_F05_THREADS_CLIENT_ID": strings.TrimSpace(
					os.Getenv("POSTQRON_F05_THREADS_CLIENT_ID"),
				),
				"POSTQRON_F05_THREADS_CLIENT_SECRET": strings.TrimSpace(
					os.Getenv("POSTQRON_F05_THREADS_CLIENT_SECRET"),
				),
				"POSTQRON_F05_THREADS_REDIRECT_URL": strings.TrimSpace(
					os.Getenv("POSTQRON_F05_THREADS_REDIRECT_URL"),
				),
			},
			"POSTQRON_F05_THREADS_CLIENT_ID",
			"POSTQRON_F05_THREADS_CLIENT_SECRET",
			"POSTQRON_F05_THREADS_REDIRECT_URL",
		) || !exactTrue("POSTQRON_F05_THREADS_ENABLED") {
			return metapublishing.RegistrationConfig{}, errors.New(
				"Threads publishing requires the verified F5 adapter configuration",
			)
		}
		if !exactTrue("POSTQRON_F05_THREADS_APP_REVIEW_APPROVED") ||
			!exactTrue("POSTQRON_F05_THREADS_RUNTIME_AUDIT_VERIFIED") {
			return metapublishing.RegistrationConfig{}, errors.New(
				"Threads publishing requires F5 review and audit gates",
			)
		}
		adapter, adapterErr := socialconnections.NewThreadsAdapter(
			socialconnections.ThreadsAdapterConfig{
				ClientID: strings.TrimSpace(
					os.Getenv("POSTQRON_F05_THREADS_CLIENT_ID"),
				),
				ClientSecret: strings.TrimSpace(
					os.Getenv("POSTQRON_F05_THREADS_CLIENT_SECRET"),
				),
				RedirectURL: strings.TrimSpace(
					os.Getenv("POSTQRON_F05_THREADS_REDIRECT_URL"),
				),
			},
		)
		if adapterErr != nil {
			return metapublishing.RegistrationConfig{}, adapterErr
		}
		adapters[socialconnections.ProviderThreads] = adapter
		origins[socialconnections.ProviderThreads] = metaThreadsOrigin
		providers = append(providers, socialconnections.ProviderThreads)
	}
	if len(providers) == 0 {
		return metapublishing.RegistrationConfig{}, errors.New(
			"Meta auto publishing gate enabled without an approved provider",
		)
	}
	service, err := socialconnections.NewService(socialconnections.Config{
		Repository: repository,
		Authorizer: denySocialMutation{},
		Cipher:     cipher,
		Quota:      quota,
		Adapters:   adapters,
		Now:        clock,
	})
	if err != nil {
		return metapublishing.RegistrationConfig{}, err
	}
	transport := newMetaTransport()
	executor, err := newMetaAuthenticatedExecutor(
		socialconnections.AuthenticatedExecutorConfig{
			Service:         service,
			Transport:       transport,
			ResourceServers: origins,
			Classifiers:     metapublishing.ResponseClassifiers(),
		},
	)
	if err != nil {
		return metapublishing.RegistrationConfig{}, err
	}
	return metapublishing.RegistrationConfig{
		Executor:            executor,
		GraphVersion:        graphVersion,
		ThreadsGraphVersion: "",
		AutoProviders:       providers,
	}, nil
}

func exactTrue(key string) bool {
	return strings.TrimSpace(os.Getenv(key)) == "true"
}

func allPresent(values map[string]string, keys ...string) bool {
	for _, key := range keys {
		if strings.TrimSpace(values[key]) == "" {
			return false
		}
	}
	return true
}

type denySocialMutation struct{}

func (denySocialMutation) Authorize(
	context.Context,
	string,
	string,
	socialconnections.Permission,
) error {
	return socialconnections.ErrUnauthorized
}

type pinnedMetaTransport struct {
	transport *http.Transport
	resolver  *net.Resolver
	mu        sync.RWMutex
	pins      map[string][]netip.Addr
}

func newPinnedMetaTransport() *pinnedMetaTransport {
	result := &pinnedMetaTransport{
		resolver: net.DefaultResolver,
		pins:     make(map[string][]netip.Addr),
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = http.ProxyFromEnvironment
	base.DialContext = result.dialContext
	base.ForceAttemptHTTP2 = true
	result.transport = base
	return result
}

func (transport *pinnedMetaTransport) PinOrigin(
	ctx context.Context,
	origin string,
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin, nil)
	if err != nil || request.URL.Scheme != "https" ||
		request.URL.Hostname() == "" {
		return errors.New("invalid Meta resource origin")
	}
	host := strings.ToLower(request.URL.Hostname())
	addresses, err := transport.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve Meta resource origin: %w", err)
	}
	public := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !address.IsValid() || !address.IsGlobalUnicast() ||
			address.IsPrivate() || address.IsLoopback() ||
			address.IsLinkLocalUnicast() {
			return errors.New("Meta resource origin resolved outside public IP space")
		}
		public = append(public, address)
	}
	if len(public) == 0 {
		return errors.New("Meta resource origin has no public address")
	}
	transport.mu.Lock()
	transport.pins[host] = public
	transport.mu.Unlock()
	return nil
}

func (transport *pinnedMetaTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return transport.transport.RoundTrip(request)
}

func (transport *pinnedMetaTransport) dialContext(
	ctx context.Context,
	network, address string,
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	transport.mu.RLock()
	addresses := append([]netip.Addr(nil), transport.pins[strings.ToLower(host)]...)
	transport.mu.RUnlock()
	if len(addresses) == 0 {
		return nil, errors.New("Meta resource origin was not DNS-pinned")
	}
	var dialer net.Dialer
	var failures []error
	for _, pinned := range addresses {
		connection, dialErr := dialer.DialContext(
			ctx,
			network,
			net.JoinHostPort(pinned.String(), port),
		)
		if dialErr == nil {
			return connection, nil
		}
		failures = append(failures, dialErr)
	}
	return nil, errors.Join(failures...)
}
