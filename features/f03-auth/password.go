package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	defaultPasswordMemory      = 64 * 1024
	defaultPasswordIterations  = 3
	defaultPasswordParallelism = 1
	defaultPasswordSaltLength  = 16
	defaultPasswordKeyLength   = 32
	defaultPasswordSessionTTL  = 30 * 24 * time.Hour
	defaultPasswordReauthLimit = 5 * time.Minute
)

var (
	ErrInvalidCredentials        = errors.New("invalid email or password")
	ErrPasswordUnauthenticated   = errors.New("password session is unavailable")
	ErrPasswordCSRFInvalid       = errors.New("password CSRF token is invalid")
	ErrPasswordReauthRequired    = errors.New("recent password authentication is required")
	ErrCurrentPasswordInvalid    = errors.New("current password is invalid")
	ErrPasswordConfirmation      = errors.New("password confirmation does not match")
	ErrPasswordPolicy            = errors.New("password does not satisfy policy")
	ErrPasswordChangeRateLimited = errors.New("password change is rate limited")
	ErrPasswordChangeConflict    = errors.New("password changed concurrently")
)

type PasswordParameters struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

func DefaultPasswordParameters() PasswordParameters {
	return PasswordParameters{
		Memory:      defaultPasswordMemory,
		Iterations:  defaultPasswordIterations,
		Parallelism: defaultPasswordParallelism,
		SaltLength:  defaultPasswordSaltLength,
		KeyLength:   defaultPasswordKeyLength,
	}
}

func (parameters PasswordParameters) validate() error {
	if parameters.Memory < 32*1024 ||
		parameters.Iterations < 2 ||
		parameters.Parallelism == 0 ||
		parameters.SaltLength < 16 ||
		parameters.KeyLength < 32 {
		return errors.New("password hashing parameters are below the security floor")
	}
	return nil
}

