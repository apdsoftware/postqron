package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

var errStoreConflict = errors.New("auth store uniqueness conflict")

type memoryState struct {
	attempts       map[string]OAuthAttempt
	attemptByState map[string]string
	accounts       map[string]Account
	accountByEmail map[string]string
	identities     map[string]ProviderIdentity
	sessions       map[string]Session
	consents       []ConsentEvent
	outbox         []OutboxEvent
}

type MemoryStore struct {
	mu    sync.Mutex
	state memoryState
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{state: newMemoryState()}
}

func newMemoryState() memoryState {
	return memoryState{
		attempts:       make(map[string]OAuthAttempt),
		attemptByState: make(map[string]string),
		accounts:       make(map[string]Account),
		accountByEmail: make(map[string]string),
		identities:     make(map[string]ProviderIdentity),
		sessions:       make(map[string]Session),
	}
}

func (s *MemoryStore) SaveAttempt(_ context.Context, attempt OAuthAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.state.attempts[attempt.ID]; exists {
		return errStoreConflict
	}
	if _, exists := s.state.attemptByState[attempt.StateHash]; exists {
		return errStoreConflict
	}
	if attempt.TargetAccountID != "" {
		account, exists := s.state.accounts[attempt.TargetAccountID]
		if !exists || !accountAccessAllowed(account) {
			return ErrAccountAccessUnavailable
		}
	}
	s.state.attempts[attempt.ID] = cloneAttempt(attempt)
	s.state.attemptByState[attempt.StateHash] = attempt.ID
	return nil
}

func (s *MemoryStore) ClaimAttempt(
	_ context.Context,
	stateHash string,
	now time.Time,
) (OAuthAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, exists := s.state.attemptByState[stateHash]
	if !exists {
		return OAuthAttempt{}, newError(
			CodeInvalidState,
			"Richiesta di accesso non valida. Riavvia il login.",
			false,
			nil,
		)
	}
	attempt := s.state.attempts[id]
	if !now.Before(attempt.ExpiresAt) {
		return OAuthAttempt{}, newError(
			CodeFlowExpired,
			"La richiesta di accesso è scaduta. Riprova.",
			true,
			nil,
		)
	}
	if attempt.Status != AttemptPending {
		return OAuthAttempt{}, newError(
			CodeInvalidState,
			"Richiesta di accesso già utilizzata. Riavvia il login.",
			false,
			nil,
		)
	}
	attempt.Status = AttemptClaimed
	attempt.ClaimedAt = timePointer(now)
	s.state.attempts[id] = attempt
	return cloneAttempt(attempt), nil
}

func (s *MemoryStore) ReleaseAttempt(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt, exists := s.state.attempts[id]
	if !exists {
		return errors.New("attempt not found")
	}
	if attempt.Status != AttemptClaimed {
		return errors.New("attempt is not claimed")
	}
	attempt.Status = AttemptPending
	attempt.ClaimedAt = nil
	s.state.attempts[id] = attempt
	return nil
}

func (s *MemoryStore) FailAttempt(_ context.Context, id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt, exists := s.state.attempts[id]
	if !exists {
		return errors.New("attempt not found")
	}
	if attempt.Status != AttemptClaimed {
		return errors.New("attempt is not claimed")
	}
	attempt.Status = AttemptFailed
	attempt.CompletedAt = timePointer(now)
	s.state.attempts[id] = attempt
	return nil
}

func (s *MemoryStore) Transaction(
	_ context.Context,
	operation func(Transaction) error,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	working := cloneMemoryState(s.state)
	if err := operation(&memoryTransaction{state: &working}); err != nil {
		return err
	}
	s.state = working
	return nil
}

type memoryTransaction struct {
	state *memoryState
}

func (tx *memoryTransaction) Attempt(id string) (OAuthAttempt, bool, error) {
	attempt, exists := tx.state.attempts[id]
	return cloneAttempt(attempt), exists, nil
}

func (tx *memoryTransaction) UpdateAttempt(attempt OAuthAttempt) error {
	if _, exists := tx.state.attempts[attempt.ID]; !exists {
		return errors.New("attempt not found")
	}
	tx.state.attempts[attempt.ID] = cloneAttempt(attempt)
	return nil
}

func (tx *memoryTransaction) ProviderIdentity(
	provider Provider,
	subject string,
) (ProviderIdentity, bool, error) {
	identity, exists := tx.state.identities[identityKey(provider, subject)]
	return cloneProviderIdentity(identity), exists, nil
}

func (tx *memoryTransaction) ProviderIdentities(
	accountID string,
) ([]ProviderIdentity, error) {
	var identities []ProviderIdentity
	for _, identity := range tx.state.identities {
		if identity.AccountID == accountID {
			identities = append(identities, cloneProviderIdentity(identity))
		}
	}
	slices.SortFunc(identities, func(left, right ProviderIdentity) int {
		if left.Provider < right.Provider {
			return -1
		}
		if left.Provider > right.Provider {
			return 1
		}
		return 0
	})
	return identities, nil
}

