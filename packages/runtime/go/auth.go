package runtime

import "context"

type authenticatedAccountKey struct{}

// WithAuthenticatedAccount records the account established by the API
// authentication middleware. Feature modules must not derive this value from
// client-controlled headers.
func WithAuthenticatedAccount(ctx context.Context, accountID string) context.Context {
	return context.WithValue(ctx, authenticatedAccountKey{}, accountID)
}

// AuthenticatedAccount returns the account established by the API
// authentication middleware.
func AuthenticatedAccount(ctx context.Context) (string, bool) {
	accountID, ok := ctx.Value(authenticatedAccountKey{}).(string)
	return accountID, ok && accountID != ""
}
