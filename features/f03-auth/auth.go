package auth

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	OnboardingEventType    = "auth.account.onboarding-required"
	OnboardingEventVersion = 1
)

var legalVersionPattern = regexp.MustCompile(`^[1-9]\d*\.\d+$`)

type Config struct {
	Store            TransactionStore
	Sealer           Sealer
	Providers        map[Provider]ProviderAdapter
	Now              func() time.Time
	AttemptTTL       time.Duration
	SessionTTL       time.Duration
	LinkReauthWindow time.Duration
}

type Service struct {
	store            TransactionStore
	sealer           Sealer
	providers        map[Provider]ProviderAdapter
	now              func() time.Time
	attemptTTL       time.Duration
	sessionTTL       time.Duration
	linkReauthWindow time.Duration
}

func NewService(config Config) (*Service, error) {
	if config.Store == nil {
		return nil, errors.New("auth store is required")
	}
	if config.Sealer == nil {
		return nil, errors.New("auth sealer is required")
	}
	providers := make(map[Provider]ProviderAdapter, len(SupportedProviders))
	for _, provider := range SupportedProviders {
		adapter, exists := config.Providers[provider]
		if !exists || adapter == nil {
			return nil, fmt.Errorf("%s provider adapter is required", provider)
		}
		if err := validateProviderConfig(provider, adapter.Config()); err != nil {
			return nil, err
		}
		providers[provider] = adapter
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.AttemptTTL == 0 {
		config.AttemptTTL = 10 * time.Minute
	}
	if config.SessionTTL == 0 {
		config.SessionTTL = 30 * 24 * time.Hour
	}
	if config.LinkReauthWindow == 0 {
		config.LinkReauthWindow = 5 * time.Minute
	}
	if config.AttemptTTL <= 0 || config.SessionTTL <= 0 || config.LinkReauthWindow <= 0 {
		return nil, errors.New("auth durations must be positive")
	}
	return &Service{
		store:            config.Store,
		sealer:           config.Sealer,
		providers:        providers,
		now:              config.Now,
		attemptTTL:       config.AttemptTTL,
		sessionTTL:       config.SessionTTL,
		linkReauthWindow: config.LinkReauthWindow,
	}, nil
}

func (s *Service) Begin(ctx context.Context, request BeginRequest) (Authorization, error) {
	if !isSupportedProvider(request.Provider) {
		return Authorization{}, newError(
			CodeUnsupportedProvider,
			"Provider di accesso non supportato.",
			false,
			nil,
		)
	}
	if err := validateReturnTo(request.ReturnTo); err != nil {
		return Authorization{}, err
	}
	if err := validateConsentShape(request.Consents); err != nil {
		return Authorization{}, err
	}
	contractCountry := strings.ToUpper(strings.TrimSpace(request.ContractCountry))
	if len(request.Consents) > 0 && contractCountry != "IT" {
		return Authorization{}, newError(
			CodeCountryNotSupported,
			"Postqron è disponibile per account con paese contrattuale Italia.",
			false,
			nil,
		)
	}
	return s.begin(ctx, beginParameters{
		provider:        request.Provider,
		intent:          IntentLogin,
		returnTo:        normalizedReturnTo(request.ReturnTo),
		contractCountry: contractCountry,
		consents:        request.Consents,
	})
}

func (s *Service) BeginLink(
	ctx context.Context,
	request BeginLinkRequest,
) (Authorization, error) {
	if !isSupportedProvider(request.Provider) {
		return Authorization{}, newError(
			CodeUnsupportedProvider,
			"Provider di accesso non supportato.",
			false,
			nil,
		)
	}
	if err := validateReturnTo(request.ReturnTo); err != nil {
		return Authorization{}, err
	}
	principal, authenticatedAt, err := s.authenticate(ctx, request.SessionToken)
	if err != nil {
		return Authorization{}, err
	}
	now := s.now().UTC()
	if now.Sub(authenticatedAt) > s.linkReauthWindow {
		return Authorization{}, newError(
			CodeReauthenticationRequired,
			"Accedi di nuovo prima di collegare un provider.",
			true,
			nil,
		)
	}
	return s.begin(ctx, beginParameters{
		provider:              request.Provider,
		intent:                IntentLink,
		targetAccountID:       principal.AccountID,
		boundSessionTokenHash: hashToken(request.SessionToken),
		returnTo:              normalizedReturnTo(request.ReturnTo),
	})
}

type beginParameters struct {
	provider              Provider
	intent                FlowIntent
	targetAccountID       string
	boundSessionTokenHash string
	returnTo              string
	contractCountry       string
	consents              []ConsentReceipt
}

func (s *Service) begin(
	ctx context.Context,
	parameters beginParameters,
) (Authorization, error) {
	state, err := randomToken(32)
	if err != nil {
		return Authorization{}, wrapInternal("generate OAuth state", err)
	}
	verifier, err := randomToken(64)
	if err != nil {
		return Authorization{}, wrapInternal("generate PKCE verifier", err)
	}
	nonce, err := randomToken(32)
	if err != nil {
		return Authorization{}, wrapInternal("generate OIDC nonce", err)
	}
	verifierCiphertext, err := s.sealer.Seal([]byte(verifier))
	if err != nil {
		return Authorization{}, wrapInternal("seal PKCE verifier", err)
	}
	nonceCiphertext, err := s.sealer.Seal([]byte(nonce))
	if err != nil {
		return Authorization{}, wrapInternal("seal OIDC nonce", err)
	}
	id, err := randomToken(18)
	if err != nil {
		return Authorization{}, wrapInternal("generate attempt id", err)
	}
	correlationID, err := randomToken(18)
	if err != nil {
		return Authorization{}, wrapInternal("generate correlation id", err)
	}
	now := s.now().UTC()
	attempt := OAuthAttempt{
		ID:                     id,
		StateHash:              hashToken(state),
		PKCEVerifierCiphertext: verifierCiphertext,
		NonceCiphertext:        nonceCiphertext,
		Provider:               parameters.provider,
		Intent:                 parameters.intent,
		TargetAccountID:        parameters.targetAccountID,
		BoundSessionTokenHash:  parameters.boundSessionTokenHash,
		ReturnTo:               parameters.returnTo,
		ContractCountry:        parameters.contractCountry,
		Consents:               append([]ConsentReceipt(nil), parameters.consents...),
		CorrelationID:          correlationID,
		Status:                 AttemptPending,
		CreatedAt:              now,
		ExpiresAt:              now.Add(s.attemptTTL),
	}
	if err := s.store.SaveAttempt(ctx, attempt); err != nil {
		return Authorization{}, wrapInternal("save OAuth attempt", err)
	}
	authorizationURL, err := buildAuthorizationURL(
		s.providers[parameters.provider].Config(),
		state,
		pkceChallenge(verifier),
		nonce,
	)
	if err != nil {
		_ = s.store.FailAttempt(ctx, attempt.ID, now)
		return Authorization{}, wrapInternal("build provider authorization URL", err)
	}
	return Authorization{URL: authorizationURL, ExpiresAt: attempt.ExpiresAt}, nil
}

func (s *Service) Callback(
	ctx context.Context,
	request CallbackRequest,
) (CallbackResult, error) {
	if strings.TrimSpace(request.State) == "" {
		return CallbackResult{}, newError(
			CodeInvalidState,
			"Richiesta di accesso non valida. Riavvia il login.",
			false,
			nil,
		)
	}
	now := s.now().UTC()
	attempt, err := s.store.ClaimAttempt(ctx, hashToken(request.State), now)
	if err != nil {
		return CallbackResult{}, err
	}
	if request.ProviderError != "" {
		if failErr := s.store.FailAttempt(ctx, attempt.ID, now); failErr != nil {
			return CallbackResult{}, wrapInternal("fail denied OAuth attempt", failErr)
		}
		return CallbackResult{}, newError(
			CodeProviderDenied,
			"Accesso annullato dal provider. Puoi riprovare.",
			true,
			nil,
		)
	}
	if strings.TrimSpace(request.Code) == "" {
		_ = s.store.FailAttempt(ctx, attempt.ID, now)
		return CallbackResult{}, newError(
			CodeInvalidRequest,
			"Il provider non ha restituito un codice valido. Riprova.",
			true,
			nil,
		)
	}
	verifier, err := s.sealer.Open(attempt.PKCEVerifierCiphertext)
	if err != nil {
		_ = s.store.FailAttempt(ctx, attempt.ID, now)
		return CallbackResult{}, wrapInternal("open PKCE verifier", err)
	}
	nonce, err := s.sealer.Open(attempt.NonceCiphertext)
	if err != nil {
		_ = s.store.FailAttempt(ctx, attempt.ID, now)
		return CallbackResult{}, wrapInternal("open OIDC nonce", err)
	}
	adapter := s.providers[attempt.Provider]
	identity, err := adapter.Exchange(ctx, ExchangeRequest{
		Code:          request.Code,
		RedirectURL:   adapter.Config().RedirectURL,
		PKCEVerifier:  string(verifier),
		ExpectedNonce: string(nonce),
	})
	if err != nil {
		return CallbackResult{}, s.handleProviderFailure(ctx, attempt.ID, err, now)
	}
	if strings.TrimSpace(identity.Subject) == "" {
		_ = s.store.FailAttempt(ctx, attempt.ID, now)
		return CallbackResult{}, newError(
			CodeInvalidRequest,
			"Il provider non ha restituito un'identità valida. Riprova.",
			true,
			nil,
		)
	}
	result, err := s.finalize(ctx, attempt, identity, now)
	if err == nil {
		return result, nil
	}
	var domainError *Error
	if errors.As(err, &domainError) && !domainError.Retryable {
		_ = s.store.FailAttempt(ctx, attempt.ID, now)
	} else {
		_ = s.store.ReleaseAttempt(ctx, attempt.ID)
	}
	return CallbackResult{}, err
}

func (s *Service) finalize(
	ctx context.Context,
	attempt OAuthAttempt,
	external ExternalIdentity,
	now time.Time,
) (CallbackResult, error) {
	var result CallbackResult
	var rawSessionToken string
	if attempt.Intent == IntentLogin {
		var err error
		rawSessionToken, err = randomToken(32)
		if err != nil {
			return CallbackResult{}, wrapInternal("generate session token", err)
		}
	}
	revocationTokenCiphertext := []byte(nil)
	if external.RevocationToken != "" {
		var err error
		revocationTokenCiphertext, err = s.sealer.Seal([]byte(external.RevocationToken))
		if err != nil {
			return CallbackResult{}, wrapInternal("seal provider token", err)
		}
	}
	err := s.store.Transaction(ctx, func(tx Transaction) error {
		currentAttempt, exists, err := tx.Attempt(attempt.ID)
		if err != nil {
			return err
		}
		if !exists || currentAttempt.Status != AttemptClaimed {
			return newError(
				CodeInvalidState,
				"Richiesta di accesso già utilizzata. Riavvia il login.",
				false,
				nil,
			)
		}
		if attempt.Intent == IntentLink {
			return s.finalizeLink(
				tx,
				currentAttempt,
				external,
				revocationTokenCiphertext,
				now,
				&result,
			)
		}
		return s.finalizeLogin(
			tx,
			currentAttempt,
			external,
			revocationTokenCiphertext,
			rawSessionToken,
			now,
			&result,
		)
	})
	if err != nil {
		if errors.Is(err, errStoreConflict) {
			return CallbackResult{}, newError(
				CodeConflict,
				"Un accesso concorrente è stato completato. Riavvia il login.",
				true,
				err,
			)
		}
		var domainError *Error
		if errors.As(err, &domainError) {
			return CallbackResult{}, err
		}
		return CallbackResult{}, wrapInternal("finalize authentication", err)
	}
	result.ReturnTo = attempt.ReturnTo
	return result, nil
}

func (s *Service) finalizeLogin(
	tx Transaction,
	attempt OAuthAttempt,
	external ExternalIdentity,
	revocationTokenCiphertext []byte,
	rawSessionToken string,
	now time.Time,
	result *CallbackResult,
) error {
	identity, identityExists, err := tx.ProviderIdentity(attempt.Provider, external.Subject)
	if err != nil {
		return err
	}
	var account Account
	created := false
	if identityExists {
		var exists bool
		account, exists, err = tx.Account(identity.AccountID)
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("provider identity references missing account")
		}
	} else {
		if !external.EmailVerified || normalizeEmail(external.Email) == "" {
			return newError(
				CodeVerifiedEmailRequired,
				"Il provider deve restituire un indirizzo email verificato.",
				false,
				nil,
			)
		}
		normalizedEmail := normalizeEmail(external.Email)
		_, exists, err := tx.AccountByVerifiedEmail(normalizedEmail)
		if err != nil {
			return err
		}
		if exists {
			return newError(
				CodeLinkingRequired,
				"Esiste già un account con questa email. Accedi e collega il nuovo provider.",
				false,
				nil,
			)
		}
		if err := validateRegistration(attempt); err != nil {
			return err
		}
		accountID, err := randomToken(18)
		if err != nil {
			return err
		}
		account = Account{
			ID:              accountID,
			Email:           strings.TrimSpace(external.Email),
			NormalizedEmail: normalizedEmail,
			DisplayName:     strings.TrimSpace(external.DisplayName),
			ContractCountry: attempt.ContractCountry,
			CreatedAt:       now,
		}
		if err := tx.PutAccount(account); err != nil {
			return err
		}
		identity = ProviderIdentity{
			Provider:                  attempt.Provider,
			Subject:                   external.Subject,
			AccountID:                 account.ID,
			Email:                     strings.TrimSpace(external.Email),
			RevocationTokenCiphertext: revocationTokenCiphertext,
			LinkedAt:                  now,
		}
		if err := tx.PutProviderIdentity(identity); err != nil {
			return err
		}
		if err := s.appendOnboardingEvent(tx, account, attempt, now); err != nil {
			return err
		}
		created = true
	}
	if err := appendConsentEvents(tx, account.ID, attempt, now); err != nil {
		return err
	}
	sessionID, err := randomToken(18)
	if err != nil {
		return err
	}
	session := Session{
		ID:              sessionID,
		AccountID:       account.ID,
		TokenHash:       hashToken(rawSessionToken),
		CreatedAt:       now,
		AuthenticatedAt: now,
		ExpiresAt:       now.Add(s.sessionTTL),
	}
	if err := tx.PutSession(session); err != nil {
		return err
	}
	attempt.Status = AttemptCompleted
	attempt.CompletedAt = timePointer(now)
	if err := tx.UpdateAttempt(attempt); err != nil {
		return err
	}
	result.AccountID = account.ID
	result.SessionToken = rawSessionToken
	result.SessionExpiry = session.ExpiresAt
	result.Onboarding = created
	return nil
}

