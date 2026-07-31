package socialconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const linkedinDMSUploadOrigin = "https://www.linkedin.com"
const linkedinDMSUploadPathPrefix = "/dms-uploads/"
const maximumLinkedInDMSUploadLifetime = 15 * time.Minute
const linkedinDMSInitializePath = "/rest/images?action=initializeUpload"

// LinkedInDMSInitializeRequest contains only the ordinary relative API request
// that creates provider upload evidence. The provider response is consumed
// inside F5 and never returned to the caller.
type LinkedInDMSInitializeRequest struct {
	WorkspaceID      string
	ConnectionID     string
	ExpectedProvider Provider
	Header           http.Header
}

// LinkedInDMSHandle is an opaque capability. Its hash, binding, encrypted
// provider evidence, immutable expiry, and lifecycle state are persisted by F5.
type LinkedInDMSHandle struct {
	Handle string
}

type LinkedInDMSUploadRequest struct {
	WorkspaceID      string
	ConnectionID     string
	ExpectedProvider Provider
	Handle           string
	Header           http.Header
	Media            *PublishingMedia
}

// LinkedInDMSUploadEvidence contains no handle, signed URL, query, credential,
// origin, response body, asset identifier, or provider session state.
type LinkedInDMSUploadEvidence struct {
	StatusCode int
	SHA256     string
}

type LinkedInAssetStateRequest struct {
	WorkspaceID      string
	ConnectionID     string
	ExpectedProvider Provider
	Handle           string
	Header           http.Header
}

type LinkedInAssetStateEvidence struct {
	State string
}

type LinkedInCreateAfterAssetAvailableRequest struct {
	WorkspaceID      string
	ConnectionID     string
	ExpectedProvider Provider
	Handle           string
	Path             string
	Header           http.Header
	// Body is a LinkedIn create template. content.media.id must be absent;
	// F5 injects the registered asset identifier after AVAILABLE is proven.
	Body []byte
}

type linkedinDMSProviderEvidence struct {
	UploadURL string `json:"upload_url"`
	AssetURN  string `json:"asset_urn"`
}

// InitializeLinkedInDMS executes initializeUpload and atomically registers its
// response as an opaque, connection-bound capability. URL, query, asset URN,
// and provider-selected expiry never cross the F5 public boundary.
func (executor *AuthenticatedExecutor) InitializeLinkedInDMS(
	ctx context.Context,
	request LinkedInDMSInitializeRequest,
) (LinkedInDMSHandle, error) {
	request.Header = request.Header.Clone()
	if err := executor.validateLinkedInHandleRequest(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
		request.ExpectedProvider,
		"",
	); err != nil {
		return LinkedInDMSHandle{}, err
	}
	owner, err := executor.linkedInInitializeOwner(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
	)
	if err != nil {
		return LinkedInDMSHandle{}, err
	}
	body, err := json.Marshal(map[string]any{
		"initializeUploadRequest": map[string]string{"owner": owner},
	})
	if err != nil {
		return LinkedInDMSHandle{}, ErrInvalidArgument
	}
	response, err := executor.execute(ctx, PublishingRequest{
		WorkspaceID:      request.WorkspaceID,
		ConnectionID:     request.ConnectionID,
		ExpectedProvider: ProviderLinkedIn,
		Method:           http.MethodPost,
		Path:             linkedinDMSInitializePath,
		Header:           request.Header,
		Body:             body,
	}, authenticatedExecuteOptions{allowLinkedInDMSInitialize: true})
	if err != nil {
		return LinkedInDMSHandle{}, err
	}
	now := executor.service.now().UTC()
	evidence, expiresAt, err := decodeLinkedInDMSInitializeResponse(
		response.Body,
		now,
	)
	if err != nil {
		return LinkedInDMSHandle{}, executorFailure(
			ExecutorFailureAmbiguous,
			"linkedin_initialize_evidence_invalid",
			0,
		)
	}
	handle, err := randomOpaqueID(32)
	if err != nil {
		return LinkedInDMSHandle{}, executorFailure(
			ExecutorFailureAmbiguous,
			"linkedin_handle_registration_failed",
			0,
		)
	}
	handleHash := digest(handle)
	plaintext, err := json.Marshal(evidence)
	if err != nil {
		return LinkedInDMSHandle{}, executorFailure(
			ExecutorFailureAmbiguous,
			"linkedin_handle_registration_failed",
			0,
		)
	}
	sealed, err := executor.service.cipher.Seal(
		plaintext,
		linkedinDMSAdditionalData(
			request.WorkspaceID,
			request.ConnectionID,
			handleHash,
		),
	)
	if err != nil {
		return LinkedInDMSHandle{}, executorFailure(
			ExecutorFailureAmbiguous,
			"linkedin_handle_registration_failed",
			0,
		)
	}
	err = executor.service.repository.SaveLinkedInDMSGrant(
		ctx,
		StoredLinkedInDMSGrant{
			HandleHash:         handleHash,
			WorkspaceID:        request.WorkspaceID,
			ConnectionID:       request.ConnectionID,
			Provider:           ProviderLinkedIn,
			EvidenceCiphertext: sealed,
			State:              LinkedInDMSGrantRegistered,
			CreatedAt:          now,
			ExpiresAt:          expiresAt,
		},
	)
	if err != nil {
		return LinkedInDMSHandle{}, executorFailure(
			ExecutorFailureAmbiguous,
			"linkedin_handle_registration_failed",
			0,
		)
	}
	return LinkedInDMSHandle{Handle: handle}, nil
}