func HashPassword(password string, parameters PasswordParameters) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	if err := parameters.validate(); err != nil {
		return "", err
	}
	salt := make([]byte, parameters.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey(
		[]byte(password),
		salt,
		parameters.Iterations,
		parameters.Memory,
		parameters.Parallelism,
		parameters.KeyLength,
	)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		parameters.Memory,
		parameters.Iterations,
		parameters.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func VerifyPassword(encoded, password string) (bool, error) {
	parameters, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey(
		[]byte(password),
		salt,
		parameters.Iterations,
		parameters.Memory,
		parameters.Parallelism,
		uint32(len(expected)),
	)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func parsePasswordHash(encoded string) (PasswordParameters, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return PasswordParameters{}, nil, nil, errors.New("password hash is not Argon2id PHC")
	}
	version, err := strconv.Atoi(strings.TrimPrefix(parts[2], "v="))
	if err != nil || version != argon2.Version {
		return PasswordParameters{}, nil, nil, errors.New("password hash version is unsupported")
	}
	var parameters PasswordParameters
	if _, err := fmt.Sscanf(
		parts[3],
		"m=%d,t=%d,p=%d",
		&parameters.Memory,
		&parameters.Iterations,
		&parameters.Parallelism,
	); err != nil {
		return PasswordParameters{}, nil, nil, errors.New("password hash parameters are invalid")
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return PasswordParameters{}, nil, nil, errors.New("password hash salt is invalid")
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return PasswordParameters{}, nil, nil, errors.New("password hash key is invalid")
	}
	parameters.SaltLength = uint32(len(salt))
	parameters.KeyLength = uint32(len(key))
	if err := parameters.validate(); err != nil {
		return PasswordParameters{}, nil, nil, err
	}
	return parameters, salt, key, nil
}

func validatePassword(password string) error {
	if len(password) < 12 || len(password) > 1024 {
		return errors.New("password must contain between 12 and 1024 bytes")
	}
	return nil
}

type PasswordCredential struct {
	AccountID      string
	PasswordHash   string
	FailedAttempts int
	LockedUntil    *time.Time
}

type PasswordSession struct {
	ID              string
	AccountID       string
	TokenHash       string
	CreatedAt       time.Time
	AuthenticatedAt time.Time
	ExpiresAt       time.Time
}

type PasswordSessionContext struct {
	AccountID         string
	PasswordHash      string
	AuthenticatedAt   time.Time
	ChangeLockedUntil *time.Time
}

type PasswordChange struct {
	AccountID               string
	CurrentPasswordHash     string
	CurrentSessionTokenHash string
	NewPasswordHash         string
	NewSession              PasswordSession
}

type PasswordStore interface {
	CredentialByEmail(context.Context, string) (PasswordCredential, bool, error)
	RecordPasswordFailure(context.Context, string, time.Time) error
	CompletePasswordLogin(context.Context, PasswordSession, string, time.Time) error
	PasswordSession(
		context.Context,
		string,
		time.Time,
	) (PasswordSessionContext, bool, error)
	RecordPasswordChangeFailure(context.Context, string, string, time.Time) error
	CompletePasswordChange(context.Context, PasswordChange, string, time.Time) error
	RevokePasswordSession(context.Context, string, string, time.Time) error
}

type PasswordService struct {
	store        PasswordStore
	now          func() time.Time
	sessionTTL   time.Duration
	reauthWindow time.Duration
	dummyHash    string
}

func NewPasswordService(
	store PasswordStore,
	now func() time.Time,
	sessionTTL time.Duration,
) (*PasswordService, error) {
	if store == nil {
		return nil, errors.New("password store is required")
	}
	if now == nil {
		now = time.Now
	}
	if sessionTTL == 0 {
		sessionTTL = defaultPasswordSessionTTL
	}
	if sessionTTL <= 0 {
		return nil, errors.New("password session lifetime must be positive")
	}
	dummyHash, err := HashPassword(
		"postqron-dummy-password-never-valid",
		DefaultPasswordParameters(),
	)
	if err != nil {
		return nil, err
	}
	return &PasswordService{
		store:        store,
		now:          now,
		sessionTTL:   sessionTTL,
		reauthWindow: defaultPasswordReauthLimit,
		dummyHash:    dummyHash,
	}, nil
}

func (service *PasswordService) Login(
	ctx context.Context,
	email, password string,
) (string, time.Time, error) {
	normalized, normalizeErr := normalizePasswordEmail(email)
	credential, exists, err := service.store.CredentialByEmail(ctx, normalized)
	if err != nil {
		return "", time.Time{}, err
	}
	hash := credential.PasswordHash
	if !exists || normalizeErr != nil {
		hash = service.dummyHash
	}
	valid, verifyErr := VerifyPassword(hash, password)
	if verifyErr != nil {
		return "", time.Time{}, verifyErr
	}
	now := service.now().UTC()
	locked := credential.LockedUntil != nil && credential.LockedUntil.After(now)
	if !exists || normalizeErr != nil || !valid || locked {
		if exists {
			_ = service.store.RecordPasswordFailure(ctx, credential.AccountID, now)
		}
		return "", time.Time{}, ErrInvalidCredentials
	}
	token, err := randomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	sessionID, err := randomToken(18)
	if err != nil {
		return "", time.Time{}, err
	}
	expiry := now.Add(service.sessionTTL)
	if err := service.store.CompletePasswordLogin(ctx, PasswordSession{
		ID:              sessionID,
		AccountID:       credential.AccountID,
		TokenHash:       tokenDigest(token),
		CreatedAt:       now,
		AuthenticatedAt: now,
		ExpiresAt:       expiry,
	}, secureEventID(), now); err != nil {
		return "", time.Time{}, err
	}
	return token, expiry, nil
}

func (service *PasswordService) ChangePassword(
	ctx context.Context,
	sessionToken, csrfToken, currentPassword, newPassword, confirmation string,
) (string, time.Time, error) {
	if strings.TrimSpace(sessionToken) == "" {
		return "", time.Time{}, ErrPasswordUnauthenticated
	}
	if !validPasswordCSRFToken(sessionToken, csrfToken) {
		return "", time.Time{}, ErrPasswordCSRFInvalid
	}
	if newPassword != confirmation {
		return "", time.Time{}, ErrPasswordConfirmation
	}
	if err := validatePassword(newPassword); err != nil {
		return "", time.Time{}, ErrPasswordPolicy
	}

	now := service.now().UTC()
	tokenHash := tokenDigest(sessionToken)
	session, exists, err := service.store.PasswordSession(ctx, tokenHash, now)
	if err != nil {
		return "", time.Time{}, err
	}
	if !exists {
		return "", time.Time{}, ErrPasswordUnauthenticated
	}
	if session.ChangeLockedUntil != nil && session.ChangeLockedUntil.After(now) {
		return "", time.Time{}, ErrPasswordChangeRateLimited
	}
	if session.AuthenticatedAt.After(now) ||
		now.Sub(session.AuthenticatedAt) > service.reauthWindow {
		return "", time.Time{}, ErrPasswordReauthRequired
	}
	valid, err := VerifyPassword(session.PasswordHash, currentPassword)
	if err != nil {
		return "", time.Time{}, err
	}
	if !valid {
		if recordErr := service.store.RecordPasswordChangeFailure(
			ctx,
			session.AccountID,
			secureEventID(),
			now,
		); recordErr != nil {
			return "", time.Time{}, recordErr
		}
		return "", time.Time{}, ErrCurrentPasswordInvalid
	}
	samePassword, err := VerifyPassword(session.PasswordHash, newPassword)
	if err != nil {
		return "", time.Time{}, err
	}
	if samePassword {
		return "", time.Time{}, ErrPasswordPolicy
	}
	newHash, err := HashPassword(newPassword, DefaultPasswordParameters())
	if err != nil {
		return "", time.Time{}, err
	}
	newToken, err := randomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	sessionID, err := randomToken(18)
	if err != nil {
		return "", time.Time{}, err
	}
	expiry := now.Add(service.sessionTTL)
	change := PasswordChange{
		AccountID:               session.AccountID,
		CurrentPasswordHash:     session.PasswordHash,
		CurrentSessionTokenHash: tokenHash,
		NewPasswordHash:         newHash,
		NewSession: PasswordSession{
			ID:              sessionID,
			AccountID:       session.AccountID,
			TokenHash:       tokenDigest(newToken),
			CreatedAt:       now,
			AuthenticatedAt: now,
			ExpiresAt:       expiry,
		},
	}
	if err := service.store.CompletePasswordChange(
		ctx,
		change,
		secureEventID(),
		now,
	); err != nil {
		return "", time.Time{}, err
	}
	return newToken, expiry, nil
}

func (service *PasswordService) Logout(
	ctx context.Context,
	sessionToken, csrfToken string,
) error {
	if strings.TrimSpace(sessionToken) == "" {
		return nil
	}
	if !validPasswordCSRFToken(sessionToken, csrfToken) {
		return ErrPasswordCSRFInvalid
	}
	return service.store.RevokePasswordSession(
		ctx,
		tokenDigest(sessionToken),
		secureEventID(),
		service.now().UTC(),
	)
}

func validPasswordCSRFToken(sessionToken, supplied string) bool {
	expected := sha256.Sum256([]byte("postqron-admin-csrf\x00" + sessionToken))
	expectedHex := hex.EncodeToString(expected[:])
	return len(supplied) == len(expectedHex) &&
		subtle.ConstantTimeCompare([]byte(supplied), []byte(expectedHex)) == 1
}

func normalizePasswordEmail(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" ||
		len(normalized) > 320 ||
		strings.Count(normalized, "@") != 1 ||
		strings.ContainsAny(normalized, "\r\n\t ") {
		return "", errors.New("email is invalid")
	}
	parts := strings.Split(normalized, "@")
	if parts[0] == "" || !strings.Contains(parts[1], ".") {
		return "", errors.New("email is invalid")
	}
	return normalized, nil
}

func tokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func secureEventID() string {
	value, err := randomToken(18)
	if err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return value
}
