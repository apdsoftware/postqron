ALTER TABLE auth_accounts
    ADD COLUMN access_state text NOT NULL DEFAULT 'active',
    ADD COLUMN access_frozen_at timestamptz,
    ADD COLUMN access_finalized_at timestamptz,
    ADD CONSTRAINT auth_accounts_access_state_check CHECK (
        access_state IN ('active', 'frozen', 'finalized')
    ),
    ADD CONSTRAINT auth_accounts_access_time_check CHECK (
        (access_state = 'active' AND access_finalized_at IS NULL)
        OR (access_state = 'frozen' AND access_frozen_at IS NOT NULL
            AND access_finalized_at IS NULL)
        OR (access_state = 'finalized' AND access_frozen_at IS NOT NULL
            AND access_finalized_at IS NOT NULL)
    );

CREATE INDEX auth_accounts_access_state_idx
    ON auth_accounts (access_state);

-- Database guards keep a rolled-back application fail-closed. Older binaries
-- cannot create an active session, identity link, or link attempt after F12
-- has frozen the account.
CREATE FUNCTION auth_require_active_session_account()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    current_access_state text;
BEGIN
    IF NEW.revoked_at IS NOT NULL THEN
        RETURN NEW;
    END IF;
    SELECT access_state INTO current_access_state
    FROM auth_accounts
    WHERE id = NEW.account_id
    FOR UPDATE;
    IF current_access_state IS DISTINCT FROM 'active' THEN
        RAISE EXCEPTION 'account access is unavailable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER auth_sessions_require_active_account
BEFORE INSERT OR UPDATE ON auth_sessions
FOR EACH ROW
EXECUTE FUNCTION auth_require_active_session_account();

CREATE FUNCTION auth_require_active_identity_account()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    current_access_state text;
BEGIN
    SELECT access_state INTO current_access_state
    FROM auth_accounts
    WHERE id = NEW.account_id
    FOR UPDATE;
    IF current_access_state IS DISTINCT FROM 'active' THEN
        RAISE EXCEPTION 'account access is unavailable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER auth_identities_require_active_account
BEFORE INSERT OR UPDATE ON auth_provider_identities
FOR EACH ROW
EXECUTE FUNCTION auth_require_active_identity_account();

CREATE FUNCTION auth_require_active_link_target()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    current_access_state text;
BEGIN
    IF NEW.intent <> 'link' OR NEW.status NOT IN ('pending', 'claimed') THEN
        RETURN NEW;
    END IF;
    SELECT access_state INTO current_access_state
    FROM auth_accounts
    WHERE id = NEW.target_account_id
    FOR UPDATE;
    IF current_access_state IS DISTINCT FROM 'active' THEN
        RAISE EXCEPTION 'account access is unavailable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER auth_attempts_require_active_link_target
BEFORE INSERT OR UPDATE ON auth_oauth_attempts
FOR EACH ROW
EXECUTE FUNCTION auth_require_active_link_target();
