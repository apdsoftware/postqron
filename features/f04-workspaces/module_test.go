package workspaces

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestWorkspaceEmailDigestKeyUsesDomainSeparatedAuthKey(t *testing.T) {
	authKey := []byte("0123456789abcdef0123456789abcdef")
	t.Setenv(authEncryptionKeyEnv, base64.StdEncoding.EncodeToString(authKey))

	first := workspaceEmailDigestKeyFromEnv()
	second := workspaceEmailDigestKeyFromEnv()
	if len(first) != 32 {
		t.Fatalf("derived key length = %d, want 32", len(first))
	}
	if !bytes.Equal(first, second) {
		t.Fatal("derived workspace email key is not deterministic")
	}
	if bytes.Equal(first, authKey) {
		t.Fatal("workspace email key reused the auth encryption key directly")
	}
}

func TestWorkspaceEmailDigestKeyRejectsInvalidConfiguration(t *testing.T) {
	for _, value := range []string{
		"",
		"not-base64",
		base64.StdEncoding.EncodeToString(make([]byte, 31)),
	} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(authEncryptionKeyEnv, value)
			if key := workspaceEmailDigestKeyFromEnv(); key != nil {
				t.Fatalf("derived key = %x, want nil", key)
			}
		})
	}
}
