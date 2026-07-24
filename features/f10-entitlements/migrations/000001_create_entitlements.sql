CREATE TABLE f10_public_plans (
    code text PRIMARY KEY,
    display_name text NOT NULL,
    currency text NOT NULL CHECK (currency = 'EUR'),
    monthly_price_cents bigint NOT NULL CHECK (monthly_price_cents > 0),
    annual_price_cents bigint NOT NULL CHECK (annual_price_cents > 0),
    member_limit bigint NOT NULL CHECK (member_limit > 0),
    channel_limit bigint NOT NULL CHECK (channel_limit > 0),
    scheduled_publication_limit bigint NOT NULL
        CHECK (scheduled_publication_limit > 0),
    CHECK (code IN ('start', 'pro', 'team'))
);

INSERT INTO f10_public_plans (
    code,
    display_name,
    currency,
    monthly_price_cents,
    annual_price_cents,
    member_limit,
    channel_limit,
    scheduled_publication_limit
) VALUES
    ('start', 'Start', 'EUR', 900, 9000, 1, 5, 100),
    ('pro', 'Pro', 'EUR', 2400, 24000, 5, 15, 500),
    ('team', 'Team', 'EUR', 4900, 49000, 15, 50, 2000);

CREATE TABLE f10_workspace_billing (
    workspace_id uuid PRIMARY KEY,
    plan_code text NOT NULL REFERENCES f10_public_plans(code),
    billing_interval text NOT NULL
        CHECK (billing_interval IN ('monthly', 'annual')),
    billing_state text NOT NULL CHECK (
        billing_state IN (
            'trialing',
            'active',
            'past_due',
            'trial_expired',
            'payment_restricted',
            'canceled'
        )
    ),
    stripe_customer_id text UNIQUE,
    stripe_subscription_id text UNIQUE,
    provider_period_start timestamptz NOT NULL,
    provider_period_end timestamptz NOT NULL,
    quota_anchor timestamptz NOT NULL,
    trial_started_at timestamptz,
    trial_ends_at timestamptz,
    last_provider_event_created_at timestamptz,
    last_provider_event_id text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (provider_period_end > provider_period_start),
    CHECK (
        (billing_state IN ('trialing', 'trial_expired')
            AND trial_started_at IS NOT NULL
            AND trial_ends_at IS NOT NULL
            AND trial_ends_at > trial_started_at)
        OR billing_state NOT IN ('trialing', 'trial_expired')
    )
);

CREATE TABLE f10_checkout_sessions (
    session_id text PRIMARY KEY
        CHECK (session_id LIKE 'cs_%' AND length(session_id) <= 255),
    workspace_id uuid NOT NULL,
    plan_code text NOT NULL REFERENCES f10_public_plans(code),
    billing_interval text NOT NULL
        CHECK (billing_interval IN ('monthly', 'annual')),
    customer_id text,
    subscription_id text UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    completed_at timestamptz,
    CHECK (
        (completed_at IS NULL AND customer_id IS NULL AND subscription_id IS NULL)
        OR (completed_at IS NOT NULL
            AND customer_id IS NOT NULL
            AND subscription_id IS NOT NULL)
    )
);

CREATE INDEX f10_checkout_sessions_workspace_idx
    ON f10_checkout_sessions (workspace_id, created_at DESC);

CREATE TABLE f10_provider_events (
    provider text NOT NULL CHECK (provider = 'stripe'),
    event_id text NOT NULL,
    event_type text NOT NULL,
    provider_created_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    workspace_id uuid,
    state_changed boolean NOT NULL DEFAULT false,
    PRIMARY KEY (provider, event_id)
);

CREATE TABLE f10_usage_counters (
    workspace_id uuid NOT NULL,
    resource text NOT NULL CHECK (
        resource IN ('members', 'channels', 'scheduled_publications')
    ),
    window_start timestamptz NOT NULL,
    used bigint NOT NULL CHECK (used >= 0),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, resource, window_start)
);