func (s *Service) finalizeLink(
	tx Transaction,
	attempt OAuthAttempt,
	external ExternalIdentity,
	revocationTokenCiphertext []byte,
	now time.Time,
	result *CallbackResult,
) error {
	session, exists, err := tx.SessionByTokenHash(attempt.BoundSessionTokenHash)
	if err != nil {
		return err
	}
	if !exists || session.AccountID != attempt.TargetAccountID ||
		session.RevokedAt != nil || !now.Before(session.ExpiresAt) {
		return newError(
			CodeUnauthenticated,
			"Sessione non valida. Accedi di nuovo.",
			true,
			nil,
		)
	}
	if now.Sub(session.AuthenticatedAt) > s.linkReauthWindow {
		return newError(
			CodeReauthenticationRequired,
			"Accedi di nuovo prima di collegare un provider.",
			true,
			nil,
		)
	}
	account, exists, err := tx.Account(attempt.TargetAccountID)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("link target account not found")
	}
	identity, identityExists, err := tx.ProviderIdentity(attempt.Provider, external.Subject)
	if err != nil {
		return err
	}
	if identityExists && identity.AccountID != account.ID {
		return newError(
			CodeIdentityConflict,
			"Questa identità è già collegata a un altro account.",
			false,
			nil,
		)
	}
	identities, err := tx.ProviderIdentities(account.ID)
	if err != nil {
		return err
	}
	for _, existing := range identities {
		if existing.Provider == attempt.Provider && existing.Subject != external.Subject {
			return newError(
				CodeIdentityConflict,
				"All'account è già collegata un'altra identità dello stesso provider.",
				false,
				nil,
			)
		}
	}
	if !identityExists {
		if err := tx.PutProviderIdentity(ProviderIdentity{
			Provider:                  attempt.Provider,
			Subject:                   external.Subject,
			AccountID:                 account.ID,
			Email:                     strings.TrimSpace(external.Email),
			RevocationTokenCiphertext: revocationTokenCiphertext,
			LinkedAt:                  now,
		}); err != nil {
			return err
		}
	}
	attempt.Status = AttemptCompleted
	attempt.CompletedAt = timePointer(now)
	if err := tx.UpdateAttempt(attempt); err != nil {
		return err
	}
	result.AccountID = account.ID
	result.Linked = true
	return nil
}

