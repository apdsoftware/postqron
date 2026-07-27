package entitlements

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

type RequestAuthenticator interface {
	AccountID(*http.Request) (string, bool)
}

type WorkspaceViewer interface {
	CanViewBilling(context.Context, string, string) (bool, error)
}

type HTTPHandler struct {
	service       *Service
	checkout      *CheckoutService
	portal        *PortalService
	changes       *SubscriptionChangeService
	webhook       http.Handler
	authenticator RequestAuthenticator
	viewer        WorkspaceViewer
}

func NewHTTPHandler(
	service *Service,
	checkout *CheckoutService,
	portal *PortalService,
	changes *SubscriptionChangeService,
	webhook http.Handler,
	authenticator RequestAuthenticator,
	viewer WorkspaceViewer,
) http.Handler {
	handler := &HTTPHandler{
		service:       service,
		checkout:      checkout,
		portal:        portal,
		changes:       changes,
		webhook:       webhook,
		authenticator: authenticator,
		viewer:        viewer,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/billing/plans", handler.plans)
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/billing",
		handler.overview,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/billing/checkout",
		handler.createCheckout,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/billing/portal",
		handler.createPortal,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/billing/subscription/preview",
		handler.previewSubscriptionChange,
	)
	mux.HandleFunc(
		"PATCH /api/v1/workspaces/{workspace_id}/billing/subscription",
		handler.applySubscriptionChange,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/billing/subscription/cancel",
		handler.cancelSubscription,
	)
	mux.Handle("POST /api/v1/billing/paddle/webhook", webhook)
	return mux
}

func (handler *HTTPHandler) previewSubscriptionChange(
	writer http.ResponseWriter,
	request *http.Request,
) {
	change, ok := handler.subscriptionChangeRequest(writer, request)
	if !ok {
		return
	}
	preview, err := handler.changes.Preview(request.Context(), change)
	if err != nil {
		handler.writeSubscriptionChangeError(writer, err)
		return
	}
	writeEntitlementJSON(writer, http.StatusOK, preview)
}