CREATE TABLE f10_usage_operations (
    workspace_id uuid NOT NULL,
    idempotency_key text NOT NULL
        CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    resource text NOT NULL CHECK (
        resource IN ('members', 'channels', 'scheduled_publications')
    ),
    delta bigint NOT NULL CHECK (delta <> 0),
    window_start timestamptz NOT NULL,
    accepted boolean NOT NULL,
    decision_code text NOT NULL,
    retryable boolean NOT NULL,
    used_after bigint NOT NULL CHECK (used_after >= 0),
    quota_limit bigint,
    remaining bigint,
    over_limit boolean NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, idempotency_key)
);

CREATE INDEX f10_usage_operations_window_idx
    ON f10_usage_operations (workspace_id, resource, window_start);

-- This table is intentionally absent from public views and contracts. It has
-- no client-callable assignment function; the separate F11 administration
-- slice owns allowlisting, strong authentication, and audit.
CREATE TABLE f10_internal_entitlement_overrides (
    workspace_id uuid PRIMARY KEY,
    active boolean NOT NULL,
    assigned_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CHECK (
        (active AND revoked_at IS NULL)
        OR (NOT active AND revoked_at IS NOT NULL)
    )
);

COMMENT ON TABLE f10_internal_entitlement_overrides IS
    'Server-only enforcement override. Never expose through client contracts.';

CREATE FUNCTION f10_monthly_quota_window(
    p_anchor timestamptz,
    p_moment timestamptz
) RETURNS TABLE (window_start timestamptz, window_end timestamptz)
LANGUAGE plpgsql
IMMUTABLE
AS $function$
DECLARE
    v_anchor_utc timestamp := p_anchor AT TIME ZONE 'UTC';
    v_moment_utc timestamp := p_moment AT TIME ZONE 'UTC';
    v_month timestamp;
    v_last_day integer;
    v_day integer;
    v_candidate timestamp;
    v_next_month timestamp;
    v_next_last_day integer;
    v_next_day integer;
BEGIN
    v_month := date_trunc('month', v_moment_utc);
    v_last_day := extract(
        day FROM (v_month + interval '1 month - 1 day')
    )::integer;
    v_day := least(extract(day FROM v_anchor_utc)::integer, v_last_day);
    v_candidate := v_month
        + (v_day - 1) * interval '1 day'
        + (v_anchor_utc - date_trunc('day', v_anchor_utc));

    IF v_candidate > v_moment_utc THEN
        v_month := v_month - interval '1 month';
        v_last_day := extract(
            day FROM (v_month + interval '1 month - 1 day')
        )::integer;
        v_day := least(extract(day FROM v_anchor_utc)::integer, v_last_day);
        v_candidate := v_month
            + (v_day - 1) * interval '1 day'
            + (v_anchor_utc - date_trunc('day', v_anchor_utc));
    END IF;

    v_next_month := date_trunc('month', v_candidate) + interval '1 month';
    v_next_last_day := extract(
        day FROM (v_next_month + interval '1 month - 1 day')
    )::integer;
    v_next_day := least(
        extract(day FROM v_anchor_utc)::integer,
        v_next_last_day
    );

    window_start := v_candidate AT TIME ZONE 'UTC';
    window_end := (
        v_next_month
        + (v_next_day - 1) * interval '1 day'
        + (v_anchor_utc - date_trunc('day', v_anchor_utc))
    ) AT TIME ZONE 'UTC';
    RETURN NEXT;
END;
$function$;

