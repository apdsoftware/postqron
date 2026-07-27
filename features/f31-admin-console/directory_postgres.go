package adminconsole

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (store *PostgresStore) ListUsers(
	ctx context.Context,
	query UserDirectoryQuery,
) (UserDirectoryPage, error) {
	return store.queryUsers(ctx, query, "")
}

func (store *PostgresStore) User(
	ctx context.Context,
	accountID string,
) (UserDirectoryItem, bool, error) {
	query, err := normalizeUserDirectoryQuery(UserDirectoryQuery{
		Page:      1,
		PageSize:  10,
		Sort:      "registered_at",
		Direction: "desc",
	})
	if err != nil {
		return UserDirectoryItem{}, false, err
	}
	result, err := store.queryUsers(ctx, query, accountID)
	if err != nil {
		return UserDirectoryItem{}, false, err
	}
	if len(result.Items) == 0 {
		return UserDirectoryItem{}, false, nil
	}
	return result.Items[0], true, nil
}

func (store *PostgresStore) queryUsers(
	ctx context.Context,
	query UserDirectoryQuery,
	accountID string,
) (UserDirectoryPage, error) {
	args := []any{store.clock().UTC()}
	where := []string{"TRUE"}
	add := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if accountID != "" {
		where = append(where, "directory.id = "+add(accountID))
	}
	if query.Search != "" {
		parameter := add("%" + escapeLike(query.Search) + "%")
		where = append(where, `(
			directory.email ILIKE `+parameter+` ESCAPE '\'
			OR directory.display_name ILIKE `+parameter+` ESCAPE '\'
		)`)
	}
	if query.Status != "" {
		where = append(where, "directory.account_status = "+add(query.Status))
	}
	if query.EmailVerified != nil {
		where = append(where, "directory.email_verified = "+add(*query.EmailVerified))
	}
	if query.Plan != "" {
		parameter := add(query.Plan)
		where = append(where, `EXISTS (
			SELECT 1
			FROM f04_memberships membership
			JOIN f04_workspaces workspace
			  ON workspace.id = membership.workspace_id
			LEFT JOIN f10_workspace_billing billing
			  ON billing.workspace_id::text = workspace.id
			LEFT JOIN f10_internal_entitlement_overrides internal
			  ON internal.workspace_id::text = workspace.id
			WHERE membership.account_id = directory.id
			  AND membership.status = 'active'
			  AND CASE
			      WHEN COALESCE(internal.active, false) THEN 'internal'
			      ELSE COALESCE(billing.plan_code, 'unassigned')
			  END = `+parameter+`
		)`)
	}
	if query.LoginMethod != "" {
		parameter := add(query.LoginMethod)
		where = append(where, `(
			(`+parameter+` = 'password' AND EXISTS (
				SELECT 1 FROM auth_password_credentials password
				WHERE password.account_id = directory.id
			))
			OR EXISTS (
				SELECT 1 FROM auth_provider_identities identity
				WHERE identity.account_id = directory.id
				  AND identity.provider = `+parameter+`
			)
		)`)
	}
	appendInstantRange(
		&where, add, "directory.registered_at",
		query.RegisteredFrom, query.RegisteredTo,
	)
	appendInstantRange(
		&where, add, "directory.last_login_at",
		query.LastLoginFrom, query.LastLoginTo,
	)

	sortColumn := map[string]string{
		"email":           "email",
		"display_name":    "display_name",
		"status":          "account_status",
		"email_verified":  "email_verified",
		"registered_at":   "registered_at",
		"last_login_at":   "last_login_at",
		"active_sessions": "active_sessions",
	}[query.Sort]
	direction := strings.ToUpper(query.Direction)
	limit := add(query.PageSize)
	offset := add((query.Page - 1) * query.PageSize)
	statement := `
		WITH directory AS (
			SELECT
				account.id,
				account.email,
				COALESCE(NULLIF(account.display_name, ''), account.email) AS display_name,
				CASE
					WHEN password.locked_until > $1 THEN 'locked'
					ELSE 'active'
				END AS account_status,
				(
					account.email_verified_at IS NOT NULL
					OR EXISTS (
						SELECT 1
						FROM auth_provider_identities identity
						WHERE identity.account_id = account.id
						  AND lower(btrim(identity.provider_email)) =
						      account.normalized_email
					)
				) AS email_verified,
				COALESCE((
					SELECT jsonb_agg(method ORDER BY method)
					FROM (
						SELECT 'password'::text AS method
						WHERE password.account_id IS NOT NULL
						UNION
						SELECT identity.provider
						FROM auth_provider_identities identity
						WHERE identity.account_id = account.id
					) login_method
				), '[]'::jsonb)::text AS login_methods,
				account.created_at AS registered_at,
				(
					SELECT max(session.authenticated_at)
					FROM auth_sessions session
					WHERE session.account_id = account.id
				) AS last_login_at,
				(
					SELECT count(*)
					FROM auth_sessions session
					WHERE session.account_id = account.id
					  AND session.revoked_at IS NULL
					  AND session.expires_at > $1
				) AS active_sessions,
				COALESCE((
					SELECT jsonb_agg(
						jsonb_build_object(
							'id', workspace.id,
							'name', workspace.name,
							'role', membership.role::text,
							'plan_code', CASE
								WHEN COALESCE(internal.active, false) THEN 'internal'
								ELSE COALESCE(billing.plan_code, 'unassigned')
							END,
							'plan_status', CASE
								WHEN COALESCE(internal.active, false) THEN 'active'
								ELSE COALESCE(billing.billing_state, 'unassigned')
							END
						)
						ORDER BY workspace.name, workspace.id
					)
					FROM f04_memberships membership
					JOIN f04_workspaces workspace
					  ON workspace.id = membership.workspace_id
					LEFT JOIN f10_workspace_billing billing
					  ON billing.workspace_id::text = workspace.id
					LEFT JOIN f10_internal_entitlement_overrides internal
					  ON internal.workspace_id::text = workspace.id
					WHERE membership.account_id = account.id
					  AND membership.status = 'active'
				), '[]'::jsonb)::text AS workspaces
			FROM auth_accounts account
			LEFT JOIN auth_password_credentials password
			  ON password.account_id = account.id
		),
		filtered AS (
			SELECT *
			FROM directory
			WHERE ` + strings.Join(where, "\n AND ") + `
		),
		paged AS (
			SELECT *
			FROM filtered
			ORDER BY ` + sortColumn + ` ` + direction + ` NULLS LAST, id ` + direction + `
			LIMIT ` + limit + ` OFFSET ` + offset + `
		)
		SELECT
			paged.id,
			paged.email,
			paged.display_name,
			paged.account_status,
			paged.email_verified,
			paged.login_methods,
			paged.registered_at,
			paged.last_login_at,
			paged.active_sessions,
			paged.workspaces,
			totals.total
		FROM (SELECT count(*) AS total FROM filtered) totals
		LEFT JOIN paged ON TRUE
		ORDER BY paged.` + sortColumn + ` ` + direction + ` NULLS LAST,
		         paged.id ` + direction

	rows, err := store.database.QueryContext(ctx, statement, args...)
	if err != nil {
		return UserDirectoryPage{}, err
	}
	defer rows.Close()
	result := UserDirectoryPage{Items: []UserDirectoryItem{}}
	for rows.Next() {
		var (
			id, email, name, status, methods, workspaces sql.NullString
			verified                                     sql.NullBool
			registeredAt, lastLoginAt                    sql.NullTime
			activeSessions, total                        sql.NullInt64
		)
		if err := rows.Scan(
			&id,
			&email,
			&name,
			&status,
			&verified,
			&methods,
			&registeredAt,
			&lastLoginAt,
			&activeSessions,
			&workspaces,
			&total,
		); err != nil {
			return UserDirectoryPage{}, err
		}
		result.Total = int(total.Int64)
		if !id.Valid {
			continue
		}
		item := UserDirectoryItem{
			ID:             id.String,
			Email:          email.String,
			DisplayName:    name.String,
			AccountStatus:  status.String,
			EmailVerified:  verified.Bool,
			RegisteredAt:   registeredAt.Time.UTC(),
			ActiveSessions: int(activeSessions.Int64),
			LoginMethods:   []string{},
			Workspaces:     []UserWorkspaceMembership{},
		}
		if lastLoginAt.Valid {
			value := lastLoginAt.Time.UTC()
			item.LastLoginAt = &value
		}
		if err := json.Unmarshal([]byte(methods.String), &item.LoginMethods); err != nil {
			return UserDirectoryPage{}, err
		}
		if err := json.Unmarshal([]byte(workspaces.String), &item.Workspaces); err != nil {
			return UserDirectoryPage{}, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

func (store *PostgresStore) ListWorkspaces(
	ctx context.Context,
	query WorkspaceDirectoryQuery,
) (WorkspaceDirectoryPage, error) {
	args := []any{}
	where := []string{"TRUE"}
	add := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if query.Search != "" {
		parameter := add("%" + escapeLike(query.Search) + "%")
		where = append(where, `(
			directory.name ILIKE `+parameter+` ESCAPE '\'
			OR directory.id ILIKE `+parameter+` ESCAPE '\'
			OR directory.owner_email ILIKE `+parameter+` ESCAPE '\'
			OR directory.owner_display_name ILIKE `+parameter+` ESCAPE '\'
		)`)
	}
	if query.Status != "" {
		where = append(where, "directory.status = "+add(query.Status))
	}
	if query.Plan != "" {
		where = append(where, "directory.plan_code = "+add(query.Plan))
	}
	if query.Owner != "" {
		parameter := add("%" + escapeLike(query.Owner) + "%")
		where = append(where, `(
			directory.owner_email ILIKE `+parameter+` ESCAPE '\'
			OR directory.owner_display_name ILIKE `+parameter+` ESCAPE '\'
		)`)
	}
	appendInstantRange(
		&where, add, "directory.created_at",
		query.CreatedFrom, query.CreatedTo,
	)
	appendInstantRange(
		&where, add, "directory.updated_at",
		query.UpdatedFrom, query.UpdatedTo,
	)
	sortColumn := map[string]string{
		"name":          "name",
		"owner_email":   "owner_email",
		"status":        "status",
		"plan_code":     "plan_code",
		"member_count":  "member_count",
		"channel_count": "channel_count",
		"post_count":    "post_count",
		"created_at":    "created_at",
		"updated_at":    "updated_at",
	}[query.Sort]
	direction := strings.ToUpper(query.Direction)
	limit := add(query.PageSize)
	offset := add((query.Page - 1) * query.PageSize)
	statement := `
		WITH directory AS (
			SELECT
				workspace.id,
				workspace.name,
				owner.id AS owner_id,
				owner.email AS owner_email,
				COALESCE(NULLIF(owner.display_name, ''), owner.email) AS owner_display_name,
				workspace.status::text AS status,
				CASE
					WHEN COALESCE(internal.active, false) THEN 'internal'
					ELSE COALESCE(billing.plan_code, 'unassigned')
				END AS plan_code,
				CASE
					WHEN COALESCE(internal.active, false) THEN 'active'
					ELSE COALESCE(billing.billing_state, 'unassigned')
				END AS plan_status,
				(
					SELECT count(*)
					FROM f04_memberships membership
					WHERE membership.workspace_id = workspace.id
					  AND membership.status = 'active'
				) AS member_count,
				(
					SELECT count(*)
					FROM f05_social_connections connection
					WHERE connection.workspace_id = workspace.id
					  AND connection.status <> 'revoked'
				) AS channel_count,
				(
					SELECT count(*)
					FROM f07_scheduled_posts post
					WHERE post.workspace_id = workspace.id
				) AS post_count,
				workspace.created_at,
				workspace.updated_at
			FROM f04_workspaces workspace
			JOIN auth_accounts owner
			  ON owner.id = workspace.personal_account_id
			LEFT JOIN f10_workspace_billing billing
			  ON billing.workspace_id::text = workspace.id
			LEFT JOIN f10_internal_entitlement_overrides internal
			  ON internal.workspace_id::text = workspace.id
		),
		filtered AS (
			SELECT *
			FROM directory
			WHERE ` + strings.Join(where, "\n AND ") + `
		),
		paged AS (
			SELECT *
			FROM filtered
			ORDER BY ` + sortColumn + ` ` + direction + ` NULLS LAST, id ` + direction + `
			LIMIT ` + limit + ` OFFSET ` + offset + `
		)
		SELECT
			paged.id,
			paged.name,
			paged.owner_id,
			paged.owner_email,
			paged.owner_display_name,
			paged.status,
			paged.plan_code,
			paged.plan_status,
			paged.member_count,
			paged.channel_count,
			paged.post_count,
			paged.created_at,
			paged.updated_at,
			totals.total
		FROM (SELECT count(*) AS total FROM filtered) totals
		LEFT JOIN paged ON TRUE
		ORDER BY paged.` + sortColumn + ` ` + direction + ` NULLS LAST,
		         paged.id ` + direction
	rows, err := store.database.QueryContext(ctx, statement, args...)
	if err != nil {
		return WorkspaceDirectoryPage{}, err
	}
	defer rows.Close()
	result := WorkspaceDirectoryPage{Items: []WorkspaceDirectoryItem{}}
	for rows.Next() {
		var (
			id, name, ownerID, ownerEmail, ownerName sql.NullString
			status, planCode, planStatus             sql.NullString
			memberCount, channelCount, postCount     sql.NullInt64
			createdAt, updatedAt                     sql.NullTime
			total                                    sql.NullInt64
		)
		if err := rows.Scan(
			&id,
			&name,
			&ownerID,
			&ownerEmail,
			&ownerName,
			&status,
			&planCode,
			&planStatus,
			&memberCount,
			&channelCount,
			&postCount,
			&createdAt,
			&updatedAt,
			&total,
		); err != nil {
			return WorkspaceDirectoryPage{}, err
		}
		result.Total = int(total.Int64)
		if !id.Valid {
			continue
		}
		result.Items = append(result.Items, WorkspaceDirectoryItem{
			ID:               id.String,
			Name:             name.String,
			OwnerID:          ownerID.String,
			OwnerEmail:       ownerEmail.String,
			OwnerDisplayName: ownerName.String,
			Status:           status.String,
			PlanCode:         planCode.String,
			PlanStatus:       planStatus.String,
			MemberCount:      int(memberCount.Int64),
			ChannelCount:     int(channelCount.Int64),
			PostCount:        int(postCount.Int64),
			CreatedAt:        createdAt.Time.UTC(),
			UpdatedAt:        updatedAt.Time.UTC(),
		})
	}
	return result, rows.Err()
}

func appendInstantRange(
	where *[]string,
	add func(any) string,
	column string,
	from, to *time.Time,
) {
	if from != nil {
		*where = append(*where, column+" >= "+add(*from))
	}
	if to != nil {
		*where = append(*where, column+" < "+add(*to))
	}
}
