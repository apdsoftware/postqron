package socialconnections

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const linkedinTestAssetURN = "urn:li:image:abc"
const linkedinTestSignedURL = "https://www.linkedin.com/dms-uploads/image/upload" +
	"?ca=123&cn=opaque%2Bsignature&empty="

type linkedinDMSFixture struct {
	service    *Service
	repository Repository
	memory     *MemoryRepository
	adapter    *fakeAdapter
	connection Connection
	now        *time.Time
}

type linkedinDMSCompletionFailureRepository struct {
	Repository
	fromState LinkedInDMSGrantState
	toState   LinkedInDMSGrantState
}

func (repository *linkedinDMSCompletionFailureRepository) TransitionLinkedInDMSGrant(
	ctx context.Context,
	command LinkedInDMSGrantTransition,
) (StoredLinkedInDMSGrant, error) {
	if command.FromState == repository.fromState &&
		command.ToState == repository.toState {
		return StoredLinkedInDMSGrant{}, errors.New(
			"injected provider completion persistence failure",
		)
	}
	return repository.Repository.TransitionLinkedInDMSGrant(ctx, command)
}

func newLinkedInDMSFixture(t *testing.T) linkedinDMSFixture {
	t.Helper()
	memory := NewMemoryRepository()
	return newLinkedInDMSFixtureWithRepository(
		t,
		memory,
		"person-1",
		"workspace-1",
		"owner-1",
		serviceTestNow,
	)
}