func (tx *memoryTransaction) PutProviderIdentity(identity ProviderIdentity) error {
	key := identityKey(identity.Provider, identity.Subject)
	if existing, exists := tx.state.identities[key]; exists &&
		existing.AccountID != identity.AccountID {
		return errStoreConflict
	}
	for _, existing := range tx.state.identities {
		if existing.AccountID == identity.AccountID &&
			existing.Provider == identity.Provider &&
			existing.Subject != identity.Subject {
			return errStoreConflict
		}
	}
	tx.state.identities[key] = cloneProviderIdentity(identity)
	return nil
}

func (tx *memoryTransaction) DeleteProviderIdentity(
	provider Provider,
	accountID string,
) error {
	for key, identity := range tx.state.identities {
		if identity.Provider == provider && identity.AccountID == accountID {
			delete(tx.state.identities, key)
			return nil
		}
	}
	return errors.New("provider identity not found")
}

func (tx *memoryTransaction) Account(id string) (Account, bool, error) {
	account, exists := tx.state.accounts[id]
	return account, exists, nil
}

func (tx *memoryTransaction) AccountByVerifiedEmail(
	normalizedEmail string,
) (Account, bool, error) {
	id, exists := tx.state.accountByEmail[normalizedEmail]
	if !exists {
		return Account{}, false, nil
	}
	account, exists := tx.state.accounts[id]
	return account, exists, nil
}

func (tx *memoryTransaction) PutAccount(account Account) error {
	if _, exists := tx.state.accounts[account.ID]; exists {
		return errStoreConflict
	}
	if _, exists := tx.state.accountByEmail[account.NormalizedEmail]; exists {
		return errStoreConflict
	}
	if account.AccessState == "" {
		account.AccessState = AccountAccessActive
	}
	tx.state.accounts[account.ID] = account
	tx.state.accountByEmail[account.NormalizedEmail] = account.ID
	return nil
}

func (s *MemoryStore) FreezeAccountAccess(
	_ context.Context,
	accountID string,
	now time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, exists := s.state.accounts[accountID]
	if !exists || account.AccessState == AccountAccessFinalized {
		return ErrAccountAccessUnavailable
	}
	if account.AccessState == AccountAccessFrozen {
		return nil
	}
	account.AccessState = AccountAccessFrozen
	account.FrozenAt = timePointer(now)
	s.state.accounts[accountID] = account
	s.invalidateAccountArtifacts(accountID, now)
	return nil
}

func (s *MemoryStore) RestoreAccountAccess(
	_ context.Context,
	accountID string,
	_ time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, exists := s.state.accounts[accountID]
	if !exists || account.AccessState == AccountAccessFinalized {
		return ErrAccountAccessUnavailable
	}
	if account.AccessState == AccountAccessActive {
		return nil
	}
	account.AccessState = AccountAccessActive
	account.FrozenAt = nil
	s.state.accounts[accountID] = account
	return nil
}

func (s *MemoryStore) FinalizeAccountAccess(
	_ context.Context,
	accountID string,
	now time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, exists := s.state.accounts[accountID]
	if !exists {
		return ErrAccountAccessUnavailable
	}
	if account.AccessState == AccountAccessFinalized {
		return nil
	}
	s.invalidateAccountArtifacts(accountID, now)
	for key, identity := range s.state.identities {
		if identity.AccountID == accountID {
			delete(s.state.identities, key)
		}
	}
	sum := sha256.Sum256([]byte(accountID))
	delete(s.state.accountByEmail, account.NormalizedEmail)
	account.Email = fmt.Sprintf("finalized-%x@invalid.local", sum)
	account.NormalizedEmail = account.Email
	account.DisplayName = ""
	account.AccessState = AccountAccessFinalized
	if account.FrozenAt == nil {
		account.FrozenAt = timePointer(now)
	}
	account.FinalizedAt = timePointer(now)
	s.state.accounts[accountID] = account
	s.state.accountByEmail[account.NormalizedEmail] = accountID
	for index := range s.state.outbox {
		if s.state.outbox[index].AggregateID == accountID {
			s.state.outbox[index].Payload = []byte("{}")
		}
	}
	return nil
}

func (s *MemoryStore) invalidateAccountArtifacts(accountID string, now time.Time) {
	for key, session := range s.state.sessions {
		if session.AccountID == accountID && session.RevokedAt == nil {
			session.RevokedAt = timePointer(now)
			s.state.sessions[key] = session
		}
	}
	for id, attempt := range s.state.attempts {
		if attempt.TargetAccountID == accountID &&
			(attempt.Status == AttemptPending || attempt.Status == AttemptClaimed) {
			attempt.Status = AttemptFailed
			attempt.CompletedAt = timePointer(now)
			s.state.attempts[id] = attempt
		}
	}
}