func (s *Service) Authenticate(
	ctx context.Context,
	sessionToken string,
) (SessionPrincipal, error) {
	principal, _, err := s.authenticate(ctx, sessionToken)
	return principal, err
}

func (s *Service) authenticate(
	ctx context.Context,
	sessionToken string,
) (SessionPrincipal, time.Time, error) {
	if strings.TrimSpace(sessionToken) == "" {
		return SessionPrincipal{}, time.Time{}, newError(
			CodeUnauthenticated,
			"Sessione non valida. Accedi di nuovo.",
			true,
			nil,
		)
	}
	now := s.now().UTC()
	var session Session
	err := s.store.Transaction(ctx, func(tx Transaction) error {
		var exists bool
		var err error
		session, exists, err = tx.SessionByTokenHash(hashToken(sessionToken))
		if err != nil {
			return err
		}
		if !exists || session.RevokedAt != nil || !now.Before(session.ExpiresAt) {
			return newError(
				CodeUnauthenticated,
				"Sessione non valida. Accedi di nuovo.",
				true,
				nil,
			)
		}
		return nil
	})
	if err != nil {
		var domainError *Error
		if errors.As(err, &domainError) {
			return SessionPrincipal{}, time.Time{}, err
		}
		return SessionPrincipal{}, time.Time{}, wrapInternal("authenticate session", err)
	}
	return SessionPrincipal{
		AccountID: session.AccountID,
		SessionID: session.ID,
		ExpiresAt: session.ExpiresAt,
	}, session.AuthenticatedAt, nil
}

