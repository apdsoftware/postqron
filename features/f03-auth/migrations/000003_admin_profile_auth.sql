ALTER TABLE auth_password_credentials
    ADD COLUMN password_change_failed_attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN password_change_locked_until timestamptz,
    ADD CONSTRAINT auth_password_change_failed_attempts_nonnegative
        CHECK (password_change_failed_attempts >= 0);

ALTER TABLE auth_security_events
    DROP CONSTRAINT auth_security_events_event_type_check;

ALTER TABLE auth_security_events
    ADD CONSTRAINT auth_security_events_event_type_check CHECK (
        event_type IN (
            'password.bootstrap',
            'password.login_succeeded',
            'password.login_failed',
            'password.changed',
            'password.change_failed',
            'password.reset_requested',
            'password.reset_completed',
            'email.verification_requested',
            'email.verified',
            'session.logged_out'
        )
    );
