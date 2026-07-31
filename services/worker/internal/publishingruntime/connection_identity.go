package publishingruntime

import (
	"context"
	"strings"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

type credentialReader interface {
	GetCredential(
		context.Context, string, string,
	) (socialconnections.StoredCredential, error)
}

// F5ConnectionIdentityResolver derives provider identity only from the
// server-side F5 connection record. Credential material returned by the F5
// repository remains inside this worker boundary and is never passed to F8.
type F5ConnectionIdentityResolver struct {
	repository credentialReader
}

func NewF5ConnectionIdentityResolver(
	repository credentialReader,
) (*F5ConnectionIdentityResolver, error) {
	if repository == nil {
		return nil, publishing.ErrInvalidArgument
	}
	return &F5ConnectionIdentityResolver{repository: repository}, nil
}

func (resolver *F5ConnectionIdentityResolver) RemoteID(
	ctx context.Context,
	workspaceID, connectionID string,
	expected socialconnections.Provider,
) (string, error) {
	if resolver == nil || resolver.repository == nil ||
		strings.TrimSpace(workspaceID) == "" ||
		strings.TrimSpace(connectionID) == "" {
		return "", publishing.ErrInvalidArgument
	}
	stored, err := resolver.repository.GetCredential(
		ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(connectionID),
	)
	if err != nil {
		return "", &publishing.ProviderError{
			Code:      "connection_identity_unavailable",
			Detail:    "The trusted connection identity could not be loaded.",
			Retryable: true,
		}
	}
	if stored.Provider != expected ||
		stored.Status != socialconnections.StatusConnected ||
		strings.TrimSpace(stored.RemoteID) == "" {
		return "", &publishing.ProviderError{
			Code:   "connection_identity_mismatch",
			Detail: "The trusted connection does not match the publishing provider.",
		}
	}
	return strings.TrimSpace(stored.RemoteID), nil
}
