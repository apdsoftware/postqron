package privacyruntime

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestEncryptedArtifactWriterStoresNoPlaintext(t *testing.T) {
	t.Parallel()
	key := [32]byte{1, 2, 3}
	plaintext := bytes.Repeat([]byte("sensitive-export-record\n"), 10_000)
	var encrypted bytes.Buffer
	writer, err := newEncryptedArtifactWriter(&encrypted, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(plaintext); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted.Bytes(), []byte("sensitive-export-record")) {
		t.Fatal("artifact persisted plaintext")
	}
	if !bytes.HasPrefix(encrypted.Bytes(), artifactMagic[:]) {
		t.Fatal("encrypted artifact header missing")
	}
}

func TestArtifactKeyFailsClosedInProduction(t *testing.T) {
	t.Setenv("POSTQRON_ENV", "production")
	t.Setenv("POSTQRON_PRIVACY_ARTIFACT_KEY_B64", "")
	if _, err := artifactKeyFromEnv(); err == nil {
		t.Fatal("expected missing production key rejection")
	}

	t.Setenv("POSTQRON_PRIVACY_ARTIFACT_KEY_B64", base64.StdEncoding.EncodeToString(make([]byte, 31)))
	if _, err := artifactKeyFromEnv(); err == nil {
		t.Fatal("expected invalid production key rejection")
	}

	t.Setenv("POSTQRON_PRIVACY_ARTIFACT_KEY_B64", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if _, err := artifactKeyFromEnv(); err != nil {
		t.Fatalf("valid production key rejected: %v", err)
	}
}