// UploadLinkedInDMS resolves all provider evidence server-side from Handle,
// claims it once, pins the fixed origin, and streams the verified media.
func (executor *AuthenticatedExecutor) UploadLinkedInDMS(
	ctx context.Context,
	request LinkedInDMSUploadRequest,
) (LinkedInDMSUploadEvidence, error) {
	if request.Media == nil ||
		request.Media.Body == nil ||
		request.Media.Size < 0 ||
		request.Media.SHA256 == "" {
		return LinkedInDMSUploadEvidence{}, ErrInvalidArgument
	}
	request = snapshotLinkedInDMSUploadRequest(request)
	defer request.Media.close()
	if err := executor.validateLinkedInHandleRequest(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
		request.ExpectedProvider,
		request.Handle,
	); err != nil {
		return LinkedInDMSUploadEvidence{}, err
	}
	prepared := PublishingRequest{
		WorkspaceID:      request.WorkspaceID,
		ConnectionID:     request.ConnectionID,
		ExpectedProvider: ProviderLinkedIn,
		Method:           http.MethodPut,
		Path:             "/linkedin-dms-upload",
		Header:           request.Header,
		Media:            request.Media,
	}
	_, body, verifier, err := executor.prepareRequest(prepared)
	if err != nil {
		return LinkedInDMSUploadEvidence{}, err
	}
	if err = validateLinkedInDMSUploadHeaders(request.Header); err != nil {
		return LinkedInDMSUploadEvidence{}, err
	}
	grant, leaseID, err := executor.claimLinkedInDMSGrant(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
		request.Handle,
		LinkedInDMSGrantRegistered,
		LinkedInDMSGrantUploading,
	)
	if err != nil {
		return LinkedInDMSUploadEvidence{}, err
	}
	evidence, err := executor.openLinkedInDMSEvidence(grant)
	if err != nil {
		return LinkedInDMSUploadEvidence{}, executor.failLinkedInDMSOperation(
			ctx,
			grant,
			LinkedInDMSGrantUploading,
			leaseID,
			executorFailure(
				ExecutorFailurePermanent,
				"linkedin_handle_evidence_invalid",
				0,
			),
		)
	}
	target, err := validateLinkedInDMSUploadURL(evidence.UploadURL)
	if err != nil {
		return LinkedInDMSUploadEvidence{}, executor.failLinkedInDMSOperation(
			ctx,
			grant,
			LinkedInDMSGrantUploading,
			leaseID,
			executorFailure(
				ExecutorFailurePermanent,
				"linkedin_handle_evidence_invalid",
				0,
			),
		)
	}
	if executor.transport == nil {
		return LinkedInDMSUploadEvidence{}, executor.releaseLinkedInDMSUpload(
			ctx,
			grant,
			leaseID,
			executorFailure(
				ExecutorFailurePermanent,
				"provider_transport_not_configured",
				0,
			),
		)
	}
	if err = executor.transport.PinOrigin(ctx, linkedinDMSUploadOrigin); err != nil {
		return LinkedInDMSUploadEvidence{}, executor.releaseLinkedInDMSUpload(
			ctx,
			grant,
			leaseID,
			executorFailure(
				ExecutorFailureTemporary,
				"provider_dns_pin_failed",
				0,
			),
		)
	}
	token, err := executor.service.AccessToken(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
	)
	if err != nil {
		return LinkedInDMSUploadEvidence{}, executor.releaseLinkedInDMSUpload(
			ctx,
			grant,
			leaseID,
			redactExecutorError(err, http.MethodPut, 0),
		)
	}
	if !executor.service.now().UTC().Before(grant.ExpiresAt) {
		return LinkedInDMSUploadEvidence{}, executor.failLinkedInDMSOperation(
			ctx,
			grant,
			LinkedInDMSGrantUploading,
			leaseID,
			linkedinDMSExpiredFailure(),
		)
	}
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPut,
		target.String(),
		body,
	)
	if err != nil {
		return LinkedInDMSUploadEvidence{}, executor.releaseLinkedInDMSUpload(
			ctx,
			grant,
			leaseID,
			ErrInvalidArgument,
		)
	}
	httpRequest.Header = request.Header.Clone()
	if httpRequest.Header == nil {
		httpRequest.Header = make(http.Header)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	httpRequest.ContentLength = request.Media.Size
	grant, err = executor.transitionLinkedInDMSGrant(
		ctx,
		grant,
		LinkedInDMSGrantUploading,
		LinkedInDMSGrantUploadSending,
		leaseID,
		"",
		nil,
	)
	if err != nil {
		return LinkedInDMSUploadEvidence{}, err
	}
	response, transportErr := executor.client.Do(httpRequest)
	if finalizeMedia(verifier) != nil {
		closeHTTPResponse(response)
		return LinkedInDMSUploadEvidence{}, executor.failLinkedInDMSOperation(
			ctx,
			grant,
			LinkedInDMSGrantUploadSending,
			"",
			executorFailure(
				ExecutorFailureAmbiguous,
				"provider_outcome_ambiguous",
				0,
			),
		)
	}
	if transportErr != nil {
		return LinkedInDMSUploadEvidence{}, executor.failLinkedInDMSOperation(
			ctx,
			grant,
			LinkedInDMSGrantUploadSending,
			"",
			executorFailure(
				ExecutorFailureAmbiguous,
				"provider_outcome_ambiguous",
				0,
			),
		)
	}
	status, finishErr := executor.finishLinkedInDMSUpload(
		response,
		token,
		evidence.UploadURL,
		target.RawQuery,
	)
	if finishErr != nil {
		return LinkedInDMSUploadEvidence{}, executor.failLinkedInDMSOperation(
			ctx,
			grant,
			LinkedInDMSGrantUploadSending,
			"",
			finishErr,
		)
	}
	if _, err = executor.service.repository.TransitionLinkedInDMSGrant(
		ctx,
		LinkedInDMSGrantTransition{
			HandleHash:      grant.HandleHash,
			WorkspaceID:     grant.WorkspaceID,
			ConnectionID:    grant.ConnectionID,
			FromState:       LinkedInDMSGrantUploadSending,
			ToState:         LinkedInDMSGrantUploaded,
			ExpectedLeaseID: "",
			Now:             executor.service.now().UTC(),
		},
	); err != nil {
		return LinkedInDMSUploadEvidence{}, executorFailure(
			ExecutorFailureAmbiguous,
			"linkedin_upload_state_persistence_failed",
			0,
		)
	}
	return LinkedInDMSUploadEvidence{
		StatusCode: status,
		SHA256:     request.Media.SHA256,
	}, nil
}