func newLinkedInDMSFixtureWithRepository(
	t *testing.T,
	repository Repository,
	remoteID, workspaceID, actorID string,
	now time.Time,
) linkedinDMSFixture {
	t.Helper()
	authorizer := &fakeAuthorizer{permissions: map[Permission]bool{
		PermissionViewWorkspace:  true,
		PermissionManageChannels: true,
	}}
	cipher, err := NewAESGCMCipher(
		"test-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	expires := now.Add(time.Hour)
	const scope = "w_member_social"
	adapter := &fakeAdapter{
		config: OAuthConfig{
			ClientID:         "linkedin-client",
			AuthorizationURL: "https://www.linkedin.com/oauth/v2/authorization",
			RedirectURL:      "https://app.example.test/social/callback",
			Scopes:           []string{scope},
			ScopeSeparator:   OAuthScopeSeparatorSpace,
		},
		grant: Credential{
			AccessToken: "linkedin-member-token",
			ExpiresAt:   &expires,
			Scopes:      []string{scope},
		},
		resources: []DiscoveredResource{
			{
				Candidate: Candidate{
					RemoteID:     remoteID,
					ResourceType: ResourceLinkedInProfile,
					AccountType:  AccountTypeProfile,
					DisplayName:  "LinkedIn member",
					Scopes:       []string{scope},
				},
				Credential: Credential{
					AccessToken: "linkedin-member-token",
					ExpiresAt:   &expires,
					Scopes:      []string{scope},
				},
			},
			{
				Candidate: Candidate{
					RemoteID:     "person-secondary",
					ResourceType: ResourceLinkedInProfile,
					AccountType:  AccountTypeProfile,
					DisplayName:  "Secondary LinkedIn member",
					Scopes:       []string{scope},
				},
				Credential: Credential{
					AccessToken: "linkedin-secondary-token",
					ExpiresAt:   &expires,
					Scopes:      []string{scope},
				},
			},
		},
	}
	clock := now
	service, err := NewService(Config{
		Repository: repository,
		Authorizer: authorizer,
		Cipher:     cipher,
		Quota:      newFakeChannelQuota(),
		Adapters: map[Provider]Adapter{
			ProviderLinkedIn: adapter,
		},
		Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	connection := connectLinkedInResource(
		t,
		service,
		workspaceID,
		actorID,
		remoteID,
	)
	fixture := linkedinDMSFixture{
		service: service, repository: repository, adapter: adapter,
		connection: connection, now: &clock,
	}
	if memory, ok := repository.(*MemoryRepository); ok {
		fixture.memory = memory
	}
	return fixture
}

func connectLinkedInResource(
	t *testing.T,
	service *Service,
	workspaceID, actorID, remoteID string,
) Connection {
	t.Helper()
	authorization, err := service.Begin(context.Background(), BeginRequest{
		WorkspaceID: workspaceID,
		ActorID:     actorID,
		Provider:    ProviderLinkedIn,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorization.URL)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := service.Callback(context.Background(), CallbackRequest{
		State: parsed.Query().Get("state"),
		Code:  "linkedin-code",
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := service.Select(context.Background(), SelectRequest{
		WorkspaceID: workspaceID,
		ActorID:     actorID,
		SelectionID: selection.ID,
		RemoteID:    remoteID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func newLinkedInBoundaryExecutor(
	t *testing.T,
	service *Service,
	transport *executorTransport,
	maxMediaBytes int64,
) *AuthenticatedExecutor {
	t.Helper()
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service: service, Transport: transport,
		ResourceServers: map[Provider]string{
			ProviderLinkedIn: "https://api.linkedin.com",
		},
		MaxMediaBytes: maxMediaBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func linkedinInitializeResponse(expiry time.Time) string {
	expiresField := ""
	if !expiry.IsZero() {
		expiresField = `,"uploadUrlExpiresAt":` +
			strconv.FormatInt(expiry.UnixMilli(), 10)
	}
	return `{"value":{"uploadUrl":"` + linkedinTestSignedURL +
		`","image":"` + linkedinTestAssetURN + `"` + expiresField + `}}`
}

func initializeHandle(
	t *testing.T,
	executor *AuthenticatedExecutor,
	connectionID string,
) LinkedInDMSHandle {
	t.Helper()
	handle, err := executor.InitializeLinkedInDMS(
		context.Background(),
		LinkedInDMSInitializeRequest{
			WorkspaceID:      "workspace-1",
			ConnectionID:     connectionID,
			ExpectedProvider: ProviderLinkedIn,
			Header: http.Header{
				"Content-Type":              {"application/json"},
				"LinkedIn-Version":          {"202607"},
				"X-Restli-Protocol-Version": {"2.0.0"},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if handle.Handle == "" {
		t.Fatal("initialize returned an empty opaque handle")
	}
	return handle
}

func linkedinUploadRequest(
	connectionID, handle string,
	body []byte,
) LinkedInDMSUploadRequest {
	sum := sha256.Sum256(body)
	return LinkedInDMSUploadRequest{
		WorkspaceID:      "workspace-1",
		ConnectionID:     connectionID,
		ExpectedProvider: ProviderLinkedIn,
		Handle:           handle,
		Header:           http.Header{"Content-Type": {"image/png"}},
		Media: &PublishingMedia{
			Body:   io.NopCloser(bytes.NewReader(body)),
			Size:   int64(len(body)),
			SHA256: hex.EncodeToString(sum[:]),
		},
	}
}

func TestLinkedInDMSPublicContractsExposeOnlyOpaqueHandle(t *testing.T) {
	for _, contract := range []any{
		LinkedInDMSInitializeRequest{},
		LinkedInDMSHandle{},
		LinkedInDMSUploadRequest{},
		LinkedInAssetStateRequest{},
		LinkedInCreateAfterAssetAvailableRequest{},
		LinkedInDMSUploadEvidence{},
		LinkedInAssetStateEvidence{},
	} {
		contractType := reflect.TypeOf(contract)
		for index := 0; index < contractType.NumField(); index++ {
			name := contractType.Field(index).Name
			switch name {
			case "UploadURL", "AssetURN", "ExpiresAt":
				t.Fatalf("%s exposes forbidden field %s", contractType, name)
			}
		}
	}
	if _, exists := reflect.TypeOf(
		LinkedInDMSInitializeRequest{},
	).FieldByName("Body"); exists {
		t.Fatal("initialize contract exposes a caller-controlled provider body")
	}
}

func TestLinkedInDMSInitializeRegistersEncryptedBoundEvidenceAndServerExpiry(
	t *testing.T,
) {
	fixture := newLinkedInDMSFixture(t)
	transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
		if request.URL.String() !=
			"https://api.linkedin.com/rest/images?action=initializeUpload" {
			t.Fatalf("initialize target = %q", request.URL)
		}
		var payload struct {
			Initialize struct {
				Owner string `json:"owner"`
			} `json:"initializeUploadRequest"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Initialize.Owner != "urn:li:person:person-1" {
			t.Fatalf("server-derived initialize owner = %q", payload.Initialize.Owner)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				linkedinInitializeResponse(serviceTestNow.Add(24 * time.Hour)),
			)),
		}, nil
	}}
	executor := newLinkedInBoundaryExecutor(t, fixture.service, transport, 0)
	handle := initializeHandle(t, executor, fixture.connection.ID)
	grant, err := fixture.repository.GetLinkedInDMSGrant(
		context.Background(),
		"workspace-1",
		fixture.connection.ID,
		digest(handle.Handle),
		serviceTestNow,
	)
	if err != nil {
		t.Fatal(err)
	}
	if grant.State != LinkedInDMSGrantRegistered ||
		!grant.ExpiresAt.Equal(
			serviceTestNow.Add(maximumLinkedInDMSUploadLifetime),
		) {
		t.Fatalf("registered grant = %#v", grant)
	}
	if bytes.Contains(grant.EvidenceCiphertext.Data, []byte(linkedinTestSignedURL)) ||
		bytes.Contains(grant.EvidenceCiphertext.Data, []byte(linkedinTestAssetURN)) {
		t.Fatal("provider URL/query or asset URN persisted in plaintext")
	}
	evidence, err := executor.openLinkedInDMSEvidence(grant)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.UploadURL != linkedinTestSignedURL ||
		evidence.AssetURN != linkedinTestAssetURN {
		t.Fatalf("opened provider evidence = %#v", evidence)
	}
}

func TestLinkedInDMSInitializeUsesEarlierProviderExpiryAndRejectsExpired(
	t *testing.T,
) {
	fixture := newLinkedInDMSFixture(t)
	providerExpiry := serviceTestNow.Add(2 * time.Minute)
	var responseBody atomic.Value
	responseBody.Store(linkedinInitializeResponse(providerExpiry))
	transport := &executorTransport{do: func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(responseBody.Load().(string))),
		}, nil
	}}
	executor := newLinkedInBoundaryExecutor(t, fixture.service, transport, 0)
	handle := initializeHandle(t, executor, fixture.connection.ID)
	grant, err := fixture.repository.GetLinkedInDMSGrant(
		context.Background(), "workspace-1", fixture.connection.ID,
		digest(handle.Handle), serviceTestNow,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !grant.ExpiresAt.Equal(providerExpiry) {
		t.Fatalf("provider expiry = %s, want %s", grant.ExpiresAt, providerExpiry)
	}
	responseBody.Store(linkedinInitializeResponse(serviceTestNow))
	_, err = executor.InitializeLinkedInDMS(
		context.Background(),
		LinkedInDMSInitializeRequest{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn,
			Header:           http.Header{"Content-Type": {"application/json"}},
		},
	)
	if failureCode(err) != "linkedin_initialize_evidence_invalid" {
		t.Fatalf("expired provider evidence error = %#v", err)
	}
}

func TestLinkedInDMSInitializeRejectsUnsafeProviderEvidence(t *testing.T) {
	invalidURLs := []string{
		"http://www.linkedin.com/dms-uploads/image?sig=x",
		"https://linkedin.com/dms-uploads/image?sig=x",
		"https://api.linkedin.com/dms-uploads/image?sig=x",
		"https://www.linkedin.com.evil.test/dms-uploads/image?sig=x",
		"https://www.linkedin.com:443/dms-uploads/image?sig=x",
		"https://127.0.0.1/dms-uploads/image?sig=x",
		"https://[::1]/dms-uploads/image?sig=x",
		"https://user@www.linkedin.com/dms-uploads/image?sig=x",
		"https://www.linkedin.com/dms-uploads/image?sig=x#fragment",
		"https://www.linkedin.com/dms-uploads/image",
		"https://www.linkedin.com/dms-uploads/../admin?sig=x",
		"https://www.linkedin.com/dms-uploads/%2e%2e/admin?sig=x",
		"https://www.linkedin.com/dms-uploads/%2f%2fevil?sig=x",
		"https://www.linkedin.com/dms-uploads/%5cevil?sig=x",
		"https://www.linkedin.com/dms-uploads//image?sig=x",
	}
	for _, raw := range invalidURLs {
		t.Run(raw, func(t *testing.T) {
			encoded, err := json.Marshal(map[string]any{
				"value": map[string]string{
					"uploadUrl": raw,
					"image":     linkedinTestAssetURN,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = decodeLinkedInDMSInitializeResponse(
				encoded,
				serviceTestNow,
			); err == nil {
				t.Fatalf("unsafe provider URL %q was accepted", raw)
			}
		})
	}
}

func TestAuthenticatedExecutorBlocksLinkedInDMSHandleBypasses(t *testing.T) {
	fixture := newLinkedInDMSFixture(t)
	transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/rest/posts" {
			t.Fatalf("forbidden generic request reached transport: %s %s", request.Method, request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusCreated, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"id":"text-post"}`)),
		}, nil
	}}
	executor := newLinkedInBoundaryExecutor(t, fixture.service, transport, 0)
	for _, request := range []PublishingRequest{
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Method: http.MethodPost,
			Path: linkedinDMSInitializePath,
			Body: []byte(`{"initializeUploadRequest":{}}`),
		},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Method: http.MethodGet,
			Path: "/rest/images/urn:li:image:caller",
		},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Method: http.MethodGet,
			Path: "/rest/images?ids=urn%3Ali%3Aimage%3Acaller",
		},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Method: http.MethodPost,
			Path: "/rest/images/?action=initializeUpload",
			Body: []byte(`{"initializeUploadRequest":{}}`),
		},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Method: http.MethodPut,
			Path: "/rest/%69mages?action=initializeUpload",
			Body: []byte(`{"initializeUploadRequest":{}}`),
		},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Method: http.MethodDelete,
			Path: "/rest/images%2Furn:li:image:caller",
		},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Method: http.MethodPost,
			Path: "/rest/posts",
			Body: []byte(`{"content":{"media":{"id":"urn:li:image:caller"}}}`),
		},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Method: http.MethodPost,
			Path: "/rest/posts",
			Body: []byte(`{"content":{"multiImage":{"images":[{"id":"urn:li:image:caller"}]}}}`),
		},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Method: http.MethodPost,
			Path: "/rest/posts/",
			Body: []byte(`{"content":{"media":{"id":"urn:li:image:caller"}}}`),
		},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Method: http.MethodGet,
			Path: "/rest/posts",
			Body: []byte(`{"content":{"media":{"id":"urn:li:image:caller"}}}`),
		},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Method: http.MethodPost,
			Path: "/rest/%70osts",
			Body: []byte(`{"content":{"media":{"id":"urn:li:image:caller"}}}`),
		},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Method: http.MethodPost,
			Path: "/rest/videos",
			Body: []byte(`{"content":{"media":{"id":"urn:li:image:caller"}}}`),
		},
	} {
		if _, err := executor.Execute(
			context.Background(),
			request,
		); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("generic LinkedIn DMS bypass error = %v", err)
		}
	}
	response, err := executor.Execute(
		context.Background(),
		PublishingRequest{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Method: http.MethodPost,
			Path: "/rest/posts",
			Body: []byte(`{"commentary":"text only","visibility":"PUBLIC"}`),
		},
	)
	if err != nil || response.StatusCode != http.StatusCreated {
		t.Fatalf("text-only LinkedIn create response=%#v err=%v", response, err)
	}
	if transport.calls.Load() != 1 {
		t.Fatalf("generic transport calls = %d, want text only", transport.calls.Load())
	}
}

