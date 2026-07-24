package socialconnections

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

type AESGCMCipher struct {
	keyID string
	aead  cipher.AEAD
}

func NewAESGCMCipher(keyID string, key []byte) (*AESGCMCipher, error) {
	if keyID == "" {
		return nil, errors.New("social credential key ID is required")
	}
	if len(key) != 32 {
		return nil, errors.New("social credential key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create social credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create social credential AEAD: %w", err)
	}
	return &AESGCMCipher{keyID: keyID, aead: aead}, nil
}

func (cipher *AESGCMCipher) Seal(
	plaintext, additionalData []byte,
) (Ciphertext, error) {
	if len(plaintext) == 0 {
		return Ciphertext{KeyID: cipher.keyID}, nil
	}
	nonce := make([]byte, cipher.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Ciphertext{}, fmt.Errorf("generate social credential nonce: %w", err)
	}
	sealed := cipher.aead.Seal(nonce, nonce, plaintext, additionalData)
	return Ciphertext{KeyID: cipher.keyID, Data: sealed}, nil
}

func (cipher *AESGCMCipher) Open(
	ciphertext Ciphertext,
	additionalData []byte,
) ([]byte, error) {
	if len(ciphertext.Data) == 0 {
		return nil, nil
	}
	if ciphertext.KeyID != cipher.keyID {
		return nil, errors.New("social credential ciphertext uses an unavailable key")
	}
	nonceSize := cipher.aead.NonceSize()
	if len(ciphertext.Data) < nonceSize {
		return nil, errors.New("invalid social credential ciphertext")
	}
	plaintext, err := cipher.aead.Open(
		nil,
		ciphertext.Data[:nonceSize],
		ciphertext.Data[nonceSize:],
		additionalData,
	)
	if err != nil {
		return nil, errors.New("invalid social credential ciphertext")
	}
	return plaintext, nil
}

func randomOpaqueID(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func digest(value string) string {
	hashed := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hashed[:])
}

func pkceChallenge(verifier string) string {
	hashed := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hashed[:])
}
