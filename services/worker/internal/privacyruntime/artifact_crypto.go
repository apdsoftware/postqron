package privacyruntime

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
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

type encryptedArtifactWriter struct {
	destination io.Writer
	aead        cipher.AEAD
	buffer      []byte
	counter     uint64
	closed      bool
}

func newEncryptedArtifactWriter(destination io.Writer, key [32]byte) (*encryptedArtifactWriter, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if _, err := destination.Write(artifactMagic[:]); err != nil {
		return nil, err
	}
	return &encryptedArtifactWriter{
		destination: destination,
		aead:        aead,
		buffer:      make([]byte, 0, artifactChunkSize),
	}, nil
}

func (writer *encryptedArtifactWriter) Write(payload []byte) (int, error) {
	if writer.closed {
		return 0, errors.New("encrypted artifact writer is closed")
	}
	written := 0
	for len(payload) > 0 {
		space := artifactChunkSize - len(writer.buffer)
		take := min(space, len(payload))
		writer.buffer = append(writer.buffer, payload[:take]...)
		payload = payload[take:]
		written += take
		if len(writer.buffer) == artifactChunkSize {
			if err := writer.flush(false); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (writer *encryptedArtifactWriter) Close() error {
	if writer.closed {
		return nil
	}
	writer.closed = true
	if len(writer.buffer) > 0 {
		if err := writer.flush(false); err != nil {
			return err
		}
	}
	return writer.flush(true)
}

func (writer *encryptedArtifactWriter) flush(final bool) error {
	nonce := make([]byte, writer.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	var aad [17]byte
	copy(aad[:8], artifactMagic[:])
	binary.BigEndian.PutUint64(aad[8:16], writer.counter)
	if final {
		aad[16] = 1
	}
	ciphertext := writer.aead.Seal(nil, nonce, writer.buffer, aad[:])
	if len(ciphertext) > artifactChunkSize+writer.aead.Overhead() {
		return fmt.Errorf("encrypted artifact chunk is too large")
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(ciphertext)))
	for _, part := range [][]byte{nonce, size[:], ciphertext} {
		if _, err := writer.destination.Write(part); err != nil {
			return err
		}
	}
	writer.buffer = writer.buffer[:0]
	writer.counter++
	return nil
}