func TestLinkedInDMSUploadResolvesHandlePreservesQueryAndPinsOverTLS(
	t *testing.T,
) {
	body := []byte("streamed-linkedin-image")
	received := make(chan struct {
		uri  string
		auth string
		body []byte
	}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(
		func(writer http.ResponseWriter, incoming *http.Request) {
			raw, err := io.ReadAll(incoming.Body)
			if err != nil {
				t.Error(err)
			}
			received <- struct {
				uri  string
				auth string
				body []byte
			}{
				uri:  incoming.URL.RequestURI(),
				auth: incoming.Header.Get("Authorization"), body: raw,
			}
			writer.WriteHeader(http.StatusCreated)
		},
	))
	defer server.Close()
	fixture := newLinkedInDMSFixture(t)
	transport := &executorTransport{do: func(outgoing *http.Request) (*http.Response, error) {
		if outgoing.URL.Host == "api.linkedin.com" {
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					linkedinInitializeResponse(time.Time{}),
				)),
			}, nil
		}
		forward, err := http.NewRequestWithContext(
			outgoing.Context(),
			outgoing.Method,
			server.URL+outgoing.URL.RequestURI(),
			outgoing.Body,
		)
		if err != nil {
			return nil, err
		}
		forward.Header = outgoing.Header.Clone()
		forward.ContentLength = outgoing.ContentLength
		return server.Client().Do(forward)
	}}
	executor := newLinkedInBoundaryExecutor(
		t,
		fixture.service,
		transport,
		int64(len(body)),
	)
	handle := initializeHandle(t, executor, fixture.connection.ID)
	result, err := executor.UploadLinkedInDMS(
		context.Background(),
		linkedinUploadRequest(fixture.connection.ID, handle.Handle, body),
	)
	if err != nil {
		t.Fatal(err)
	}
	got := <-received
	const wantURI = "/dms-uploads/image/upload?ca=123&cn=opaque%2Bsignature&empty="
	if got.uri != wantURI ||
		got.auth != "Bearer linkedin-member-token" ||
		!bytes.Equal(got.body, body) {
		t.Fatalf("TLS upload = uri %q auth %q body %q", got.uri, got.auth, got.body)
	}
	if result.StatusCode != http.StatusCreated ||
		result.SHA256 == "" {
		t.Fatalf("sanitized upload result = %#v", result)
	}
	if transport.pinnedOrigin() != linkedinDMSUploadOrigin {
		t.Fatalf("final pinned origin = %q", transport.pinnedOrigin())
	}
	grant, err := fixture.repository.GetLinkedInDMSGrant(
		context.Background(), "workspace-1", fixture.connection.ID,
		digest(handle.Handle), serviceTestNow,
	)
	if err != nil || grant.State != LinkedInDMSGrantUploaded {
		t.Fatalf("uploaded grant = %#v err=%v", grant, err)
	}
}

