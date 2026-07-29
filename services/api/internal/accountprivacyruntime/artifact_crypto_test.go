package accountprivacyruntime

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"testing"
)

func TestDecryptArtifactRejectsTamperAndWrongKeyWithoutPlaintext(t *testing.T) {
	t.Parallel()
	key := [32]byte{1, 2, 3}
	plaintext := []byte("privacy export plaintext must never survive at rest")
	encrypted := encryptArtifactFixture(t, key, plaintext)
	if bytes.Contains(encrypted, plaintext) {
		t.Fatal("encrypted artifact contains plaintext")
	}

	var decoded bytes.Buffer
	if err := decryptArtifact(&decoded, bytes.NewReader(encrypted), key); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Bytes(), plaintext) {
		t.Fatal("decrypted artifact mismatch")
	}

	wrongKey := [32]byte{9, 8, 7}
	decoded.Reset()
	if err := decryptArtifact(&decoded, bytes.NewReader(encrypted), wrongKey); err == nil {
		t.Fatal("expected wrong key rejection")
	}
	if decoded.Len() != 0 {
		t.Fatal("wrong key emitted plaintext")
	}

	tampered := bytes.Clone(encrypted)
	tampered[8+12+4+len(plaintext)+16-1] ^= 0x80
	decoded.Reset()
	if err := decryptArtifact(&decoded, bytes.NewReader(tampered), key); err == nil {
		t.Fatal("expected tamper rejection")
	}
	if decoded.Len() != 0 {
		t.Fatal("tampered first chunk emitted plaintext")
	}

	truncated := encrypted[:len(encrypted)-(12+4+16)]
	decoded.Reset()
	if err := decryptArtifact(&decoded, bytes.NewReader(truncated), key); err == nil {
		t.Fatal("expected authenticated truncation rejection")
	}
}

func encryptArtifactFixture(t *testing.T, key [32]byte, plaintext []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	var aad [17]byte
	copy(aad[:8], artifactMagic[:])
	ciphertext := aead.Seal(nil, nonce, plaintext, aad[:])
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(ciphertext)))
	finalNonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(finalNonce); err != nil {
		t.Fatal(err)
	}
	var finalAAD [17]byte
	copy(finalAAD[:8], artifactMagic[:])
	binary.BigEndian.PutUint64(finalAAD[8:16], 1)
	finalAAD[16] = 1
	finalCiphertext := aead.Seal(nil, finalNonce, nil, finalAAD[:])
	var finalSize [4]byte
	binary.BigEndian.PutUint32(finalSize[:], uint32(len(finalCiphertext)))
	return bytes.Join([][]byte{
		artifactMagic[:],
		nonce, size[:], ciphertext,
		finalNonce, finalSize[:], finalCiphertext,
	}, nil)
}
