package cookieconsent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type PostgresRepository struct {
	database *sql.DB
}

func NewPostgresRepository(database *sql.DB) (*PostgresRepository, error) {
	if database == nil {
		return nil, ErrInvalidRequest
	}
	return &PostgresRepository{database: database}, nil
}

func (repository *PostgresRepository) Read(
	ctx context.Context,
	subject Subject,
	policy PolicyRelease,
	now time.Time,
) (PreferenceState, error) {
	state, found, err := scanPreference(repository.database.QueryRowContext(ctx, `
		SELECT
			p.necessary, p.preferences, p.analytics, p.marketing,
			p.policy_version, p.policy_digest_sha256, p.selected_at,
			p.expires_at, p.source, p.revision
		FROM f26_cookie_subjects s
		JOIN f26_cookie_preferences p ON p.subject_key = s.subject_key
		WHERE s.subject_kind = $1 AND s.external_subject_id = $2`,
		subject.Kind,
		subject.ID,
	))
	if err != nil {
		return PreferenceState{}, fmt.Errorf("read cookie preferences: %w", err)
	}
	if !found ||
		state.PolicyVersion != policy.Version ||
		state.PolicyDigest != policy.DigestSHA256 ||
		state.ExpiresAt == nil ||
		!now.Before(*state.ExpiresAt) {
		return defaultState(policy), nil
	}
	state.HasRecordedChoice = true
	return state, nil
}