func TestLinkedInDMSUploadEnforcesBodyDigestAndResponseLimits(t *testing.T) {
	fixture := newLinkedInDMSFixture(t)
	var responseOversized atomic.Bool
	transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "api.linkedin.com" {
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					linkedinInitializeResponse(time.Time{}),
				)),
			}, nil
		}
		_, _ = io.Copy(io.Discard, request.Body)
		responseBody := io.NopCloser(strings.NewReader(`{}`))
		if responseOversized.Load() {
			responseBody = io.NopCloser(bytes.NewReader(
				make([]byte, maximumAuthenticatedBody+1),
			))
		}
		return &http.Response{
			StatusCode: http.StatusCreated, Header: make(http.Header),
			Body: responseBody,
		}, nil
	}}
	executor := newLinkedInBoundaryExecutor(t, fixture.service, transport, 4)
	oversizedHandle := initializeHandle(t, executor, fixture.connection.ID)
	_, err := executor.UploadLinkedInDMS(
		context.Background(),
		linkedinUploadRequest(
			fixture.connection.ID,
			oversizedHandle.Handle,
			[]byte("12345"),
		),
	)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("oversized media error = %#v", err)
	}
	oversizedGrant, err := fixture.repository.GetLinkedInDMSGrant(
		context.Background(), "workspace-1", fixture.connection.ID,
		digest(oversizedHandle.Handle), *fixture.now,
	)
	if err != nil || oversizedGrant.State != LinkedInDMSGrantRegistered {
		t.Fatalf("oversized media claimed grant=%#v err=%v", oversizedGrant, err)
	}
	digestHandle := initializeHandle(t, executor, fixture.connection.ID)
	badDigest := linkedinUploadRequest(
		fixture.connection.ID,
		digestHandle.Handle,
		[]byte("1234"),
	)
	badDigest.Media.SHA256 = strings.Repeat("0", sha256.Size*2)
	_, err = executor.UploadLinkedInDMS(context.Background(), badDigest)
	if failureKind(err) != ExecutorFailureAmbiguous {
		t.Fatalf("digest failure = %#v", err)
	}
	responseHandle := initializeHandle(t, executor, fixture.connection.ID)
	responseOversized.Store(true)
	_, err = executor.UploadLinkedInDMS(
		context.Background(),
		linkedinUploadRequest(
			fixture.connection.ID,
			responseHandle.Handle,
			[]byte("1234"),
		),
	)
	if failureKind(err) != ExecutorFailureAmbiguous {
		t.Fatalf("oversized response failure = %#v", err)
	}
}

func TestLinkedInDMSUploadRejectsNilAndIncompleteMediaWithoutPanic(
	t *testing.T,
) {
	fixture := newLinkedInDMSFixture(t)
	transport := &executorTransport{do: func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid media reached transport")
		return nil, nil
	}}
	executor := newLinkedInBoundaryExecutor(t, fixture.service, transport, 0)
	requests := []LinkedInDMSUploadRequest{
		{},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Handle: "opaque",
		},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Handle: "opaque",
			Media: &PublishingMedia{Size: 1, SHA256: strings.Repeat("0", 64)},
		},
		{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Handle: "opaque",
			Media: &PublishingMedia{Body: io.NopCloser(strings.NewReader("x"))},
		},
	}
	missingContentType := linkedinUploadRequest(
		fixture.connection.ID,
		"opaque",
		[]byte("x"),
	)
	missingContentType.Header = nil
	requests = append(requests, missingContentType)
	for index, request := range requests {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("request %d panicked: %v", index, recovered)
				}
			}()
			_, err := executor.UploadLinkedInDMS(context.Background(), request)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("request %d error = %v", index, err)
			}
		}()
	}
}

func TestLinkedInDMSHandleBindingPreventsConfusedDeputyAndReuse(t *testing.T) {
	fixture := newLinkedInDMSFixture(t)
	transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "api.linkedin.com" {
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					linkedinInitializeResponse(time.Time{}),
				)),
			}, nil
		}
		_, _ = io.Copy(io.Discard, request.Body)
		return &http.Response{
			StatusCode: http.StatusCreated, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	}}
	executor := newLinkedInBoundaryExecutor(t, fixture.service, transport, 0)
	handle := initializeHandle(t, executor, fixture.connection.ID)
	secondConnection := connectLinkedInResource(
		t,
		fixture.service,
		"workspace-1",
		"owner-1",
		"person-secondary",
	)
	wrongWorkspace := linkedinUploadRequest(
		fixture.connection.ID,
		handle.Handle,
		[]byte("x"),
	)
	wrongWorkspace.WorkspaceID = "workspace-other"
	if _, err := executor.UploadLinkedInDMS(
		context.Background(),
		wrongWorkspace,
	); err == nil {
		t.Fatal("cross-workspace handle was accepted")
	}
	wrongConnection := linkedinUploadRequest(
		secondConnection.ID,
		handle.Handle,
		[]byte("x"),
	)
	if _, err := executor.UploadLinkedInDMS(
		context.Background(),
		wrongConnection,
	); err == nil {
		t.Fatal("cross-connection handle was accepted")
	}
	wrongProvider := linkedinUploadRequest(
		fixture.connection.ID,
		handle.Handle,
		[]byte("x"),
	)
	wrongProvider.ExpectedProvider = ProviderX
	if _, err := executor.UploadLinkedInDMS(
		context.Background(),
		wrongProvider,
	); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("cross-provider error = %v", err)
	}
	if _, err := executor.UploadLinkedInDMS(
		context.Background(),
		linkedinUploadRequest(fixture.connection.ID, handle.Handle, []byte("x")),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.UploadLinkedInDMS(
		context.Background(),
		linkedinUploadRequest(fixture.connection.ID, handle.Handle, []byte("x")),
	); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("reused upload handle error = %v", err)
	}
}

