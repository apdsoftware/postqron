package pwa

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

type Ciphertext struct {
	KeyID string
	Data  []byte
}

type CredentialCipher interface {
	Seal([]byte, []byte) (Ciphertext, error)
	Open(Ciphertext, []byte) ([]byte, error)
}

type AESGCMCipher struct {
	keyID string
	aead  cipher.AEAD
}

func NewAESGCMCipher(keyID string, key []byte) (*AESGCMCipher, error) {
	if keyID == "" {
		return nil, errors.New("push credential key ID is required")
	}
	if len(key) != 32 {
		return nil, errors.New("push credential key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create push credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create push credential AEAD: %w", err)
	}
	return &AESGCMCipher{keyID: keyID, aead: aead}, nil
}

func (value *AESGCMCipher) Seal(
	plaintext, additionalData []byte,
) (Ciphertext, error) {
	nonce := make([]byte, value.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Ciphertext{}, fmt.Errorf("generate push credential nonce: %w", err)
	}
	return Ciphertext{
		KeyID: value.keyID,
		Data: value.aead.Seal(
			nonce,
			nonce,
			plaintext,
			additionalData,
		),
	}, nil
}

func (value *AESGCMCipher) Open(
	ciphertext Ciphertext,
	additionalData []byte,
) ([]byte, error) {
	if ciphertext.KeyID != value.keyID {
		return nil, errors.New("push credential ciphertext uses an unavailable key")
	}
	nonceSize := value.aead.NonceSize()
	if len(ciphertext.Data) < nonceSize {
		return nil, errors.New("invalid push credential ciphertext")
	}
	plaintext, err := value.aead.Open(
		nil,
		ciphertext.Data[:nonceSize],
		ciphertext.Data[nonceSize:],
		additionalData,
	)
	if err != nil {
		return nil, errors.New("invalid push credential ciphertext")
	}
	return plaintext, nil
}
