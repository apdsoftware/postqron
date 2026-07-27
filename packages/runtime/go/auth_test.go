package runtime

import (
	"context"
	"testing"
)

func TestAuthenticatedAccountIsOnlyReadFromRuntimeContext(t *testing.T) {
	if accountID, ok := AuthenticatedAccount(context.Background()); ok || accountID != "" {
		t.Fatal("unauthenticated context returned an account")
	}
	ctx := WithAuthenticatedAccount(context.Background(), "account-1")
	if accountID, ok := AuthenticatedAccount(ctx); !ok || accountID != "account-1" {
		t.Fatalf("authenticated account = %q, %v", accountID, ok)
	}
}