func TestLinkedInDMSUploadClaimAllowsOnlyOneConcurrentUse(t *testing.T) {
	fixture := newLinkedInDMSFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var uploadCalls atomic.Int32
	transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "api.linkedin.com" {
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					linkedinInitializeResponse(time.Time{}),
				)),
			}, nil
		}
		if uploadCalls.Add(1) == 1 {
			close(entered)
		}
		<-release
		_, _ = io.Copy(io.Discard, request.Body)
		return &http.Response{
			StatusCode: http.StatusCreated, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	}}
	executor := newLinkedInBoundaryExecutor(t, fixture.service, transport, 0)
	handle := initializeHandle(t, executor, fixture.connection.ID)
	firstDone := make(chan error, 1)
	go func() {
		_, err := executor.UploadLinkedInDMS(
			context.Background(),
			linkedinUploadRequest(
				fixture.connection.ID,
				handle.Handle,
				[]byte("first"),
			),
		)
		firstDone <- err
	}()
	<-entered
	_, concurrentErr := executor.UploadLinkedInDMS(
		context.Background(),
		linkedinUploadRequest(
			fixture.connection.ID,
			handle.Handle,
			[]byte("second"),
		),
	)
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if failureKind(concurrentErr) != ExecutorFailureAmbiguous {
		t.Fatalf("concurrent handle error = %v", concurrentErr)
	}
	if uploadCalls.Load() != 1 {
		t.Fatalf("signed upload calls = %d", uploadCalls.Load())
	}
}

func TestLinkedInDMSRetryKeepsRegisteredExpiryAndExpiryCannotReset(t *testing.T) {
	fixture := newLinkedInDMSFixture(t)
	transport := &executorTransport{
		do: func(request *http.Request) (*http.Response, error) {
			if request.URL.Host == "api.linkedin.com" {
				return &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader(
						linkedinInitializeResponse(time.Time{}),
					)),
				}, nil
			}
			_, _ = io.Copy(io.Discard, request.Body)
			return &http.Response{
				StatusCode: http.StatusCreated, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{}`)),
			}, nil
		},
	}
	executor := newLinkedInBoundaryExecutor(t, fixture.service, transport, 0)
	handle := initializeHandle(t, executor, fixture.connection.ID)
	original, err := fixture.repository.GetLinkedInDMSGrant(
		context.Background(), "workspace-1", fixture.connection.ID,
		digest(handle.Handle), *fixture.now,
	)
	if err != nil {
		t.Fatal(err)
	}
	transport.pinErr = errors.New("private DNS answer")
	_, err = executor.UploadLinkedInDMS(
		context.Background(),
		linkedinUploadRequest(fixture.connection.ID, handle.Handle, []byte("retry")),
	)
	if failureCode(err) != "provider_dns_pin_failed" {
		t.Fatalf("pin failure = %#v", err)
	}
	afterFailure, err := fixture.repository.GetLinkedInDMSGrant(
		context.Background(), "workspace-1", fixture.connection.ID,
		digest(handle.Handle), *fixture.now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if afterFailure.State != LinkedInDMSGrantRegistered ||
		!afterFailure.ExpiresAt.Equal(original.ExpiresAt) {
		t.Fatalf("retry changed grant = %#v", afterFailure)
	}
	transport.pinErr = nil
	*fixture.now = original.ExpiresAt.Add(time.Nanosecond)
	_, err = executor.UploadLinkedInDMS(
		context.Background(),
		linkedinUploadRequest(fixture.connection.ID, handle.Handle, []byte("retry")),
	)
	if failureCode(err) != "linkedin_dms_handle_expired" {
		t.Fatalf("expired handle error = %#v", err)
	}
	if transport.calls.Load() != 1 {
		t.Fatalf("expired retry reached transport: %d calls", transport.calls.Load())
	}
}

func TestLinkedInDMSFailuresAreTerminalAfterTransport(t *testing.T) {
	tests := []struct {
		status int
		kind   ExecutorFailureKind
	}{
		{status: http.StatusFound, kind: ExecutorFailurePermanent},
		{status: http.StatusForbidden, kind: ExecutorFailurePermanent},
		{status: http.StatusTooManyRequests, kind: ExecutorFailureRateLimit},
		{status: http.StatusServiceUnavailable, kind: ExecutorFailureAmbiguous},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			fixture := newLinkedInDMSFixture(t)
			transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == "api.linkedin.com" {
					return &http.Response{
						StatusCode: http.StatusOK, Header: make(http.Header),
						Body: io.NopCloser(strings.NewReader(
							linkedinInitializeResponse(time.Time{}),
						)),
					}, nil
				}
				_, _ = io.Copy(io.Discard, request.Body)
				return &http.Response{
					StatusCode: test.status,
					Header: http.Header{
						"Location":    {"https://evil.example/capture"},
						"Retry-After": {"7"},
					},
					Body: io.NopCloser(strings.NewReader(`{}`)),
				}, nil
			}}
			executor := newLinkedInBoundaryExecutor(t, fixture.service, transport, 0)
			handle := initializeHandle(t, executor, fixture.connection.ID)
			_, err := executor.UploadLinkedInDMS(
				context.Background(),
				linkedinUploadRequest(
					fixture.connection.ID,
					handle.Handle,
					[]byte("body"),
				),
			)
			if failureKind(err) != test.kind {
				t.Fatalf("status %d failure = %#v", test.status, err)
			}
			grant, getErr := fixture.repository.GetLinkedInDMSGrant(
				context.Background(), "workspace-1", fixture.connection.ID,
				digest(handle.Handle), *fixture.now,
			)
			if getErr != nil || grant.State != LinkedInDMSGrantFailed {
				t.Fatalf("terminal grant = %#v err=%v", grant, getErr)
			}
		})
	}
}

func TestLinkedInAssetStatusAndCreateUseHandleInjectAssetAndConsumeOnce(
	t *testing.T,
) {
	fixture := newLinkedInDMSFixture(t)
	var mu sync.Mutex
	requests := make([]string, 0, 4)
	state := "AVAILABLE"
	var createBody []byte
	transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, request.Method+" "+request.URL.RequestURI())
		mu.Unlock()
		switch {
		case request.URL.RawQuery == "action=initializeUpload":
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					linkedinInitializeResponse(time.Time{}),
				)),
			}, nil
		case request.URL.Host == "www.linkedin.com":
			_, _ = io.Copy(io.Discard, request.Body)
			return &http.Response{
				StatusCode: http.StatusCreated, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{}`)),
			}, nil
		case request.Method == http.MethodGet:
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"value":{"status":"` + state + `","extra":"safe"}}`,
				)),
			}, nil
		default:
			createBody, _ = io.ReadAll(request.Body)
			return &http.Response{
				StatusCode: http.StatusCreated, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"id":"remote-post"}`)),
			}, nil
		}
	}}
	executor := newLinkedInBoundaryExecutor(t, fixture.service, transport, 0)
	handle := initializeHandle(t, executor, fixture.connection.ID)
	if _, err := executor.UploadLinkedInDMS(
		context.Background(),
		linkedinUploadRequest(
			fixture.connection.ID,
			handle.Handle,
			[]byte("asset"),
		),
	); err != nil {
		t.Fatal(err)
	}
	status, err := executor.InspectLinkedInAsset(
		context.Background(),
		LinkedInAssetStateRequest{
			WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
			ExpectedProvider: ProviderLinkedIn, Handle: handle.Handle,
			Header: http.Header{"LinkedIn-Version": {"202607"}},
		},
	)
	if err != nil || status.State != "AVAILABLE" {
		t.Fatalf("asset status = %#v err=%v", status, err)
	}
	create := LinkedInCreateAfterAssetAvailableRequest{
		WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
		ExpectedProvider: ProviderLinkedIn, Handle: handle.Handle,
		Path: "/rest/posts",
		Header: http.Header{
			"Content-Type":              {"application/json"},
			"LinkedIn-Version":          {"202607"},
			"X-Restli-Protocol-Version": {"2.0.0"},
		},
		Body: []byte(`{"commentary":"hello","content":{"media":{"title":"alt"}}}`),
	}
	mu.Lock()
	callsBeforeInvalidCreate := len(requests)
	mu.Unlock()
	for _, invalidPath := range []string{
		"/rest/posts/",
		"/rest/posts?viewContext=AUTHOR",
		"/rest/%70osts",
		"/rest/articles",
	} {
		invalidCreate := create
		invalidCreate.Path = invalidPath
		if _, _, invalidErr := executor.ExecuteLinkedInCreateAfterAssetAvailable(
			context.Background(),
			invalidCreate,
		); !errors.Is(invalidErr, ErrInvalidArgument) {
			t.Fatalf("non-canonical create path %q error = %v", invalidPath, invalidErr)
		}
	}
	mu.Lock()
	callsAfterInvalidCreate := len(requests)
	mu.Unlock()
	if callsAfterInvalidCreate != callsBeforeInvalidCreate {
		t.Fatalf(
			"non-canonical create reached provider: %d -> %d",
			callsBeforeInvalidCreate,
			callsAfterInvalidCreate,
		)
	}
	response, evidence, err := executor.ExecuteLinkedInCreateAfterAssetAvailable(
		context.Background(),
		create,
	)
	if err != nil || evidence.State != "AVAILABLE" ||
		response.StatusCode != http.StatusCreated {
		t.Fatalf("create response=%#v evidence=%#v err=%v", response, evidence, err)
	}
	var sent struct {
		Content struct {
			Media struct {
				ID string `json:"id"`
			} `json:"media"`
		} `json:"content"`
	}
	if err = json.Unmarshal(createBody, &sent); err != nil ||
		sent.Content.Media.ID != linkedinTestAssetURN {
		t.Fatalf("server-injected create body = %s err=%v", createBody, err)
	}
	if bytes.Contains(create.Body, []byte(linkedinTestAssetURN)) {
		t.Fatal("caller template contained provider asset URN")
	}
	_, _, err = executor.ExecuteLinkedInCreateAfterAssetAvailable(
		context.Background(),
		create,
	)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("consumed create handle error = %v", err)
	}
	grant, err := fixture.repository.GetLinkedInDMSGrant(
		context.Background(), "workspace-1", fixture.connection.ID,
		digest(handle.Handle), *fixture.now,
	)
	if err != nil || grant.State != LinkedInDMSGrantConsumed {
		t.Fatalf("consumed grant = %#v err=%v", grant, err)
	}
}

