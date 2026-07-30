-- Recover accounts whose onboarding event was published before the F12/F10
-- bridge became complete. Both repairs are idempotent and retain the original
-- account/workspace timestamps rather than granting a fresh trial on deploy.

INSERT INTO account_privacy_profiles (
    account_id,
    display_name,
    locale,
    timezone,
    updated_at
)
SELECT account.id,
       left(
           coalesce(
               nullif(btrim(account.display_name), ''),
               nullif(btrim(split_part(account.email, '@', 1)), ''),
               'Postqron user'
           ),
           100
       ),
       'it-IT',
       'Europe/Rome',
       account.created_at
  FROM auth_accounts AS account
 WHERE NOT EXISTS (
     SELECT 1
       FROM account_privacy_profiles AS profile
      WHERE profile.account_id = account.id
 )
ON CONFLICT (account_id) DO NOTHING;

SELECT f10_provision_trial(workspace.id::text, workspace.created_at)
  FROM f04_workspaces AS workspace
 WHERE NOT EXISTS (
     SELECT 1
       FROM f10_workspace_billing AS billing
      WHERE billing.workspace_id = workspace.id
 );
