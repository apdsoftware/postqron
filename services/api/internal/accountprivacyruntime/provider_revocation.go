package accountprivacyruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	auth "github.com/apdsoftware/postqron/features/f03-auth"
	accountprivacy "github.com/apdsoftware/postqron/features/f12-account-privacy"
	authruntime "github.com/apdsoftware/postqron/services/api/internal/authruntime"
)

type runtimeProviderRevoker struct {
	database *sql.DB
}

func (revoker runtimeProviderRevoker) RevokeForDeletion(
	ctx context.Context,
	request accountprivacy.DeletionRequest,
	workspaceIDs []string,
) error {
	var revocableSocial int
	if err := revoker.database.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM f05_social_connections
		WHERE workspace_id = ANY($1)
		  AND status <> 'revoked'
		  AND (
			access_token_key_id IS NOT NULL
			OR access_token_ciphertext IS NOT NULL
			OR refresh_token_key_id IS NOT NULL
			OR refresh_token_ciphertext IS NOT NULL
			OR token_expires_at IS NOT NULL
			OR refresh_locked_until IS NOT NULL
		  )`,
		workspaceIDs).Scan(&revocableSocial); err != nil {
		return fmt.Errorf("inspect social revocation boundary: %w", err)
	}
	if revocableSocial > 0 {
		return errors.New("public social provider revocation boundary is unavailable")
	}
	if request.Scope != accountprivacy.DeleteAccount {
		return nil
	}
	rows, err := revoker.database.QueryContext(ctx, `
		SELECT provider, COALESCE(revocation_token_ciphertext, ''::bytea)
		FROM auth_provider_identities
		WHERE account_id = $1
		ORDER BY provider`, request.AccountID)
	if err != nil {
		return fmt.Errorf("list identity revocation credentials: %w", err)
	}
	defer rows.Close()
	type credential struct {
		provider   auth.Provider
		ciphertext []byte
	}
	var credentials []credential
	for rows.Next() {
		var item credential
		if err := rows.Scan(&item.provider, &item.ciphertext); err != nil {
			return fmt.Errorf("scan identity revocation credential: %w", err)
		}
		credentials = append(credentials, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate identity revocation credentials: %w", err)
	}
	adapters := authruntime.RuntimeProviderAdapters()
	sealer := authruntime.RuntimeSealerFromEnv()
	for _, item := range credentials {
		if len(item.ciphertext) == 0 {
			continue
		}
		adapter := adapters[item.provider]
		if adapter == nil || sealer == nil {
			return errors.New("identity provider revocation boundary is unavailable")
		}
		token, err := sealer.Open(item.ciphertext)
		if err != nil {
			return errors.New("identity provider revocation credential is unavailable")
		}
		if err := adapter.Revoke(ctx, string(token)); err != nil {
			return fmt.Errorf("identity provider revocation failed: %w", err)
		}
	}
	if _, err := revoker.database.ExecContext(ctx, `
		UPDATE auth_provider_identities
		SET revocation_token_ciphertext = NULL
		WHERE account_id = $1`, request.AccountID); err != nil {
		return fmt.Errorf("delete local identity revocation credentials: %w", err)
	}
	return nil
}