func TestLinkedInCreateRequiresAvailableAndRejectsCallerAssetID(t *testing.T) {
	fixture := newLinkedInDMSFixture(t)
	state := "PROCESSING"
	var createCalls atomic.Int32
	transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.RawQuery == "action=initializeUpload":
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					linkedinInitializeResponse(time.Time{}),
				)),
			}, nil
		case request.URL.Host == "www.linkedin.com":
			_, _ = io.Copy(io.Discard, request.Body)
			return &http.Response{
				StatusCode: http.StatusCreated, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{}`)),
			}, nil
		case request.Method == http.MethodGet:
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"value":{"status":"` + state + `"}}`,
				)),
			}, nil
		default:
			createCalls.Add(1)
			return &http.Response{
				StatusCode: http.StatusCreated, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{}`)),
			}, nil
		}
	}}
	executor := newLinkedInBoundaryExecutor(t, fixture.service, transport, 0)
	handle := initializeHandle(t, executor, fixture.connection.ID)
	if _, err := executor.UploadLinkedInDMS(
		context.Background(),
		linkedinUploadRequest(fixture.connection.ID, handle.Handle, []byte("asset")),
	); err != nil {
		t.Fatal(err)
	}
	request := LinkedInCreateAfterAssetAvailableRequest{
		WorkspaceID: "workspace-1", ConnectionID: fixture.connection.ID,
		ExpectedProvider: ProviderLinkedIn, Handle: handle.Handle,
		Path:   "/rest/posts",
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"content":{"media":{}}}`),
	}
	_, evidence, err := executor.ExecuteLinkedInCreateAfterAssetAvailable(
		context.Background(),
		request,
	)
	if failureCode(err) != "linkedin_asset_not_available" ||
		evidence.State != "PROCESSING" ||
		createCalls.Load() != 0 {
		t.Fatalf("processing create evidence=%#v err=%v calls=%d", evidence, err, createCalls.Load())
	}
	state = "AVAILABLE"
	request.Body = []byte(
		`{"content":{"media":{"id":"urn:li:image:caller-controlled"}}}`,
	)
	_, _, err = executor.ExecuteLinkedInCreateAfterAssetAvailable(
		context.Background(),
		request,
	)
	if !errors.Is(err, ErrInvalidArgument) || createCalls.Load() != 0 {
		t.Fatalf("caller asset ID error=%v calls=%d", err, createCalls.Load())
	}
}

