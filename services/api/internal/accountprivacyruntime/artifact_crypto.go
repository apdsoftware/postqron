package accountprivacyruntime

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const artifactChunkSize = 64 << 10

var artifactMagic = [8]byte{'P', 'Q', 'P', 'R', 'I', 'V', '1', 0}

func artifactKeyFromEnv() ([32]byte, error) {
	var key [32]byte
	encoded := strings.TrimSpace(os.Getenv("POSTQRON_PRIVACY_ARTIFACT_KEY_B64"))
	if encoded == "" {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("POSTQRON_ENV")), "production") {
			return key, errors.New("POSTQRON_PRIVACY_ARTIFACT_KEY_B64 is required in production")
		}
		copy(key[:], []byte("postqron-dev-privacy-key-32byte!"))
		return key, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != len(key) {
		return key, errors.New("POSTQRON_PRIVACY_ARTIFACT_KEY_B64 must encode exactly 32 bytes")
	}
	copy(key[:], decoded)
	return key, nil
}

func decryptArtifact(destination io.Writer, source io.Reader, key [32]byte) error {
	var magic [8]byte
	if _, err := io.ReadFull(source, magic[:]); err != nil {
		return errors.New("encrypted artifact header is unavailable")
	}
	if magic != artifactMagic {
		return errors.New("encrypted artifact header is invalid")
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	var counter uint64
	for {
		nonce := make([]byte, aead.NonceSize())
		if _, err := io.ReadFull(source, nonce); err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("encrypted artifact terminal record is missing")
			}
			return errors.New("encrypted artifact nonce is truncated")
		}
		var sizeBytes [4]byte
		if _, err := io.ReadFull(source, sizeBytes[:]); err != nil {
			return errors.New("encrypted artifact size is truncated")
		}
		size := binary.BigEndian.Uint32(sizeBytes[:])
		if size < uint32(aead.Overhead()) || size > uint32(artifactChunkSize+aead.Overhead()) {
			return errors.New("encrypted artifact chunk size is invalid")
		}
		ciphertext := make([]byte, size)
		if _, err := io.ReadFull(source, ciphertext); err != nil {
			return errors.New("encrypted artifact chunk is truncated")
		}
		final := size == uint32(aead.Overhead())
		var aad [17]byte
		copy(aad[:8], artifactMagic[:])
		binary.BigEndian.PutUint64(aad[8:16], counter)
		if final {
			aad[16] = 1
		}
		plaintext, err := aead.Open(nil, nonce, ciphertext, aad[:])
		if err != nil {
			return fmt.Errorf("authenticate encrypted artifact chunk: %w", err)
		}
		if final {
			var trailing [1]byte
			if n, err := io.ReadFull(source, trailing[:]); n != 0 || !errors.Is(err, io.EOF) {
				return errors.New("encrypted artifact has trailing data")
			}
			return nil
		}
		if _, err := destination.Write(plaintext); err != nil {
			return err
		}
		counter++
	}
}