// InspectLinkedInAsset resolves the asset only from the persisted handle and
// returns a single normalized state.
func (executor *AuthenticatedExecutor) InspectLinkedInAsset(
	ctx context.Context,
	request LinkedInAssetStateRequest,
) (LinkedInAssetStateEvidence, error) {
	request.Header = request.Header.Clone()
	if err := executor.validateLinkedInHandleRequest(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
		request.ExpectedProvider,
		request.Handle,
	); err != nil {
		return LinkedInAssetStateEvidence{}, err
	}
	grant, evidence, err := executor.loadLinkedInDMSEvidence(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
		request.Handle,
	)
	if err != nil {
		return LinkedInAssetStateEvidence{}, err
	}
	if grant.State != LinkedInDMSGrantUploaded {
		return LinkedInAssetStateEvidence{}, ErrInvalidState
	}
	return executor.inspectLinkedInAssetEvidence(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
		request.Header,
		evidence,
	)
}

// ExecuteLinkedInCreateAfterAssetAvailable checks AVAILABLE, claims the handle
// atomically, injects the registered asset server-side, and consumes the handle
// after the single create attempt.
func (executor *AuthenticatedExecutor) ExecuteLinkedInCreateAfterAssetAvailable(
	ctx context.Context,
	request LinkedInCreateAfterAssetAvailableRequest,
) (PublishingResponse, LinkedInAssetStateEvidence, error) {
	request.Header = request.Header.Clone()
	request.Body = append([]byte(nil), request.Body...)
	if err := executor.validateLinkedInHandleRequest(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
		request.ExpectedProvider,
		request.Handle,
	); err != nil {
		return PublishingResponse{}, LinkedInAssetStateEvidence{}, err
	}
	if request.Path != "/rest/posts" {
		return PublishingResponse{}, LinkedInAssetStateEvidence{}, ErrInvalidArgument
	}
	if request.Path == "" || len(request.Body) == 0 {
		return PublishingResponse{}, LinkedInAssetStateEvidence{}, ErrInvalidArgument
	}
	grant, evidence, err := executor.loadLinkedInDMSEvidence(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
		request.Handle,
	)
	if err != nil {
		return PublishingResponse{}, LinkedInAssetStateEvidence{}, err
	}
	if grant.State != LinkedInDMSGrantUploaded &&
		grant.State != LinkedInDMSGrantCreating &&
		grant.State != LinkedInDMSGrantCreateSending {
		return PublishingResponse{}, LinkedInAssetStateEvidence{}, ErrInvalidState
	}
	createBody, err := linkedInCreateBodyWithAsset(
		request.Body,
		evidence.AssetURN,
	)
	if err != nil {
		return PublishingResponse{}, LinkedInAssetStateEvidence{}, err
	}
	stateEvidence, err := executor.inspectLinkedInAssetEvidence(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
		linkedInProtocolHeaders(request.Header),
		evidence,
	)
	if err != nil {
		return PublishingResponse{}, stateEvidence, err
	}
	if stateEvidence.State != "AVAILABLE" {
		return PublishingResponse{}, stateEvidence, executorFailure(
			ExecutorFailureTemporary,
			"linkedin_asset_not_available",
			0,
		)
	}
	grant, leaseID, err := executor.claimLinkedInDMSGrant(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
		request.Handle,
		LinkedInDMSGrantUploaded,
		LinkedInDMSGrantCreating,
	)
	if err != nil {
		return PublishingResponse{}, stateEvidence, err
	}
	grant, err = executor.transitionLinkedInDMSGrant(
		ctx,
		grant,
		LinkedInDMSGrantCreating,
		LinkedInDMSGrantCreateSending,
		leaseID,
		"",
		nil,
	)
	if err != nil {
		return PublishingResponse{}, stateEvidence, err
	}
	response, executeErr := executor.execute(ctx, PublishingRequest{
		WorkspaceID:      request.WorkspaceID,
		ConnectionID:     request.ConnectionID,
		ExpectedProvider: ProviderLinkedIn,
		Method:           http.MethodPost,
		Path:             request.Path,
		Header:           request.Header,
		Body:             createBody,
	}, authenticatedExecuteOptions{allowLinkedInMediaCreate: true})
	toState := LinkedInDMSGrantConsumed
	if executeErr != nil {
		toState = LinkedInDMSGrantFailed
	}
	if _, err = executor.service.repository.TransitionLinkedInDMSGrant(
		ctx,
		LinkedInDMSGrantTransition{
			HandleHash:      grant.HandleHash,
			WorkspaceID:     grant.WorkspaceID,
			ConnectionID:    grant.ConnectionID,
			FromState:       LinkedInDMSGrantCreateSending,
			ToState:         toState,
			ExpectedLeaseID: "",
			Now:             executor.service.now().UTC(),
		},
	); err != nil {
		return PublishingResponse{}, stateEvidence, executorFailure(
			ExecutorFailureAmbiguous,
			"linkedin_create_state_persistence_failed",
			0,
		)
	}
	return response, stateEvidence, executeErr
}

