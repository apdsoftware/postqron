package email

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

type Unsubscribe struct {
	baseURL    *url.URL
	secrets    SecretProvider
	secretName string
	store      Store
	now        func() time.Time
}

func NewUnsubscribe(
	baseURL string,
	secrets SecretProvider,
	secretName string,
	store Store,
) (*Unsubscribe, error) {
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("unsubscribe base URL must use HTTPS")
	}
	if secrets == nil || !validSecretName(secretName) || store == nil {
		return nil, errors.New("unsubscribe secret provider, secret name, and store are required")
	}
	return &Unsubscribe{
		baseURL:    parsed,
		secrets:    secrets,
		secretName: secretName,
		store:      store,
		now:        time.Now,
	}, nil
}

func (service *Unsubscribe) URL(
	ctx context.Context,
	recipientID string,
) (string, error) {
	if strings.TrimSpace(recipientID) == "" {
		return "", ErrInvalidRecipient
	}
	key, err := service.key(ctx)
	if err != nil {
		return "", err
	}
	aead, err := unsubscribeAEAD(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("create unsubscribe nonce: %w", err)
	}
	sealed := aead.Seal(nonce, nonce, []byte(recipientID), []byte("unsubscribe:v1"))
	token := "v1." + base64.RawURLEncoding.EncodeToString(sealed)
	target := *service.baseURL
	query := target.Query()
	query.Set("token", token)
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func (service *Unsubscribe) Apply(
	ctx context.Context,
	token string,
) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] != "v1" {
		return "", errors.New("invalid unsubscribe token")
	}
	key, err := service.key(ctx)
	if err != nil {
		return "", err
	}
	aead, err := unsubscribeAEAD(key)
	if err != nil {
		return "", err
	}
	sealed, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(sealed) <= aead.NonceSize() {
		return "", errors.New("invalid unsubscribe token")
	}
	nonce, ciphertext := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	decoded, err := aead.Open(
		nil,
		nonce,
		ciphertext,
		[]byte("unsubscribe:v1"),
	)
	if err != nil || len(decoded) == 0 || len(decoded) > 255 {
		return "", errors.New("invalid unsubscribe token")
	}
	recipientID := string(decoded)
	if err := service.store.Suppress(ctx, Suppression{
		RecipientID: recipientID,
		Scope:       SuppressMarketing,
		Reason:      "unsubscribe",
		OccurredAt:  service.now().UTC(),
	}); err != nil {
		return "", err
	}
	return recipientID, nil
}

func (service *Unsubscribe) key(ctx context.Context) ([]byte, error) {
	value, err := service.secrets.Secret(ctx, service.secretName)
	if err != nil || len(value) < 32 {
		return nil, errors.New("unsubscribe signing secret is unavailable")
	}
	return []byte(value), nil
}

func unsubscribeAEAD(key []byte) (cipher.AEAD, error) {
	sum := sha256.Sum256(key)
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("create unsubscribe cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create unsubscribe AEAD: %w", err)
	}
	return aead, nil
}