func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	if strings.TrimSpace(sessionToken) == "" {
		return nil
	}
	now := s.now().UTC()
	err := s.store.Transaction(ctx, func(tx Transaction) error {
		session, exists, err := tx.SessionByTokenHash(hashToken(sessionToken))
		if err != nil {
			return err
		}
		if !exists || session.RevokedAt != nil {
			return nil
		}
		session.RevokedAt = timePointer(now)
		return tx.PutSession(session)
	})
	if err != nil {
		return wrapInternal("logout session", err)
	}
	return nil
}

func (s *Service) RevokeAllSessions(ctx context.Context, sessionToken string) error {
	principal, _, err := s.authenticate(ctx, sessionToken)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	err = s.store.Transaction(ctx, func(tx Transaction) error {
		sessions, err := tx.Sessions(principal.AccountID)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			if session.RevokedAt == nil {
				session.RevokedAt = timePointer(now)
				if err := tx.PutSession(session); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return wrapInternal("revoke account sessions", err)
	}
	return nil
}

func (s *Service) UnlinkProvider(
	ctx context.Context,
	sessionToken string,
	provider Provider,
) error {
	if !isSupportedProvider(provider) {
		return newError(
			CodeUnsupportedProvider,
			"Provider di accesso non supportato.",
			false,
			nil,
		)
	}
	principal, _, err := s.authenticate(ctx, sessionToken)
	if err != nil {
		return err
	}
	var target ProviderIdentity
	err = s.store.Transaction(ctx, func(tx Transaction) error {
		identities, err := tx.ProviderIdentities(principal.AccountID)
		if err != nil {
			return err
		}
		if len(identities) <= 1 {
			return newError(
				CodeLastProvider,
				"Non puoi scollegare l'unico metodo di accesso.",
				false,
				nil,
			)
		}
		for _, identity := range identities {
			if identity.Provider == provider {
				target = identity
				return nil
			}
		}
		return newError(
			CodeInvalidRequest,
			"Il provider non è collegato a questo account.",
			false,
			nil,
		)
	})
	if err != nil {
		return err
	}
	if len(target.RevocationTokenCiphertext) > 0 {
		token, openErr := s.sealer.Open(target.RevocationTokenCiphertext)
		if openErr != nil {
			return wrapInternal("open provider revocation token", openErr)
		}
		if revokeErr := s.providers[provider].Revoke(ctx, string(token)); revokeErr != nil {
			return s.providerOperationError(revokeErr)
		}
	}
	err = s.store.Transaction(ctx, func(tx Transaction) error {
		session, exists, err := tx.SessionByTokenHash(hashToken(sessionToken))
		if err != nil {
			return err
		}
		if !exists || session.RevokedAt != nil || !s.now().UTC().Before(session.ExpiresAt) {
			return newError(
				CodeUnauthenticated,
				"Sessione non valida. Accedi di nuovo.",
				true,
				nil,
			)
		}
		identities, err := tx.ProviderIdentities(principal.AccountID)
		if err != nil {
			return err
		}
		if len(identities) <= 1 {
			return newError(
				CodeLastProvider,
				"Non puoi scollegare l'unico metodo di accesso.",
				false,
				nil,
			)
		}
		return tx.DeleteProviderIdentity(provider, principal.AccountID)
	})
	if err != nil {
		var domainError *Error
		if errors.As(err, &domainError) {
			return err
		}
		return wrapInternal("unlink provider", err)
	}
	return nil
}

func (s *Service) appendOnboardingEvent(
	tx Transaction,
	account Account,
	attempt OAuthAttempt,
	now time.Time,
) error {
	eventID, err := randomToken(18)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		AccountID            string    `json:"account_id"`
		Email                string    `json:"email"`
		DisplayName          string    `json:"display_name,omitempty"`
		ContractCountry      string    `json:"contract_country"`
		PersonalWorkspaceKey string    `json:"personal_workspace_key"`
		RequestedRole        string    `json:"requested_role"`
		IdempotencyKey       string    `json:"idempotency_key"`
		OccurredAt           time.Time `json:"occurred_at"`
	}{
		AccountID:            account.ID,
		Email:                account.Email,
		DisplayName:          account.DisplayName,
		ContractCountry:      account.ContractCountry,
		PersonalWorkspaceKey: "personal:" + account.ID,
		RequestedRole:        "owner",
		IdempotencyKey:       "auth-account:" + account.ID,
		OccurredAt:           now,
	})
	if err != nil {
		return err
	}
	return tx.AppendOutbox(OutboxEvent{
		ID:            eventID,
		Type:          OnboardingEventType,
		Version:       OnboardingEventVersion,
		AggregateID:   account.ID,
		CorrelationID: attempt.CorrelationID,
		Payload:       payload,
		OccurredAt:    now,
	})
}