func (repository *PostgresRepository) Put(
	ctx context.Context,
	mutation Mutation,
) (PreferenceState, bool, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return PreferenceState{}, false, fmt.Errorf("begin cookie preference transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	subjectKey, err := ensureSubject(ctx, transaction, mutation.Subject, mutation.SelectedAt)
	if err != nil {
		return PreferenceState{}, false, err
	}
	if _, err := transaction.ExecContext(
		ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		subjectKey,
	); err != nil {
		return PreferenceState{}, false, fmt.Errorf("lock cookie subject: %w", err)
	}

	var previousFingerprint string
	var responseBytes []byte
	err = transaction.QueryRowContext(ctx, `
		SELECT request_fingerprint, response
		FROM f26_cookie_idempotency
		WHERE subject_key = $1 AND idempotency_key = $2`,
		subjectKey,
		mutation.IdempotencyKey,
	).Scan(&previousFingerprint, &responseBytes)
	if err == nil {
		if previousFingerprint != mutation.Fingerprint {
			return PreferenceState{}, false, ErrIdempotencyConflict
		}
		var replay PreferenceState
		if err := json.Unmarshal(responseBytes, &replay); err != nil {
			return PreferenceState{}, false, fmt.Errorf("decode idempotent response: %w", err)
		}
		if err := transaction.Commit(); err != nil {
			return PreferenceState{}, false, fmt.Errorf("commit cookie replay: %w", err)
		}
		return replay, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PreferenceState{}, false, fmt.Errorf("read cookie idempotency: %w", err)
	}

	previous, found, err := scanPreference(transaction.QueryRowContext(ctx, `
		SELECT
			necessary, preferences, analytics, marketing, policy_version,
			policy_digest_sha256, selected_at, expires_at, source, revision
		FROM f26_cookie_preferences
		WHERE subject_key = $1
		FOR UPDATE`,
		subjectKey,
	))
	if err != nil {
		return PreferenceState{}, false, fmt.Errorf("lock cookie preferences: %w", err)
	}
	if !found {
		previous = defaultState(mutation.Policy)
	}
	revision := previous.Revision + 1
	selectedAt := mutation.SelectedAt.UTC()
	expiresAt := mutation.ExpiresAt.UTC()
	state := PreferenceState{
		Necessary:         true,
		Preferences:       mutation.Selection.Preferences,
		Analytics:         mutation.Selection.Analytics,
		Marketing:         mutation.Selection.Marketing,
		HasRecordedChoice: true,
		PolicyVersion:     mutation.Policy.Version,
		PolicyDigest:      mutation.Policy.DigestSHA256,
		SelectedAt:        &selectedAt,
		ExpiresAt:         &expiresAt,
		Source:            mutation.Source,
		Revision:          revision,
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO f26_cookie_preferences (
			subject_key, necessary, preferences, analytics, marketing,
			policy_version, policy_digest_sha256, source, selected_at,
			expires_at, revision
		) VALUES ($1, true, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (subject_key) DO UPDATE SET
			necessary = true,
			preferences = EXCLUDED.preferences,
			analytics = EXCLUDED.analytics,
			marketing = EXCLUDED.marketing,
			policy_version = EXCLUDED.policy_version,
			policy_digest_sha256 = EXCLUDED.policy_digest_sha256,
			source = EXCLUDED.source,
			selected_at = EXCLUDED.selected_at,
			expires_at = EXCLUDED.expires_at,
			revision = EXCLUDED.revision`,
		subjectKey,
		state.Preferences,
		state.Analytics,
		state.Marketing,
		state.PolicyVersion,
		state.PolicyDigest,
		state.Source,
		selectedAt,
		expiresAt,
		revision,
	); err != nil {
		return PreferenceState{}, false, fmt.Errorf("write cookie preferences: %w", err)
	}
	for _, category := range []string{"preferences", "analytics", "marketing"} {
		oldValue := categoryValue(previous.Selection(), category)
		newValue := categoryValue(mutation.Selection, category)
		eventDigest := sha256.Sum256([]byte(
			subjectKey + "\x00" + mutation.IdempotencyKey + "\x00" + category,
		))
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO f26_cookie_consent_events (
				event_id, subject_key, category, action, enabled,
				policy_version, policy_digest_sha256, occurred_at, source,
				idempotency_key, preference_revision, retention_until
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			hex.EncodeToString(eventDigest[:]),
			subjectKey,
			category,
			evidenceAction(oldValue, newValue, previous.HasRecordedChoice),
			newValue,
			mutation.Policy.Version,
			mutation.Policy.DigestSHA256,
			selectedAt,
			mutation.Source,
			mutation.IdempotencyKey,
			revision,
			mutation.RetentionUntil.UTC(),
		); err != nil {
			return PreferenceState{}, false, fmt.Errorf("append cookie evidence: %w", err)
		}
	}
	responseBytes, _ = json.Marshal(state)
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO f26_cookie_idempotency (
			subject_key, idempotency_key, request_fingerprint, response, created_at
		) VALUES ($1, $2, $3, $4, $5)`,
		subjectKey,
		mutation.IdempotencyKey,
		mutation.Fingerprint,
		responseBytes,
		selectedAt,
	); err != nil {
		return PreferenceState{}, false, fmt.Errorf("record cookie idempotency: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return PreferenceState{}, false, fmt.Errorf("commit cookie preferences: %w", err)
	}
	return state, false, nil
}

func (repository *PostgresRepository) Export(
	ctx context.Context,
	subject Subject,
	policy PolicyRelease,
	now time.Time,
) (PortableExport, error) {
	current, err := repository.Read(ctx, subject, policy, now)
	if err != nil {
		return PortableExport{}, err
	}
	rows, err := repository.database.QueryContext(ctx, `
		SELECT
			e.event_id, e.category, e.action, e.enabled, e.policy_version,
			e.policy_digest_sha256, e.occurred_at, e.source,
			e.idempotency_key, e.retention_until, e.preference_revision
		FROM f26_cookie_subjects s
		JOIN f26_cookie_consent_events e ON e.subject_key = s.subject_key
		WHERE s.subject_kind = $1
		  AND s.external_subject_id = $2
		  AND e.retention_until > $3
		ORDER BY e.occurred_at, e.category`,
		subject.Kind,
		subject.ID,
		now.UTC(),
	)
	if err != nil {
		return PortableExport{}, fmt.Errorf("export cookie evidence: %w", err)
	}
	defer rows.Close()
	evidence := make([]Evidence, 0)
	for rows.Next() {
		var event Evidence
		if err := rows.Scan(
			&event.EventID,
			&event.Category,
			&event.Action,
			&event.Enabled,
			&event.PolicyVersion,
			&event.PolicyDigest,
			&event.OccurredAt,
			&event.Source,
			&event.IdempotencyKey,
			&event.RetentionUntil,
			&event.PreferenceState,
		); err != nil {
			return PortableExport{}, fmt.Errorf("scan cookie evidence: %w", err)
		}
		evidence = append(evidence, event)
	}
	if err := rows.Err(); err != nil {
		return PortableExport{}, fmt.Errorf("iterate cookie evidence: %w", err)
	}
	return PortableExport{
		GeneratedAt: now.UTC(),
		SubjectKind: subject.Kind,
		Current:     current,
		Evidence:    evidence,
	}, nil
}

func (repository *PostgresRepository) Erase(
	ctx context.Context,
	subject Subject,
) error {
	_, err := repository.database.ExecContext(ctx, `
		DELETE FROM f26_cookie_subjects
		WHERE subject_kind = $1 AND external_subject_id = $2`,
		subject.Kind,
		subject.ID,
	)
	if err != nil {
		return fmt.Errorf("erase cookie subject mapping: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) PurgeEvidence(
	ctx context.Context,
	now time.Time,
) (int64, error) {
	result, err := repository.database.ExecContext(ctx, `
		DELETE FROM f26_cookie_consent_events
		WHERE retention_until <= $1`,
		now.UTC(),
	)
	if err != nil {
		return 0, fmt.Errorf("purge cookie evidence: %w", err)
	}
	return result.RowsAffected()
}

func ensureSubject(
	ctx context.Context,
	transaction *sql.Tx,
	subject Subject,
	now time.Time,
) (string, error) {
	candidate, err := randomHex(16)
	if err != nil {
		return "", fmt.Errorf("generate cookie subject key: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO f26_cookie_subjects (
			subject_key, subject_kind, external_subject_id, created_at
		) VALUES ($1, $2, $3, $4)
		ON CONFLICT (subject_kind, external_subject_id) DO NOTHING`,
		candidate,
		subject.Kind,
		subject.ID,
		now.UTC(),
	); err != nil {
		return "", fmt.Errorf("ensure cookie subject: %w", err)
	}
	var subjectKey string
	if err := transaction.QueryRowContext(ctx, `
		SELECT subject_key
		FROM f26_cookie_subjects
		WHERE subject_kind = $1 AND external_subject_id = $2`,
		subject.Kind,
		subject.ID,
	).Scan(&subjectKey); err != nil {
		return "", fmt.Errorf("resolve cookie subject: %w", err)
	}
	return subjectKey, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanPreference(row rowScanner) (PreferenceState, bool, error) {
	var state PreferenceState
	var selectedAt, expiresAt time.Time
	err := row.Scan(
		&state.Necessary,
		&state.Preferences,
		&state.Analytics,
		&state.Marketing,
		&state.PolicyVersion,
		&state.PolicyDigest,
		&selectedAt,
		&expiresAt,
		&state.Source,
		&state.Revision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PreferenceState{}, false, nil
	}
	if err != nil {
		return PreferenceState{}, false, err
	}
	selectedAt = selectedAt.UTC()
	expiresAt = expiresAt.UTC()
	state.SelectedAt = &selectedAt
	state.ExpiresAt = &expiresAt
	state.HasRecordedChoice = true
	return state, true, nil
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

type PostgresPolicySource struct {
	database *sql.DB
}

func NewPostgresPolicySource(database *sql.DB) (*PostgresPolicySource, error) {
	if database == nil {
		return nil, ErrInvalidRequest
	}
	return &PostgresPolicySource{database: database}, nil
}

func (source *PostgresPolicySource) Current(
	ctx context.Context,
	now time.Time,
) (PolicyRelease, error) {
	var policy PolicyRelease
	err := source.database.QueryRowContext(ctx, `
		SELECT version, digest_sha256, effective_at
		FROM compliance_legal_documents
		WHERE document_key = 'cookies_it'
		  AND content_status = 'approved'
		  AND published_at IS NOT NULL
		  AND effective_at <= $1
		  AND (superseded_at IS NULL OR superseded_at > $1)
		ORDER BY effective_at DESC, published_at DESC
		LIMIT 1`,
		now.UTC(),
	).Scan(&policy.Version, &policy.DigestSHA256, &policy.EffectiveAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PolicyRelease{}, ErrPolicyUnavailable
	}
	if err != nil {
		return PolicyRelease{}, fmt.Errorf("read current cookie policy: %w", err)
	}
	return policy, nil
}