func (tx *memoryTransaction) SessionByTokenHash(
	tokenHash string,
) (Session, bool, error) {
	session, exists := tx.state.sessions[tokenHash]
	return cloneSession(session), exists, nil
}

func (tx *memoryTransaction) Sessions(accountID string) ([]Session, error) {
	var sessions []Session
	for _, session := range tx.state.sessions {
		if session.AccountID == accountID {
			sessions = append(sessions, cloneSession(session))
		}
	}
	return sessions, nil
}

func (tx *memoryTransaction) PutSession(session Session) error {
	if existing, exists := tx.state.sessions[session.TokenHash]; exists &&
		existing.ID != session.ID {
		return errStoreConflict
	}
	tx.state.sessions[session.TokenHash] = cloneSession(session)
	return nil
}

func (tx *memoryTransaction) ConsentExists(
	accountID string,
	receipt ConsentReceipt,
	correlationID string,
) (bool, error) {
	for _, event := range tx.state.consents {
		if event.AccountID == accountID &&
			event.DocumentKey == receipt.DocumentKey &&
			event.Version == receipt.Version &&
			event.DigestSHA256 == receipt.DigestSHA256 &&
			event.Action == receipt.Action &&
			event.Purpose == receipt.Purpose &&
			event.CorrelationID == correlationID {
			return true, nil
		}
	}
	return false, nil
}

func (tx *memoryTransaction) AppendConsent(event ConsentEvent) error {
	tx.state.consents = append(tx.state.consents, event)
	return nil
}

func (tx *memoryTransaction) AppendOutbox(event OutboxEvent) error {
	tx.state.outbox = append(tx.state.outbox, cloneOutboxEvent(event))
	return nil
}

type MemorySnapshot struct {
	Attempts   []OAuthAttempt
	Accounts   []Account
	Identities []ProviderIdentity
	Sessions   []Session
	Consents   []ConsentEvent
	Outbox     []OutboxEvent
}

func (s *MemoryStore) Snapshot() MemorySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := MemorySnapshot{
		Consents: slices.Clone(s.state.consents),
		Outbox:   make([]OutboxEvent, 0, len(s.state.outbox)),
	}
	for _, attempt := range s.state.attempts {
		snapshot.Attempts = append(snapshot.Attempts, cloneAttempt(attempt))
	}
	for _, account := range s.state.accounts {
		snapshot.Accounts = append(snapshot.Accounts, account)
	}
	for _, identity := range s.state.identities {
		snapshot.Identities = append(snapshot.Identities, cloneProviderIdentity(identity))
	}
	for _, session := range s.state.sessions {
		snapshot.Sessions = append(snapshot.Sessions, cloneSession(session))
	}
	for _, event := range s.state.outbox {
		snapshot.Outbox = append(snapshot.Outbox, cloneOutboxEvent(event))
	}
	return snapshot
}

func cloneMemoryState(source memoryState) memoryState {
	target := newMemoryState()
	for key, attempt := range source.attempts {
		target.attempts[key] = cloneAttempt(attempt)
	}
	for key, id := range source.attemptByState {
		target.attemptByState[key] = id
	}
	for key, account := range source.accounts {
		target.accounts[key] = account
	}
	for key, id := range source.accountByEmail {
		target.accountByEmail[key] = id
	}
	for key, identity := range source.identities {
		target.identities[key] = cloneProviderIdentity(identity)
	}
	for key, session := range source.sessions {
		target.sessions[key] = cloneSession(session)
	}
	target.consents = slices.Clone(source.consents)
	for _, event := range source.outbox {
		target.outbox = append(target.outbox, cloneOutboxEvent(event))
	}
	return target
}

func cloneAttempt(attempt OAuthAttempt) OAuthAttempt {
	attempt.PKCEVerifierCiphertext = slices.Clone(attempt.PKCEVerifierCiphertext)
	attempt.NonceCiphertext = slices.Clone(attempt.NonceCiphertext)
	attempt.Consents = slices.Clone(attempt.Consents)
	if attempt.ClaimedAt != nil {
		attempt.ClaimedAt = timePointer(*attempt.ClaimedAt)
	}
	if attempt.CompletedAt != nil {
		attempt.CompletedAt = timePointer(*attempt.CompletedAt)
	}
	return attempt
}

func cloneProviderIdentity(identity ProviderIdentity) ProviderIdentity {
	identity.RevocationTokenCiphertext = slices.Clone(identity.RevocationTokenCiphertext)
	return identity
}

func cloneSession(session Session) Session {
	if session.RevokedAt != nil {
		session.RevokedAt = timePointer(*session.RevokedAt)
	}
	return session
}

func cloneOutboxEvent(event OutboxEvent) OutboxEvent {
	event.Payload = slices.Clone(event.Payload)
	return event
}

func identityKey(provider Provider, subject string) string {
	return string(provider) + "\x00" + subject
}

func timePointer(value time.Time) *time.Time {
	return &value
}