func appendConsentEvents(
	tx Transaction,
	accountID string,
	attempt OAuthAttempt,
	now time.Time,
) error {
	for _, receipt := range attempt.Consents {
		exists, err := tx.ConsentExists(accountID, receipt, attempt.CorrelationID)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		id, err := randomToken(18)
		if err != nil {
			return err
		}
		if err := tx.AppendConsent(ConsentEvent{
			ID:            id,
			AccountID:     accountID,
			DocumentKey:   receipt.DocumentKey,
			Version:       receipt.Version,
			DigestSHA256:  strings.ToLower(receipt.DigestSHA256),
			Action:        receipt.Action,
			Purpose:       receipt.Purpose,
			Locale:        receipt.Locale,
			Country:       attempt.ContractCountry,
			Surface:       receipt.Surface,
			ControlTextID: receipt.ControlTextID,
			CorrelationID: attempt.CorrelationID,
			OccurredAt:    now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) handleProviderFailure(
	ctx context.Context,
	attemptID string,
	err error,
	now time.Time,
) error {
	var providerError *ProviderError
	if errors.As(err, &providerError) && providerError.Denied {
		_ = s.store.FailAttempt(ctx, attemptID, now)
		return newError(
			CodeProviderDenied,
			"Accesso annullato dal provider. Puoi riprovare.",
			true,
			err,
		)
	}
	if errors.As(err, &providerError) && !providerError.Retryable {
		_ = s.store.FailAttempt(ctx, attemptID, now)
		return newError(
			CodeInvalidRequest,
			"Il provider ha rifiutato la richiesta. Riavvia il login.",
			true,
			err,
		)
	}
	_ = s.store.ReleaseAttempt(ctx, attemptID)
	return newError(
		CodeProviderUnavailable,
		"Il provider non è disponibile. Riprova tra poco.",
		true,
		err,
	)
}

func (s *Service) providerOperationError(err error) error {
	var providerError *ProviderError
	if errors.As(err, &providerError) && !providerError.Retryable {
		return newError(
			CodeInvalidRequest,
			"Il provider ha rifiutato la revoca.",
			false,
			err,
		)
	}
	return newError(
		CodeProviderUnavailable,
		"Il provider non è disponibile. Riprova tra poco.",
		true,
		err,
	)
}

func buildAuthorizationURL(
	config ProviderConfig,
	state, challenge, nonce string,
) (string, error) {
	authorizationURL, err := url.Parse(config.AuthorizationURL)
	if err != nil {
		return "", err
	}
	query := authorizationURL.Query()
	for key, value := range config.ExtraParameters {
		query.Set(key, value)
	}
	query.Set("client_id", config.ClientID)
	query.Set("redirect_uri", config.RedirectURL)
	query.Set("response_type", "code")
	query.Set("scope", strings.Join(config.Scopes, " "))
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("nonce", nonce)
	authorizationURL.RawQuery = query.Encode()
	return authorizationURL.String(), nil
}

func validateProviderConfig(provider Provider, config ProviderConfig) error {
	if strings.TrimSpace(config.ClientID) == "" {
		return fmt.Errorf("%s provider client id is required", provider)
	}
	for name, rawURL := range map[string]string{
		"authorization": config.AuthorizationURL,
		"redirect":      config.RedirectURL,
	} {
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return fmt.Errorf("%s provider %s URL must be absolute HTTPS", provider, name)
		}
	}
	if len(config.Scopes) == 0 {
		return fmt.Errorf("%s provider scopes are required", provider)
	}
	requiredScopes := map[Provider][]string{
		ProviderGoogle:   {"openid", "email"},
		ProviderApple:    {"email"},
		ProviderFacebook: {"email"},
		ProviderLinkedIn: {"openid", "email"},
	}
	for _, required := range requiredScopes[provider] {
		if !contains(config.Scopes, required) {
			return fmt.Errorf("%s provider must request %s scope", provider, required)
		}
	}
	return nil
}

func validateRegistration(attempt OAuthAttempt) error {
	if attempt.ContractCountry != "IT" {
		return newError(
			CodeCountryNotSupported,
			"Postqron è disponibile per account con paese contrattuale Italia.",
			false,
			nil,
		)
	}
	var terms, privacy bool
	for _, consent := range attempt.Consents {
		switch {
		case consent.DocumentKey == "terms_it" && consent.Action == ConsentAccepted:
			terms = true
		case consent.DocumentKey == "privacy_it" && consent.Action == ConsentAcknowledged:
			privacy = true
		}
	}
	if !terms || !privacy {
		return newError(
			CodeInvalidConsent,
			"Accetta i Termini e conferma di aver letto l'Informativa privacy.",
			false,
			nil,
		)
	}
	return nil
}

func validateConsentShape(receipts []ConsentReceipt) error {
	for _, receipt := range receipts {
		if strings.TrimSpace(receipt.DocumentKey) == "" ||
			!legalVersionPattern.MatchString(receipt.Version) ||
			!validSHA256(receipt.DigestSHA256) ||
			!validConsentAction(receipt.Action) ||
			strings.TrimSpace(receipt.Purpose) == "" ||
			receipt.Locale != "it-IT" ||
			strings.TrimSpace(receipt.Surface) == "" ||
			strings.TrimSpace(receipt.ControlTextID) == "" {
			return newError(
				CodeInvalidConsent,
				"Il riferimento al documento legale non è valido.",
				false,
				nil,
			)
		}
	}
	return nil
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validConsentAction(action ConsentAction) bool {
	switch action {
	case ConsentAccepted,
		ConsentAcknowledged,
		ConsentGranted,
		ConsentRejected,
		ConsentWithdrawn:
		return true
	default:
		return false
	}
}

func validateReturnTo(returnTo string) error {
	if returnTo == "" {
		return nil
	}
	if !strings.HasPrefix(returnTo, "/") || strings.HasPrefix(returnTo, "//") {
		return newError(
			CodeInvalidRequest,
			"La destinazione dopo l'accesso non è valida.",
			false,
			nil,
		)
	}
	parsed, err := url.Parse(returnTo)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return newError(
			CodeInvalidRequest,
			"La destinazione dopo l'accesso non è valida.",
			false,
			err,
		)
	}
	return nil
}

func normalizedReturnTo(returnTo string) string {
	if returnTo == "" {
		return "/app"
	}
	return returnTo
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func isSupportedProvider(provider Provider) bool {
	for _, supported := range SupportedProviders {
		if provider == supported {
			return true
		}
	}
	return false
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
