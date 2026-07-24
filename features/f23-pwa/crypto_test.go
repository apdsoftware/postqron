package pwa

import (
	"bytes"
	"testing"
)

func TestPushCredentialsAreEncryptedWithBoundAdditionalData(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x42}, 32)
	cipher, err := NewAESGCMCipher("push-key-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("https://push.example.test/device-secret")
	additionalData := credentialAAD("subscription_1", "endpoint")
	sealed, err := cipher.Seal(plaintext, additionalData)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed.Data, plaintext) {
		t.Fatal("ciphertext contains plaintext push endpoint")
	}
	opened, err := cipher.Open(sealed, additionalData)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened %q, want %q", opened, plaintext)
	}
	if _, err := cipher.Open(
		sealed,
		credentialAAD("subscription_2", "endpoint"),
	); err == nil {
		t.Fatal("ciphertext opened for another subscription")
	}
}