func (executor *AuthenticatedExecutor) validateLinkedInHandleRequest(
	ctx context.Context,
	workspaceID, connectionID string,
	expectedProvider Provider,
	handle string,
) error {
	if executor == nil || executor.service == nil ||
		strings.TrimSpace(workspaceID) == "" ||
		strings.TrimSpace(connectionID) == "" ||
		expectedProvider == "" {
		return ErrInvalidArgument
	}
	if handle != "" && strings.TrimSpace(handle) != handle {
		return ErrInvalidArgument
	}
	if expectedProvider != ProviderLinkedIn {
		return ErrInvalidState
	}
	return executor.validateLinkedInConnection(ctx, workspaceID, connectionID)
}

func snapshotLinkedInDMSUploadRequest(
	request LinkedInDMSUploadRequest,
) LinkedInDMSUploadRequest {
	snapshot := request
	snapshot.Header = request.Header.Clone()
	snapshot.Media = &PublishingMedia{
		Body:   request.Media.Body,
		Size:   request.Media.Size,
		SHA256: request.Media.SHA256,
	}
	return snapshot
}

func decodeLinkedInDMSInitializeResponse(
	body []byte,
	now time.Time,
) (linkedinDMSProviderEvidence, time.Time, error) {
	var response struct {
		Value struct {
			UploadURL          string          `json:"uploadUrl"`
			AssetURN           string          `json:"image"`
			UploadURLExpiresAt json.RawMessage `json:"uploadUrlExpiresAt"`
		} `json:"value"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&response); err != nil {
		return linkedinDMSProviderEvidence{}, time.Time{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return linkedinDMSProviderEvidence{}, time.Time{}, ErrInvalidState
	}
	if _, err := validateLinkedInDMSUploadURL(response.Value.UploadURL); err != nil {
		return linkedinDMSProviderEvidence{}, time.Time{}, err
	}
	if !validLinkedInAssetURN(response.Value.AssetURN) {
		return linkedinDMSProviderEvidence{}, time.Time{}, ErrInvalidState
	}
	expiresAt := now.Add(maximumLinkedInDMSUploadLifetime)
	if len(response.Value.UploadURLExpiresAt) != 0 &&
		string(response.Value.UploadURLExpiresAt) != "null" {
		raw := strings.TrimSpace(string(response.Value.UploadURLExpiresAt))
		milliseconds, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return linkedinDMSProviderEvidence{}, time.Time{}, ErrInvalidState
		}
		providerExpiry := time.UnixMilli(milliseconds).UTC()
		if !now.Before(providerExpiry) {
			return linkedinDMSProviderEvidence{}, time.Time{}, ErrFlowExpired
		}
		if providerExpiry.Before(expiresAt) {
			expiresAt = providerExpiry
		}
	}
	return linkedinDMSProviderEvidence{
		UploadURL: response.Value.UploadURL,
		AssetURN:  response.Value.AssetURN,
	}, expiresAt, nil
}

func validateLinkedInDMSUploadURL(raw string) (*url.URL, error) {
	if raw == "" || strings.TrimSpace(raw) != raw ||
		strings.ContainsAny(raw, "\r\n\t") {
		return nil, invalidLinkedInDMSUploadURL()
	}
	target, err := url.Parse(raw)
	if err != nil ||
		target.Scheme != "https" ||
		target.Host != "www.linkedin.com" ||
		target.User != nil ||
		target.Opaque != "" ||
		target.Fragment != "" ||
		target.RawFragment != "" ||
		target.RawQuery == "" ||
		target.ForceQuery ||
		!strings.HasPrefix(target.EscapedPath(), linkedinDMSUploadPathPrefix) ||
		target.String() != raw {
		return nil, invalidLinkedInDMSUploadURL()
	}
	if err = validateCanonicalPublishingPath(target.EscapedPath()); err != nil {
		return nil, invalidLinkedInDMSUploadURL()
	}
	if err = validateAuthenticatedRequest(
		DynamicOAuthConfig{NetworkPolicy: DynamicNetworkPolicy{
			RejectRedirects:   true,
			ValidateAndPinDNS: true,
			MaxResponseBytes:  maximumAuthenticatedBody,
		}},
		AuthenticatedRequest{
			Method: http.MethodPut,
			Path:   target.EscapedPath(),
		},
	); err != nil {
		return nil, invalidLinkedInDMSUploadURL()
	}
	return target, nil
}

func invalidLinkedInDMSUploadURL() error {
	return fmt.Errorf(
		"%w: LinkedIn signed upload URL is invalid",
		ErrInvalidArgument,
	)
}

func (executor *AuthenticatedExecutor) validateLinkedInConnection(
	ctx context.Context,
	workspaceID, connectionID string,
) error {
	stored, err := executor.service.repository.GetCredential(
		ctx,
		workspaceID,
		connectionID,
	)
	if err != nil {
		return err
	}
	if stored.Status == StatusReconnectRequired {
		return &ExecutorFailure{
			Kind:      ExecutorFailureReconnect,
			Code:      "reconnect_required",
			Reconnect: true,
		}
	}
	if stored.Status == StatusRevoked {
		return executorFailure(
			ExecutorFailurePermanent,
			"connection_revoked",
			0,
		)
	}
	if stored.Provider != ProviderLinkedIn {
		return ErrInvalidState
	}
	if _, err = executor.service.availableAdapter(ProviderLinkedIn); err != nil {
		return redactExecutorError(err, http.MethodPut, 0)
	}
	return nil
}

func (executor *AuthenticatedExecutor) linkedInInitializeOwner(
	ctx context.Context,
	workspaceID, connectionID string,
) (string, error) {
	stored, err := executor.service.repository.GetCredential(
		ctx,
		workspaceID,
		connectionID,
	)
	if err != nil {
		return "", err
	}
	var prefix string
	switch stored.ResourceType {
	case ResourceLinkedInProfile:
		prefix = "urn:li:person:"
	case ResourceLinkedInPage:
		prefix = "urn:li:organization:"
	default:
		return "", ErrInvalidState
	}
	owner := stored.RemoteID
	if !strings.HasPrefix(owner, "urn:li:") {
		owner = prefix + owner
	}
	if !strings.HasPrefix(owner, prefix) ||
		!validLinkedInAssetURN(owner) {
		return "", ErrInvalidState
	}
	return owner, nil
}

func validateLinkedInDMSUploadHeaders(header http.Header) error {
	contentTypes := header.Values("Content-Type")
	if len(contentTypes) != 1 ||
		strings.TrimSpace(contentTypes[0]) == "" ||
		containsUnsafeText(contentTypes[0]) {
		return fmt.Errorf(
			"%w: LinkedIn signed upload Content-Type is required",
			ErrInvalidArgument,
		)
	}
	for name := range header {
		if !strings.EqualFold(name, "Content-Type") {
			return fmt.Errorf(
				"%w: LinkedIn signed upload header is forbidden",
				ErrInvalidArgument,
			)
		}
	}
	return nil
}

func (executor *AuthenticatedExecutor) loadLinkedInDMSEvidence(
	ctx context.Context,
	workspaceID, connectionID, handle string,
) (StoredLinkedInDMSGrant, linkedinDMSProviderEvidence, error) {
	if handle == "" {
		return StoredLinkedInDMSGrant{}, linkedinDMSProviderEvidence{}, ErrInvalidArgument
	}
	grant, err := executor.service.repository.GetLinkedInDMSGrant(
		ctx,
		workspaceID,
		connectionID,
		digest(handle),
		executor.service.now().UTC(),
	)
	if err != nil {
		return StoredLinkedInDMSGrant{}, linkedinDMSProviderEvidence{},
			linkedinDMSGrantError(err)
	}
	evidence, err := executor.openLinkedInDMSEvidence(grant)
	return grant, evidence, err
}

func (executor *AuthenticatedExecutor) openLinkedInDMSEvidence(
	grant StoredLinkedInDMSGrant,
) (linkedinDMSProviderEvidence, error) {
	plaintext, err := executor.service.cipher.Open(
		grant.EvidenceCiphertext,
		linkedinDMSAdditionalData(
			grant.WorkspaceID,
			grant.ConnectionID,
			grant.HandleHash,
		),
	)
	if err != nil {
		return linkedinDMSProviderEvidence{}, ErrInvalidState
	}
	var evidence linkedinDMSProviderEvidence
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&evidence); err != nil {
		return linkedinDMSProviderEvidence{}, ErrInvalidState
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return linkedinDMSProviderEvidence{}, ErrInvalidState
	}
	if _, err = validateLinkedInDMSUploadURL(evidence.UploadURL); err != nil ||
		!validLinkedInAssetURN(evidence.AssetURN) {
		return linkedinDMSProviderEvidence{}, ErrInvalidState
	}
	return evidence, nil
}

func linkedinDMSAdditionalData(
	workspaceID, connectionID, handleHash string,
) []byte {
	return []byte(
		"f05|linkedin-dms|" + workspaceID + "\x00" +
			connectionID + "\x00" + string(ProviderLinkedIn) + "\x00" +
			handleHash,
	)
}

func (executor *AuthenticatedExecutor) claimLinkedInDMSGrant(
	ctx context.Context,
	workspaceID, connectionID, handle string,
	baseState, operationState LinkedInDMSGrantState,
) (StoredLinkedInDMSGrant, string, error) {
	if handle == "" {
		return StoredLinkedInDMSGrant{}, "", ErrInvalidArgument
	}
	leaseID, err := randomOpaqueID(18)
	if err != nil {
		return StoredLinkedInDMSGrant{}, "", executorFailure(
			ExecutorFailureTemporary,
			"linkedin_handle_claim_failed",
			0,
		)
	}
	now := executor.service.now().UTC()
	lockedUntil := now.Add(executor.service.refreshLockTTL)
	command := LinkedInDMSGrantTransition{
		HandleHash:     digest(handle),
		WorkspaceID:    workspaceID,
		ConnectionID:   connectionID,
		FromState:      baseState,
		ToState:        operationState,
		NewLeaseID:     leaseID,
		NewLockedUntil: &lockedUntil,
		Now:            now,
	}
	grant, err := executor.service.repository.TransitionLinkedInDMSGrant(
		ctx,
		command,
	)
	if err == nil {
		return grant, leaseID, nil
	}
	if !errors.Is(err, ErrInvalidState) {
		return StoredLinkedInDMSGrant{}, "", linkedinDMSGrantError(err)
	}
	existing, getErr := executor.service.repository.GetLinkedInDMSGrant(
		ctx,
		workspaceID,
		connectionID,
		digest(handle),
		now,
	)
	if getErr != nil {
		return StoredLinkedInDMSGrant{}, "", linkedinDMSGrantError(getErr)
	}
	switch existing.State {
	case operationState:
		if existing.LockedUntil == nil {
			return StoredLinkedInDMSGrant{}, "", ErrInvalidState
		}
		if existing.LockedUntil.After(now) {
			return StoredLinkedInDMSGrant{}, "", executorFailure(
				ExecutorFailureTemporary,
				"linkedin_handle_in_progress",
				existing.LockedUntil.Sub(now),
			)
		}
		command.FromState = operationState
		command.ExpectedLeaseID = existing.LeaseID
		grant, err = executor.service.repository.TransitionLinkedInDMSGrant(
			ctx,
			command,
		)
		if err != nil {
			return StoredLinkedInDMSGrant{}, "", linkedinDMSGrantError(err)
		}
		return grant, leaseID, nil
	case LinkedInDMSGrantUploadSending, LinkedInDMSGrantCreateSending:
		return StoredLinkedInDMSGrant{}, "", executorFailure(
			ExecutorFailureAmbiguous,
			"provider_outcome_ambiguous",
			0,
		)
	default:
		return StoredLinkedInDMSGrant{}, "", ErrInvalidState
	}
}

func (executor *AuthenticatedExecutor) transitionLinkedInDMSGrant(
	ctx context.Context,
	grant StoredLinkedInDMSGrant,
	fromState, toState LinkedInDMSGrantState,
	expectedLeaseID, newLeaseID string,
	newLockedUntil *time.Time,
) (StoredLinkedInDMSGrant, error) {
	updated, err := executor.service.repository.TransitionLinkedInDMSGrant(
		ctx,
		LinkedInDMSGrantTransition{
			HandleHash:      grant.HandleHash,
			WorkspaceID:     grant.WorkspaceID,
			ConnectionID:    grant.ConnectionID,
			FromState:       fromState,
			ToState:         toState,
			ExpectedLeaseID: expectedLeaseID,
			NewLeaseID:      newLeaseID,
			NewLockedUntil:  newLockedUntil,
			Now:             executor.service.now().UTC(),
		},
	)
	if err != nil {
		return StoredLinkedInDMSGrant{}, linkedinDMSGrantError(err)
	}
	return updated, nil
}

func linkedinDMSGrantError(err error) error {
	if errors.Is(err, ErrFlowExpired) {
		return linkedinDMSExpiredFailure()
	}
	if errors.Is(err, ErrResourceNotFound) ||
		errors.Is(err, ErrInvalidState) {
		return ErrInvalidState
	}
	return redactExecutorError(err, http.MethodPut, 0)
}

func linkedinDMSExpiredFailure() *ExecutorFailure {
	return executorFailure(
		ExecutorFailurePermanent,
		"linkedin_dms_handle_expired",
		0,
	)
}

func (executor *AuthenticatedExecutor) releaseLinkedInDMSUpload(
	ctx context.Context,
	grant StoredLinkedInDMSGrant,
	leaseID string,
	outcome error,
) error {
	_, err := executor.service.repository.TransitionLinkedInDMSGrant(
		ctx,
		LinkedInDMSGrantTransition{
			HandleHash:      grant.HandleHash,
			WorkspaceID:     grant.WorkspaceID,
			ConnectionID:    grant.ConnectionID,
			FromState:       LinkedInDMSGrantUploading,
			ToState:         LinkedInDMSGrantRegistered,
			ExpectedLeaseID: leaseID,
			Now:             executor.service.now().UTC(),
		},
	)
	if err != nil {
		return executorFailure(
			ExecutorFailurePermanent,
			"linkedin_upload_state_persistence_failed",
			0,
		)
	}
	return outcome
}

func (executor *AuthenticatedExecutor) failLinkedInDMSOperation(
	ctx context.Context,
	grant StoredLinkedInDMSGrant,
	fromState LinkedInDMSGrantState,
	leaseID string,
	outcome error,
) error {
	_, err := executor.service.repository.TransitionLinkedInDMSGrant(
		ctx,
		LinkedInDMSGrantTransition{
			HandleHash:      grant.HandleHash,
			WorkspaceID:     grant.WorkspaceID,
			ConnectionID:    grant.ConnectionID,
			FromState:       fromState,
			ToState:         LinkedInDMSGrantFailed,
			ExpectedLeaseID: leaseID,
			Now:             executor.service.now().UTC(),
		},
	)
	if err != nil {
		return executorFailure(
			ExecutorFailureAmbiguous,
			"linkedin_operation_state_persistence_failed",
			0,
		)
	}
	return outcome
}

func (executor *AuthenticatedExecutor) finishLinkedInDMSUpload(
	response *http.Response,
	secrets ...string,
) (int, error) {
	if response == nil || response.Body == nil {
		return 0, executorFailure(
			ExecutorFailureAmbiguous,
			"provider_outcome_ambiguous",
			0,
		)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(
		response.Body,
		maximumAuthenticatedBody+1,
	))
	if err != nil || len(body) > maximumAuthenticatedBody {
		return 0, executorFailure(
			ExecutorFailureAmbiguous,
			"provider_outcome_ambiguous",
			0,
		)
	}
	sanitized := sanitizePublishingResponse(AuthenticatedResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       body,
	})
	if responseContainsSecret(sanitized, secrets...) {
		return 0, executorFailure(
			ExecutorFailurePermanent,
			"provider_response_redacted",
			0,
		)
	}
	retryAfter := parseRetryAfter(
		sanitized.Header.Get("Retry-After"),
		executor.service.now(),
	)
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return response.StatusCode, nil
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return 0, executorFailure(
			ExecutorFailurePermanent,
			"provider_redirect_rejected",
			0,
		)
	case response.StatusCode == http.StatusTooManyRequests:
		return 0, executorFailure(
			ExecutorFailureRateLimit,
			"provider_rate_limited",
			retryAfter,
		)
	case response.StatusCode >= 500:
		return 0, executorFailure(
			ExecutorFailureAmbiguous,
			"provider_outcome_ambiguous",
			retryAfter,
		)
	default:
		return 0, executorFailure(
			ExecutorFailurePermanent,
			"linkedin_signed_upload_rejected",
			0,
		)
	}
}

func validLinkedInAssetURN(value string) bool {
	if !strings.HasPrefix(value, "urn:li:") ||
		len(value) > 512 ||
		strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune(":._-", character)) {
			return false
		}
	}
	return true
}

func (executor *AuthenticatedExecutor) inspectLinkedInAssetEvidence(
	ctx context.Context,
	workspaceID, connectionID string,
	header http.Header,
	evidence linkedinDMSProviderEvidence,
) (LinkedInAssetStateEvidence, error) {
	response, err := executor.execute(ctx, PublishingRequest{
		WorkspaceID:      workspaceID,
		ConnectionID:     connectionID,
		ExpectedProvider: ProviderLinkedIn,
		Method:           http.MethodGet,
		Path:             "/rest/images/" + url.PathEscape(evidence.AssetURN),
		Header:           header,
	}, authenticatedExecuteOptions{allowLinkedInAssetStatus: true})
	if err != nil {
		return LinkedInAssetStateEvidence{}, err
	}
	state, err := decodeLinkedInAssetState(response.Body)
	if err != nil {
		return LinkedInAssetStateEvidence{}, executorFailure(
			ExecutorFailureTemporary,
			"linkedin_asset_state_invalid",
			0,
		)
	}
	return LinkedInAssetStateEvidence{State: state}, nil
}

func decodeLinkedInAssetState(body []byte) (string, error) {
	var response struct {
		Status string `json:"status"`
		Value  *struct {
			Status string `json:"status"`
		} `json:"value"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&response); err != nil {
		return "", err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", ErrInvalidState
	}
	state := response.Status
	if response.Value != nil {
		if state != "" && state != response.Value.Status {
			return "", ErrInvalidState
		}
		state = response.Value.Status
	}
	switch state {
	case "WAITING_UPLOAD", "PROCESSING", "AVAILABLE", "PROCESSING_FAILED":
		return state, nil
	default:
		return "", ErrInvalidState
	}
}

