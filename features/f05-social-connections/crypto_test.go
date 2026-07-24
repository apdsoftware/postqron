package socialconnections

import (
	"bytes"
	"testing"
)

func TestAESGCMCipherBindsCiphertextToResource(t *testing.T) {
	cipher, err := NewAESGCMCipher(
		"social-key-2026-07",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	additionalData := credentialAdditionalData(
		"workspace-1",
		ProviderFacebookPages,
		"page-1",
	)
	sealed, err := cipher.Seal([]byte("provider-secret-token"), additionalData)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.KeyID != "social-key-2026-07" {
		t.Fatalf("key ID = %q", sealed.KeyID)
	}
	if bytes.Contains(sealed.Data, []byte("provider-secret-token")) {
		t.Fatal("ciphertext contains plaintext token")
	}
	opened, err := cipher.Open(sealed, additionalData)
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != "provider-secret-token" {
		t.Fatalf("opened token = %q", opened)
	}
	if _, err = cipher.Open(
		sealed,
		credentialAdditionalData(
			"workspace-2",
			ProviderFacebookPages,
			"page-1",
		),
	); err == nil {
		t.Fatal("ciphertext opened under a different workspace")
	}
	tampered := cloneCiphertext(sealed)
	tampered.Data[len(tampered.Data)-1] ^= 1
	if _, err = cipher.Open(tampered, additionalData); err == nil {
		t.Fatal("tampered ciphertext opened successfully")
	}
}

func TestAESGCMCipherRequiresExternalKeyIdentityAndAES256(t *testing.T) {
	if _, err := NewAESGCMCipher("", make([]byte, 32)); err == nil {
		t.Fatal("empty key ID accepted")
	}
	if _, err := NewAESGCMCipher("key", make([]byte, 16)); err == nil {
		t.Fatal("non-AES-256 key accepted")
	}
}