func (handler *HTTPHandler) applySubscriptionChange(
	writer http.ResponseWriter,
	request *http.Request,
) {
	change, ok := handler.subscriptionChangeRequest(writer, request)
	if !ok {
		return
	}
	if err := handler.changes.Apply(request.Context(), change); err != nil {
		handler.writeSubscriptionChangeError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *HTTPHandler) cancelSubscription(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeEntitlementError(writer, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var payload struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !decodeEntitlementRequest(writer, request, &payload) {
		return
	}
	err := handler.changes.Cancel(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		payload.IdempotencyKey,
	)
	if err != nil {
		handler.writeSubscriptionChangeError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *HTTPHandler) subscriptionChangeRequest(
	writer http.ResponseWriter,
	request *http.Request,
) (SubscriptionChangeRequest, bool) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeEntitlementError(writer, http.StatusUnauthorized, "unauthenticated")
		return SubscriptionChangeRequest{}, false
	}
	var payload struct {
		Plan           PlanCode        `json:"plan"`
		Interval       BillingInterval `json:"interval"`
		Channels       *int64          `json:"channels"`
		IdempotencyKey string          `json:"idempotency_key"`
	}
	if !decodeEntitlementRequest(writer, request, &payload) {
		return SubscriptionChangeRequest{}, false
	}
	return SubscriptionChangeRequest{
		WorkspaceID:    request.PathValue("workspace_id"),
		AccountID:      accountID,
		Plan:           payload.Plan,
		Interval:       payload.Interval,
		Channels:       payload.Channels,
		IdempotencyKey: payload.IdempotencyKey,
	}, true
}

func (handler *HTTPHandler) writeSubscriptionChangeError(
	writer http.ResponseWriter,
	err error,
) {
	switch {
	case errors.Is(err, ErrOwnerRequired):
		writeEntitlementError(writer, http.StatusForbidden, "owner_required")
	case errors.Is(err, ErrUnknownPlan),
		errors.Is(err, ErrInvalidInterval),
		errors.Is(err, ErrInvalidChannels),
		errors.Is(err, ErrFreePlan),
		errors.Is(err, ErrInvalidIdempotencyKey),
		errors.Is(err, ErrMixedSubscriptionChange):
		writeEntitlementError(writer, http.StatusBadRequest, "invalid_request")
	default:
		writeEntitlementError(writer, http.StatusBadGateway, "billing_unavailable")
	}
}

func decodeEntitlementRequest(
	writer http.ResponseWriter,
	request *http.Request,
	payload any,
) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(payload); err != nil {
		writeEntitlementError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func (handler *HTTPHandler) createPortal(
	writer http.ResponseWriter,
	request *http.Request,
) {
	workspaceID := request.PathValue("workspace_id")
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeEntitlementError(writer, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var payload struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeEntitlementError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	session, err := handler.portal.Create(request.Context(), PortalRequest{
		WorkspaceID:    workspaceID,
		AccountID:      accountID,
		IdempotencyKey: payload.IdempotencyKey,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrOwnerRequired):
			writeEntitlementError(writer, http.StatusForbidden, "owner_required")
		case errors.Is(err, ErrInvalidIdempotencyKey):
			writeEntitlementError(writer, http.StatusBadRequest, "invalid_request")
		default:
			writeEntitlementError(writer, http.StatusBadGateway, "billing_unavailable")
		}
		return
	}
	writeEntitlementJSON(writer, http.StatusCreated, session)
}

func (handler *HTTPHandler) plans(writer http.ResponseWriter, _ *http.Request) {
	writeEntitlementJSON(writer, http.StatusOK, map[string]any{
		"provider":        "paddle",
		"catalog_version": CatalogVersion,
		"currency":        "EUR",
		"plans":           PublicPlans(),
	})
}

func (handler *HTTPHandler) overview(writer http.ResponseWriter, request *http.Request) {
	workspaceID := request.PathValue("workspace_id")
	allowed := handler.authorizeView(request, workspaceID)
	if !allowed {
		writeEntitlementError(writer, http.StatusForbidden, "forbidden")
		return
	}
	result, err := handler.service.GetOverview(request.Context(), workspaceID)
	if err != nil {
		if errors.Is(err, ErrEntitlementUnavailable) {
			writeEntitlementError(
				writer,
				http.StatusServiceUnavailable,
				"entitlement_unavailable",
			)
			return
		}
		writeEntitlementError(writer, http.StatusInternalServerError, "internal_error")
		return
	}
	writeEntitlementJSON(writer, http.StatusOK, result)
}

func (handler *HTTPHandler) createCheckout(
	writer http.ResponseWriter,
	request *http.Request,
) {
	workspaceID := request.PathValue("workspace_id")
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeEntitlementError(writer, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var payload struct {
		Plan           PlanCode        `json:"plan"`
		Interval       BillingInterval `json:"interval"`
		Channels       *int64          `json:"channels"`
		IdempotencyKey string          `json:"idempotency_key"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeEntitlementError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	session, err := handler.checkout.Create(request.Context(), CheckoutRequest{
		WorkspaceID:    workspaceID,
		AccountID:      accountID,
		Plan:           payload.Plan,
		Interval:       payload.Interval,
		Channels:       payload.Channels,
		IdempotencyKey: payload.IdempotencyKey,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrOwnerRequired):
			writeEntitlementError(writer, http.StatusForbidden, "owner_required")
		case errors.Is(err, ErrUnknownPlan),
			errors.Is(err, ErrInvalidInterval),
			errors.Is(err, ErrInvalidChannels),
			errors.Is(err, ErrFreePlan),
			errors.Is(err, ErrInvalidIdempotencyKey):
			writeEntitlementError(writer, http.StatusBadRequest, "invalid_request")
		default:
			writeEntitlementError(writer, http.StatusBadGateway, "billing_unavailable")
		}
		return
	}
	writeEntitlementJSON(writer, http.StatusCreated, session)
}

func (handler *HTTPHandler) authorizeView(
	request *http.Request,
	workspaceID string,
) bool {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		return false
	}
	allowed, err := handler.viewer.CanViewBilling(
		request.Context(),
		workspaceID,
		accountID,
	)
	return err == nil && allowed
}

func writeEntitlementJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeEntitlementError(writer http.ResponseWriter, status int, code string) {
	writeEntitlementJSON(writer, status, map[string]string{"error": code})
}