CREATE FUNCTION f10_provision_trial(
    p_workspace_id uuid,
    p_started_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql
AS $function$
BEGIN
    INSERT INTO f10_workspace_billing (
        workspace_id,
        plan_code,
        billing_interval,
        billing_state,
        provider_period_start,
        provider_period_end,
        quota_anchor,
        trial_started_at,
        trial_ends_at,
        created_at,
        updated_at
    ) VALUES (
        p_workspace_id,
        'pro',
        'monthly',
        'trialing',
        p_started_at,
        p_started_at + interval '14 days',
        p_started_at,
        p_started_at,
        p_started_at + interval '14 days',
        p_started_at,
        p_started_at
    )
    ON CONFLICT (workspace_id) DO NOTHING;
    RETURN FOUND;
END;
$function$;

CREATE FUNCTION f10_register_checkout(
    p_session_id text,
    p_workspace_id uuid,
    p_plan_code text,
    p_billing_interval text,
    p_expires_at timestamptz,
    p_created_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql
AS $function$
DECLARE
    v_registration f10_checkout_sessions%ROWTYPE;
BEGIN
    INSERT INTO f10_checkout_sessions (
        session_id,
        workspace_id,
        plan_code,
        billing_interval,
        expires_at,
        created_at
    ) VALUES (
        p_session_id,
        p_workspace_id,
        p_plan_code,
        p_billing_interval,
        p_expires_at,
        p_created_at
    )
    ON CONFLICT (session_id) DO NOTHING;

    SELECT *
      INTO v_registration
      FROM f10_checkout_sessions
     WHERE session_id = p_session_id;

    RETURN v_registration.workspace_id = p_workspace_id
       AND v_registration.plan_code = p_plan_code
       AND v_registration.billing_interval = p_billing_interval
       AND v_registration.expires_at = p_expires_at;
END;
$function$;

CREATE FUNCTION f10_complete_checkout(
    p_event_id text,
    p_event_created_at timestamptz,
    p_session_id text,
    p_customer_id text,
    p_subscription_id text
) RETURNS boolean
LANGUAGE plpgsql
AS $function$
DECLARE
    v_session f10_checkout_sessions%ROWTYPE;
BEGIN
    SELECT *
      INTO v_session
      FROM f10_checkout_sessions
     WHERE session_id = p_session_id
     FOR UPDATE;
    IF NOT FOUND OR p_event_created_at > v_session.expires_at THEN
        RAISE EXCEPTION 'unknown or expired checkout session';
    END IF;

    INSERT INTO f10_provider_events (
        provider,
        event_id,
        event_type,
        provider_created_at,
        workspace_id
    ) VALUES (
        'stripe',
        p_event_id,
        'checkout.session.completed',
        p_event_created_at,
        v_session.workspace_id
    )
    ON CONFLICT (provider, event_id) DO NOTHING;
    IF NOT FOUND THEN
        RETURN false;
    END IF;

    IF v_session.completed_at IS NOT NULL AND (
        v_session.customer_id <> p_customer_id
        OR v_session.subscription_id <> p_subscription_id
    ) THEN
        RAISE EXCEPTION 'checkout completion conflicts with recorded binding';
    END IF;

    UPDATE f10_checkout_sessions
       SET customer_id = p_customer_id,
           subscription_id = p_subscription_id,
           completed_at = p_event_created_at
     WHERE session_id = p_session_id
       AND completed_at IS NULL;
    RETURN true;
END;
$function$;

CREATE FUNCTION f10_apply_billing_event(
    p_event_id text,
    p_event_type text,
    p_event_created_at timestamptz,
    p_workspace_id uuid,
    p_plan_code text,
    p_billing_interval text,
    p_billing_state text,
    p_customer_id text,
    p_subscription_id text,
    p_period_start timestamptz,
    p_period_end timestamptz
) RETURNS TABLE (first_delivery boolean, state_changed boolean)
LANGUAGE plpgsql
AS $function$
DECLARE
    v_current f10_workspace_billing%ROWTYPE;
    v_changed boolean := false;
    v_is_latest boolean := false;
BEGIN
    INSERT INTO f10_provider_events (
        provider,
        event_id,
        event_type,
        provider_created_at,
        workspace_id
    ) VALUES (
        'stripe',
        p_event_id,
        p_event_type,
        p_event_created_at,
        p_workspace_id
    )
    ON CONFLICT (provider, event_id) DO NOTHING;
    IF NOT FOUND THEN
        RETURN QUERY SELECT false, false;
        RETURN;
    END IF;

    SELECT *
      INTO v_current
      FROM f10_workspace_billing
     WHERE workspace_id = p_workspace_id
     FOR UPDATE;

    IF NOT FOUND THEN
        INSERT INTO f10_workspace_billing (
            workspace_id,
            plan_code,
            billing_interval,
            billing_state,
            stripe_customer_id,
            stripe_subscription_id,
            provider_period_start,
            provider_period_end,
            quota_anchor,
            last_provider_event_created_at,
            last_provider_event_id,
            created_at,
            updated_at
        ) VALUES (
            p_workspace_id,
            p_plan_code,
            p_billing_interval,
            p_billing_state,
            p_customer_id,
            p_subscription_id,
            p_period_start,
            p_period_end,
            p_period_start,
            p_event_created_at,
            p_event_id,
            p_event_created_at,
            p_event_created_at
        );
        v_changed := true;
        v_is_latest := true;
    ELSIF v_current.last_provider_event_created_at IS NULL
       OR (p_event_created_at, p_event_id)
          > (v_current.last_provider_event_created_at,
             v_current.last_provider_event_id) THEN
        v_changed := v_current.plan_code <> p_plan_code
            OR v_current.billing_interval <> p_billing_interval
            OR v_current.billing_state <> p_billing_state
            OR v_current.provider_period_start <> p_period_start
            OR v_current.provider_period_end <> p_period_end;

        UPDATE f10_workspace_billing
           SET plan_code = p_plan_code,
               billing_interval = p_billing_interval,
               billing_state = p_billing_state,
               stripe_customer_id = p_customer_id,
               stripe_subscription_id = p_subscription_id,
               provider_period_start = p_period_start,
               provider_period_end = p_period_end,
               quota_anchor = CASE
                   WHEN v_current.stripe_subscription_id IS NULL
                   THEN p_period_start
                   ELSE v_current.quota_anchor
               END,
               last_provider_event_created_at = p_event_created_at,
               last_provider_event_id = p_event_id,
               updated_at = p_event_created_at
         WHERE workspace_id = p_workspace_id;
        v_is_latest := true;
    END IF;

    UPDATE f10_checkout_sessions
       SET plan_code = p_plan_code,
           billing_interval = p_billing_interval
     WHERE subscription_id = p_subscription_id
       AND v_is_latest;

    UPDATE f10_provider_events
       SET state_changed = v_changed
     WHERE provider = 'stripe'
       AND event_id = p_event_id;

    RETURN QUERY SELECT true, v_changed;
END;
$function$;

CREATE FUNCTION f10_apply_usage(
    p_workspace_id uuid,
    p_resource text,
    p_delta bigint,
    p_idempotency_key text,
    p_occurred_at timestamptz
) RETURNS TABLE (
    accepted boolean,
    decision_code text,
    retryable boolean,
    used bigint,
    quota_limit bigint,
    remaining bigint,
    over_limit boolean
)
LANGUAGE plpgsql
AS $function$
DECLARE
    v_billing f10_workspace_billing%ROWTYPE;
    v_operation f10_usage_operations%ROWTYPE;
    v_window_start timestamptz;
    v_window_end timestamptz;
    v_used bigint := 0;
    v_limit bigint;
    v_internal boolean := false;
    v_allowed boolean;
    v_code text;
    v_retryable boolean := false;
    v_new_used bigint;
    v_remaining bigint;
    v_over_limit boolean;
BEGIN
    IF p_resource NOT IN (
        'members',
        'channels',
        'scheduled_publications'
    ) OR p_delta = 0
      OR length(p_idempotency_key) NOT BETWEEN 1 AND 255 THEN
        RAISE EXCEPTION 'invalid quota command';
    END IF;

    SELECT *
      INTO v_billing
      FROM f10_workspace_billing
     WHERE workspace_id = p_workspace_id
     FOR UPDATE;
    IF NOT FOUND THEN
        RETURN QUERY SELECT
            false,
            'entitlement_unavailable',
            true,
            0::bigint,
            NULL::bigint,
            NULL::bigint,
            false;
        RETURN;
    END IF;

    IF v_billing.billing_state = 'trialing'
       AND p_occurred_at >= v_billing.trial_ends_at THEN
        UPDATE f10_workspace_billing
           SET billing_state = 'trial_expired',
               updated_at = p_occurred_at
         WHERE workspace_id = p_workspace_id;
        v_billing.billing_state := 'trial_expired';
    END IF;

    SELECT active
      INTO v_internal
      FROM f10_internal_entitlement_overrides
     WHERE workspace_id = p_workspace_id;
    v_internal := coalesce(v_internal, false);

    SELECT CASE p_resource
               WHEN 'members' THEN member_limit
               WHEN 'channels' THEN channel_limit
               WHEN 'scheduled_publications'
                   THEN scheduled_publication_limit
           END
      INTO v_limit
      FROM f10_public_plans
     WHERE code = v_billing.plan_code;

    IF p_resource = 'scheduled_publications' THEN
        SELECT quota.window_start, quota.window_end
          INTO v_window_start, v_window_end
          FROM f10_monthly_quota_window(
              v_billing.quota_anchor,
              p_occurred_at
          ) AS quota;
    ELSE
        v_window_start := '1970-01-01 00:00:00+00'::timestamptz;
        v_window_end := 'infinity'::timestamptz;
    END IF;

    SELECT *
      INTO v_operation
      FROM f10_usage_operations
     WHERE workspace_id = p_workspace_id
       AND idempotency_key = p_idempotency_key;
    IF FOUND THEN
        IF v_operation.resource <> p_resource
           OR v_operation.delta <> p_delta THEN
            RAISE EXCEPTION 'idempotency key reused with different quota command';
        END IF;
        RETURN QUERY SELECT
            v_operation.accepted,
            v_operation.decision_code,
            v_operation.retryable,
            v_operation.used_after,
            v_operation.quota_limit,
            v_operation.remaining,
            v_operation.over_limit;
        RETURN;
    END IF;

    SELECT counters.used
      INTO v_used
      FROM f10_usage_counters AS counters
     WHERE counters.workspace_id = p_workspace_id
       AND counters.resource = p_resource
       AND counters.window_start = v_window_start;
    v_used := coalesce(v_used, 0);

    IF p_delta < 0 THEN
        v_allowed := true;
        v_code := 'released';
    ELSIF v_internal THEN
        v_allowed := true;
        v_code := 'accepted';
        v_limit := NULL;
    ELSIF v_billing.billing_state NOT IN ('trialing', 'active', 'past_due') THEN
        v_allowed := false;
        v_code := CASE v_billing.billing_state
            WHEN 'trial_expired' THEN 'trial_expired'
            WHEN 'payment_restricted' THEN 'payment_restricted'
            WHEN 'canceled' THEN 'subscription_required'
            ELSE 'entitlement_unavailable'
        END;
        v_retryable := v_code = 'entitlement_unavailable';
    ELSIF v_used + p_delta > v_limit THEN
        v_allowed := false;
        v_code := 'limit_reached';
    ELSE
        v_allowed := true;
        v_code := 'accepted';
    END IF;

    IF v_allowed THEN
        v_new_used := greatest(0, v_used + p_delta);
        INSERT INTO f10_usage_counters (
            workspace_id,
            resource,
            window_start,
            used,
            updated_at
        ) VALUES (
            p_workspace_id,
            p_resource,
            v_window_start,
            v_new_used,
            p_occurred_at
        )
        ON CONFLICT (workspace_id, resource, window_start) DO UPDATE
            SET used = EXCLUDED.used,
                updated_at = EXCLUDED.updated_at;
    ELSE
        v_new_used := v_used;
    END IF;

    v_remaining := CASE
        WHEN v_limit IS NULL THEN NULL
        ELSE greatest(v_limit - v_new_used, 0)
    END;
    v_over_limit := v_limit IS NOT NULL AND v_new_used > v_limit;

    INSERT INTO f10_usage_operations (
        workspace_id,
        idempotency_key,
        resource,
        delta,
        window_start,
        accepted,
        decision_code,
        retryable,
        used_after,
        quota_limit,
        remaining,
        over_limit,
        occurred_at
    ) VALUES (
        p_workspace_id,
        p_idempotency_key,
        p_resource,
        p_delta,
        v_window_start,
        v_allowed,
        v_code,
        v_retryable,
        v_new_used,
        v_limit,
        v_remaining,
        v_over_limit,
        p_occurred_at
    );

    RETURN QUERY SELECT
        v_allowed,
        v_code,
        v_retryable,
        v_new_used,
        v_limit,
        v_remaining,
        v_over_limit;
END;
$function$;

CREATE VIEW f10_public_entitlement_usage AS
WITH resources(resource) AS (
    VALUES
        ('members'::text),
        ('channels'::text),
        ('scheduled_publications'::text)
),
billing_windows AS (
    SELECT billing.*,
           quota.window_start AS quota_window_start,
           CASE
               WHEN billing.billing_state IN ('trialing', 'trial_expired')
               THEN least(quota.window_end, billing.trial_ends_at)
               ELSE quota.window_end
           END AS quota_window_end
      FROM f10_workspace_billing AS billing
      CROSS JOIN LATERAL f10_monthly_quota_window(
          billing.quota_anchor,
          now()
      ) AS quota
)
SELECT billing.workspace_id,
       plans.code AS plan_code,
       plans.display_name AS plan_name,
       plans.currency,
       plans.monthly_price_cents,
       plans.annual_price_cents,
       billing.billing_interval,
       CASE
           WHEN billing.billing_state = 'trialing'
            AND now() >= billing.trial_ends_at
           THEN 'trial_expired'
           ELSE billing.billing_state
       END AS billing_state,
       billing.quota_window_start AS period_start,
       billing.quota_window_end AS period_end,
       resources.resource,
       coalesce(counters.used, 0) AS used,
       CASE resources.resource
           WHEN 'members' THEN plans.member_limit
           WHEN 'channels' THEN plans.channel_limit
           WHEN 'scheduled_publications'
               THEN plans.scheduled_publication_limit
       END AS quota_limit,
       greatest(
           CASE resources.resource
               WHEN 'members' THEN plans.member_limit
               WHEN 'channels' THEN plans.channel_limit
               WHEN 'scheduled_publications'
                   THEN plans.scheduled_publication_limit
           END - coalesce(counters.used, 0),
           0
       ) AS remaining,
       coalesce(counters.used, 0) > CASE resources.resource
           WHEN 'members' THEN plans.member_limit
           WHEN 'channels' THEN plans.channel_limit
           WHEN 'scheduled_publications'
               THEN plans.scheduled_publication_limit
       END AS over_limit
  FROM billing_windows AS billing
  JOIN f10_public_plans AS plans ON plans.code = billing.plan_code
 CROSS JOIN resources
  LEFT JOIN f10_usage_counters AS counters
    ON counters.workspace_id = billing.workspace_id
   AND counters.resource = resources.resource
   AND counters.window_start = CASE resources.resource
       WHEN 'scheduled_publications' THEN billing.quota_window_start
       ELSE '1970-01-01 00:00:00+00'::timestamptz
   END;