func TestPostgresLinkedInDMSLeasesRecoverPreCallAndNeverReplayPostCall(
	t *testing.T,
) {
	databaseURL := os.Getenv("F05_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F05_DATABASE_URL is not set")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	workspaceID := "linkedin-dms-pg-workspace-" + suffix
	actorID := "linkedin-dms-pg-owner-" + suffix
	remoteID := "linkedin-dms-pg-person-" + suffix
	repository, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	fixture := newLinkedInDMSFixtureWithRepository(
		t,
		repository,
		remoteID,
		workspaceID,
		actorID,
		serviceTestNow,
	)
	var uploadCalls atomic.Int32
	var createCalls atomic.Int32
	transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.RawQuery == "action=initializeUpload":
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					linkedinInitializeResponse(time.Time{}),
				)),
			}, nil
		case request.URL.Host == "www.linkedin.com":
			uploadCalls.Add(1)
			_, _ = io.Copy(io.Discard, request.Body)
			return &http.Response{
				StatusCode: http.StatusCreated, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{}`)),
			}, nil
		case request.Method == http.MethodGet:
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"value":{"status":"AVAILABLE"}}`,
				)),
			}, nil
		default:
			createCalls.Add(1)
			return &http.Response{
				StatusCode: http.StatusCreated, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{}`)),
			}, nil
		}
	}}
	firstExecutor := newLinkedInBoundaryExecutor(t, fixture.service, transport, 0)
	initialize := func(executor *AuthenticatedExecutor) LinkedInDMSHandle {
		t.Helper()
		handle, initializeErr := executor.InitializeLinkedInDMS(
			context.Background(),
			LinkedInDMSInitializeRequest{
				WorkspaceID: workspaceID, ConnectionID: fixture.connection.ID,
				ExpectedProvider: ProviderLinkedIn,
				Header:           http.Header{"Content-Type": {"application/json"}},
			},
		)
		if initializeErr != nil {
			t.Fatal(initializeErr)
		}
		return handle
	}
	restart := func(
		restartRepository Repository,
	) *AuthenticatedExecutor {
		t.Helper()
		restartedService := *fixture.service
		restartedService.repository = restartRepository
		return newLinkedInBoundaryExecutor(
			t,
			&restartedService,
			transport,
			0,
		)
	}
	upload := func(
		executor *AuthenticatedExecutor,
		handle string,
	) error {
		t.Helper()
		request := linkedinUploadRequest(
			fixture.connection.ID,
			handle,
			[]byte("restart"),
		)
		request.WorkspaceID = workspaceID
		_, uploadErr := executor.UploadLinkedInDMS(context.Background(), request)
		return uploadErr
	}
	create := func(
		executor *AuthenticatedExecutor,
		handle string,
	) error {
		t.Helper()
		_, _, createErr := executor.ExecuteLinkedInCreateAfterAssetAvailable(
			context.Background(),
			LinkedInCreateAfterAssetAvailableRequest{
				WorkspaceID: workspaceID, ConnectionID: fixture.connection.ID,
				ExpectedProvider: ProviderLinkedIn, Handle: handle,
				Path:   "/rest/posts",
				Header: http.Header{"Content-Type": {"application/json"}},
				Body:   []byte(`{"content":{"media":{}}}`),
			},
		)
		return createErr
	}
	newPostgresRepository := func() *PostgresRepository {
		t.Helper()
		restartedRepository, repositoryErr := NewPostgresRepository(database)
		if repositoryErr != nil {
			t.Fatal(repositoryErr)
		}
		return restartedRepository
	}

	handle := initialize(firstExecutor)
	restartedExecutor := restart(newPostgresRepository())
	if err = upload(restartedExecutor, handle.Handle); err != nil {
		t.Fatal(err)
	}
	if err = upload(firstExecutor, handle.Handle); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("restart reuse error = %v", err)
	}
	if uploadCalls.Load() != 1 {
		t.Fatalf("PostgreSQL restart upload calls = %d", uploadCalls.Load())
	}
	var storedState, evidenceKey string
	var ciphertext []byte
	if err = database.QueryRowContext(context.Background(), `
		SELECT state, evidence_key_id, evidence_ciphertext
		FROM f05_linkedin_dms_grants
		WHERE handle_hash = $1`,
		digest(handle.Handle),
	).Scan(&storedState, &evidenceKey, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if storedState != string(LinkedInDMSGrantUploaded) ||
		evidenceKey != "test-key" ||
		bytes.Contains(ciphertext, []byte(linkedinTestSignedURL)) {
		t.Fatalf(
			"PostgreSQL grant state/key/ciphertext = %q/%q/%q",
			storedState,
			evidenceKey,
			ciphertext,
		)
	}

	preCallHandle := initialize(firstExecutor)
	preCallLease := "pre-call-upload-" + suffix
	preCallLockedUntil := fixture.service.now().UTC().Add(
		fixture.service.refreshLockTTL,
	)
	if _, err = repository.TransitionLinkedInDMSGrant(
		context.Background(),
		LinkedInDMSGrantTransition{
			HandleHash:  digest(preCallHandle.Handle),
			WorkspaceID: workspaceID, ConnectionID: fixture.connection.ID,
			FromState:  LinkedInDMSGrantRegistered,
			ToState:    LinkedInDMSGrantUploading,
			NewLeaseID: preCallLease, NewLockedUntil: &preCallLockedUntil,
			Now: fixture.service.now().UTC(),
		},
	); err != nil {
		t.Fatal(err)
	}
	preCallRestart := restart(newPostgresRepository())
	if err = upload(preCallRestart, preCallHandle.Handle); failureCode(err) !=
		"linkedin_handle_in_progress" {
		t.Fatalf("active pre-call lease error = %v", err)
	}
	if uploadCalls.Load() != 1 {
		t.Fatalf("active pre-call lease reached provider: %d", uploadCalls.Load())
	}
	*fixture.now = preCallLockedUntil.Add(time.Second)
	const competitors = 8
	results := make(chan error, competitors)
	var wait sync.WaitGroup
	for range competitors {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- upload(preCallRestart, preCallHandle.Handle)
		}()
	}
	wait.Wait()
	close(results)
	successes := 0
	for result := range results {
		if result == nil {
			successes++
		}
	}
	if successes != 1 || uploadCalls.Load() != 2 {
		t.Fatalf(
			"expired pre-call upload successes/calls = %d/%d",
			successes,
			uploadCalls.Load(),
		)
	}

	preCreateLease := "pre-call-create-" + suffix
	preCreateLockedUntil := fixture.service.now().UTC().Add(
		fixture.service.refreshLockTTL,
	)
	if _, err = newPostgresRepository().TransitionLinkedInDMSGrant(
		context.Background(),
		LinkedInDMSGrantTransition{
			HandleHash:  digest(preCallHandle.Handle),
			WorkspaceID: workspaceID, ConnectionID: fixture.connection.ID,
			FromState:  LinkedInDMSGrantUploaded,
			ToState:    LinkedInDMSGrantCreating,
			NewLeaseID: preCreateLease, NewLockedUntil: &preCreateLockedUntil,
			Now: fixture.service.now().UTC(),
		},
	); err != nil {
		t.Fatal(err)
	}
	preCreateRestart := restart(newPostgresRepository())
	if err = create(preCreateRestart, preCallHandle.Handle); failureCode(err) !=
		"linkedin_handle_in_progress" {
		t.Fatalf("active pre-call create lease error = %v", err)
	}
	if createCalls.Load() != 0 {
		t.Fatalf("active pre-call create lease reached provider: %d", createCalls.Load())
	}
	*fixture.now = preCreateLockedUntil.Add(time.Second)
	results = make(chan error, competitors)
	for range competitors {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- create(preCreateRestart, preCallHandle.Handle)
		}()
	}
	wait.Wait()
	close(results)
	successes = 0
	for result := range results {
		if result == nil {
			successes++
		}
	}
	if successes != 1 || createCalls.Load() != 1 {
		t.Fatalf(
			"expired pre-call create successes/calls = %d/%d",
			successes,
			createCalls.Load(),
		)
	}

	postUploadHandle := initialize(firstExecutor)
	postUploadRepository := &linkedinDMSCompletionFailureRepository{
		Repository: newPostgresRepository(),
		fromState:  LinkedInDMSGrantUploadSending,
		toState:    LinkedInDMSGrantUploaded,
	}
	if err = upload(
		restart(postUploadRepository),
		postUploadHandle.Handle,
	); failureCode(err) != "linkedin_upload_state_persistence_failed" ||
		failureKind(err) != ExecutorFailureAmbiguous {
		t.Fatalf("post-call upload crash error = %v", err)
	}
	postUploadCalls := uploadCalls.Load()
	if err = upload(
		restart(newPostgresRepository()),
		postUploadHandle.Handle,
	); failureCode(err) != "provider_outcome_ambiguous" ||
		failureKind(err) != ExecutorFailureAmbiguous {
		t.Fatalf("post-call upload restart error = %v", err)
	}
	if uploadCalls.Load() != postUploadCalls {
		t.Fatalf(
			"ambiguous upload replayed provider call: %d -> %d",
			postUploadCalls,
			uploadCalls.Load(),
		)
	}

	postCreateHandle := initialize(firstExecutor)
	if err = upload(
		restart(newPostgresRepository()),
		postCreateHandle.Handle,
	); err != nil {
		t.Fatal(err)
	}
	postCreateRepository := &linkedinDMSCompletionFailureRepository{
		Repository: newPostgresRepository(),
		fromState:  LinkedInDMSGrantCreateSending,
		toState:    LinkedInDMSGrantConsumed,
	}
	if err = create(
		restart(postCreateRepository),
		postCreateHandle.Handle,
	); failureCode(err) != "linkedin_create_state_persistence_failed" ||
		failureKind(err) != ExecutorFailureAmbiguous {
		t.Fatalf("post-call create crash error = %v", err)
	}
	postCreateCalls := createCalls.Load()
	if err = create(
		restart(newPostgresRepository()),
		postCreateHandle.Handle,
	); failureCode(err) != "provider_outcome_ambiguous" ||
		failureKind(err) != ExecutorFailureAmbiguous {
		t.Fatalf("post-call create restart error = %v", err)
	}
	if createCalls.Load() != postCreateCalls {
		t.Fatalf(
			"ambiguous create replayed provider call: %d -> %d",
			postCreateCalls,
			createCalls.Load(),
		)
	}
	for _, ambiguousHandle := range []LinkedInDMSHandle{
		postUploadHandle,
		postCreateHandle,
	} {
		var state, leaseID string
		var lockedUntil sql.NullTime
		if err = database.QueryRowContext(context.Background(), `
			SELECT state, lease_id, locked_until
			FROM f05_linkedin_dms_grants
			WHERE handle_hash = $1`,
			digest(ambiguousHandle.Handle),
		).Scan(&state, &leaseID, &lockedUntil); err != nil {
			t.Fatal(err)
		}
		if (state != string(LinkedInDMSGrantUploadSending) &&
			state != string(LinkedInDMSGrantCreateSending)) ||
			leaseID != "" ||
			lockedUntil.Valid {
			t.Fatalf(
				"ambiguous grant state/lease/lock = %q/%q/%v",
				state,
				leaseID,
				lockedUntil,
			)
		}
	}
}

func failureKind(err error) ExecutorFailureKind {
	var failure *ExecutorFailure
	if errors.As(err, &failure) {
		return failure.Kind
	}
	return ""
}

func failureCode(err error) string {
	var failure *ExecutorFailure
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ""
}