func linkedInProtocolHeaders(header http.Header) http.Header {
	safe := make(http.Header)
	for _, name := range []string{
		"LinkedIn-Version",
		"X-Restli-Protocol-Version",
	} {
		for _, value := range header.Values(name) {
			safe.Add(name, value)
		}
	}
	return safe
}

func linkedInCreateBodyWithAsset(
	body []byte,
	assetURN string,
) ([]byte, error) {
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, ErrInvalidArgument
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidArgument
	}
	content, ok := payload["content"].(map[string]any)
	if !ok {
		return nil, ErrInvalidArgument
	}
	media, ok := content["media"].(map[string]any)
	if !ok {
		return nil, ErrInvalidArgument
	}
	if _, supplied := media["id"]; supplied {
		return nil, ErrInvalidArgument
	}
	media["id"] = assetURN
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	return encoded, nil
}

func isLinkedInImagesEndpointRequest(request PublishingRequest) bool {
	target, err := url.ParseRequestURI(request.Path)
	if err != nil || target.IsAbs() || target.Host != "" {
		return false
	}
	normalized := path.Clean(target.Path)
	return normalized == "/rest/images" ||
		strings.HasPrefix(normalized, "/rest/images/")
}

func isCanonicalLinkedInDMSInitializeRequest(
	request PublishingRequest,
) bool {
	return request.Method == http.MethodPost &&
		request.Path == linkedinDMSInitializePath
}

func isCanonicalLinkedInAssetStatusRequest(
	request PublishingRequest,
) bool {
	target, err := url.ParseRequestURI(request.Path)
	return err == nil &&
		request.Method == http.MethodGet &&
		target.RawQuery == "" &&
		target.Fragment == "" &&
		target.EscapedPath() == target.Path &&
		strings.HasPrefix(target.Path, "/rest/images/") &&
		path.Clean(target.Path) == target.Path
}

func isCanonicalLinkedInMediaCreateRequest(
	request PublishingRequest,
) bool {
	return request.Method == http.MethodPost &&
		request.Path == "/rest/posts"
}

func hasLinkedInMediaCreatePayload(request PublishingRequest) bool {
	var payload struct {
		Content map[string]json.RawMessage `json:"content"`
	}
	if json.Unmarshal(request.Body, &payload) != nil {
		return false
	}
	for _, field := range []string{"media", "multiImage"} {
		if _, present := payload.Content[field]; present {
			return true
		}
	}
	return false
}
