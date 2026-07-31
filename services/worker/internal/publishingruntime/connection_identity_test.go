package publishingruntime

import (
	"context"
	"testing"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
)

type credentialReaderFunc func(
	context.Context, string, string,
) (socialconnections.StoredCredential, error)

func (read credentialReaderFunc) GetCredential(
	ctx context.Context,
	workspaceID, connectionID string,
) (socialconnections.StoredCredential, error) {
	return read(ctx, workspaceID, connectionID)
}

func TestF5ConnectionIdentityResolverUsesTrustedRemoteID(t *testing.T) {
	resolver, err := NewF5ConnectionIdentityResolver(credentialReaderFunc(func(
		_ context.Context, workspaceID, connectionID string,
	) (socialconnections.StoredCredential, error) {
		if workspaceID != "workspace" || connectionID != "connection" {
			t.Fatal("untrusted connection lookup")
		}
		return socialconnections.StoredCredential{
			Connection: socialconnections.Connection{
				Provider: socialconnections.ProviderBluesky,
				Status:   socialconnections.StatusConnected,
				RemoteID: "did:plc:trusted",
			},
		}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	remoteID, err := resolver.RemoteID(
		context.Background(), "workspace", "connection",
		socialconnections.ProviderBluesky,
	)
	if err != nil || remoteID != "did:plc:trusted" {
		t.Fatalf("remoteID=%q error=%v", remoteID, err)
	}
	if _, err = resolver.RemoteID(
		context.Background(), "workspace", "connection",
		socialconnections.ProviderMastodon,
	); err == nil {
		t.Fatal("provider mismatch accepted")
	}
}
