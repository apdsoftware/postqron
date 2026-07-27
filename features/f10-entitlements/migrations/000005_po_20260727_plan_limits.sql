-- Forward-only, retry-safe application of the Product Owner decision dated
-- 2026-07-27. Names and prices remain unchanged. The public entitlement
-- contract advances to d09-v2 while the Paddle product/price catalog stays on
-- d09-v1.

UPDATE f10_public_plans
   SET member_limit = CASE code
           WHEN 'start' THEN 1
           WHEN 'pro' THEN 3
           WHEN 'team' THEN 6
           WHEN 'unlimited' THEN NULL
       END,
       channel_limit = CASE code
           WHEN 'start' THEN 3
           WHEN 'pro' THEN 6
           WHEN 'team' THEN 9
           WHEN 'unlimited' THEN NULL
       END,
       scheduled_publication_limit = CASE code
           WHEN 'start' THEN 10
           WHEN 'pro' THEN 250
           WHEN 'team' THEN 500
           WHEN 'unlimited' THEN NULL
       END
 WHERE code IN ('start', 'pro', 'team', 'unlimited');

ALTER TABLE f10_public_plans
    DROP CONSTRAINT IF EXISTS f10_public_plans_d09_v2_limits_check;
ALTER TABLE f10_public_plans
    ADD CONSTRAINT f10_public_plans_d09_v2_limits_check
    CHECK (
        (
            code = 'start'
            AND member_limit = 1
            AND channel_limit = 3
            AND scheduled_publication_limit = 10
        )
        OR (
            code = 'pro'
            AND member_limit = 3
            AND channel_limit = 6
            AND scheduled_publication_limit = 250
        )
        OR (
            code = 'team'
            AND member_limit = 6
            AND channel_limit = 9
            AND scheduled_publication_limit = 500
        )
        OR (
            code = 'unlimited'
            AND member_limit IS NULL
            AND channel_limit IS NULL
            AND scheduled_publication_limit IS NULL
        )
    );

CREATE OR REPLACE FUNCTION f10_register_checkout(
    p_transaction_id text,
    p_workspace_id uuid,
    p_plan_code text,
    p_billing_interval text,
    p_channel_quantity bigint,
    p_catalog_version text,
    p_expected_items jsonb,
    p_expires_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql
AS $function$
DECLARE
    v_registration f10_checkout_sessions%ROWTYPE;
BEGIN
    IF p_transaction_id NOT LIKE 'txn_%'
       OR p_plan_code NOT IN ('pro', 'team', 'unlimited')
       OR p_billing_interval NOT IN ('monthly', 'annual')
       OR NOT (
           (p_plan_code = 'pro' AND p_channel_quantity BETWEEN 1 AND 6)
           OR (p_plan_code = 'team' AND p_channel_quantity BETWEEN 1 AND 9)
           OR (p_plan_code = 'unlimited' AND p_channel_quantity IS NULL)
       )
       OR p_catalog_version <> 'd09-v2'
       OR jsonb_typeof(p_expected_items) <> 'array'
       OR (
           p_plan_code = 'unlimited'
           AND jsonb_array_length(p_expected_items) <> 1
       )
       OR (
           p_plan_code IN ('pro', 'team')
           AND jsonb_array_length(p_expected_items) NOT BETWEEN 1 AND 3
       ) THEN
        RAISE EXCEPTION 'invalid Paddle transaction registration';
    END IF;

    INSERT INTO f10_checkout_sessions (
        session_id,
        workspace_id,
        plan_code,
        billing_interval,
        channel_quantity,
        catalog_version,
        expected_items,
        expires_at,
        created_at
    ) VALUES (
        p_transaction_id,
        p_workspace_id,
        p_plan_code,
        p_billing_interval,
        p_channel_quantity,
        p_catalog_version,
        p_expected_items,
        p_expires_at,
        now()
    )
    ON CONFLICT (session_id) DO NOTHING;

    SELECT *
      INTO v_registration
      FROM f10_checkout_sessions
     WHERE session_id = p_transaction_id;

    RETURN v_registration.workspace_id = p_workspace_id
       AND v_registration.plan_code = p_plan_code
       AND v_registration.billing_interval = p_billing_interval
       AND v_registration.channel_quantity IS NOT DISTINCT FROM
           p_channel_quantity
       AND v_registration.catalog_version = p_catalog_version
       AND v_registration.expected_items = p_expected_items
       AND v_registration.expires_at = p_expires_at;
END;
$function$;

CREATE OR REPLACE FUNCTION f10_apply_usage(
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
    v_window_start timestamptz :=
        '1970-01-01 00:00:00+00'::timestamptz;
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
        'members', 'channels', 'scheduled_publications'
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
            false, 'entitlement_unavailable', true, 0::bigint,
            NULL::bigint, NULL::bigint, false;
        RETURN;
    END IF;

    IF v_billing.billing_state = 'trialing'
       AND v_billing.trial_ends_at <= p_occurred_at THEN
        UPDATE f10_workspace_billing
           SET plan_code = 'start',
               billing_interval = 'monthly',
               billing_state = 'trial_expired',
               channel_quantity = 3,
               updated_at = p_occurred_at
         WHERE workspace_id = p_workspace_id;
        v_billing.plan_code := 'start';
        v_billing.billing_interval := 'monthly';
        v_billing.billing_state := 'trial_expired';
        v_billing.channel_quantity := 3;
    END IF;

    SELECT *
      INTO v_operation
      FROM f10_usage_operations
     WHERE workspace_id = p_workspace_id
       AND idempotency_key = p_idempotency_key;
    IF FOUND THEN
        IF v_operation.resource <> p_resource
           OR v_operation.delta <> p_delta THEN
            RAISE EXCEPTION
                'idempotency key reused with different quota command';
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

    SELECT active
      INTO v_internal
      FROM f10_internal_entitlement_overrides
     WHERE workspace_id = p_workspace_id;
    v_internal := coalesce(v_internal, false);

    IF v_billing.billing_state = 'trialing' THEN
        SELECT CASE p_resource
                   WHEN 'members' THEN member_limit
                   WHEN 'channels' THEN channel_limit
                   WHEN 'scheduled_publications'
                       THEN scheduled_publication_limit
               END
          INTO v_limit
          FROM f10_public_plans
         WHERE code = 'team';
        IF NOT FOUND OR v_limit IS NULL THEN
            RAISE EXCEPTION 'public entitlement catalog unavailable';
        END IF;
    ELSIF p_resource = 'channels' THEN
        v_limit := v_billing.channel_quantity;
        IF v_billing.plan_code <> 'unlimited' AND v_limit IS NULL THEN
            RAISE EXCEPTION 'public entitlement catalog unavailable';
        END IF;
    ELSE
        SELECT CASE p_resource
                   WHEN 'members' THEN member_limit
                   WHEN 'scheduled_publications'
                       THEN scheduled_publication_limit
               END
          INTO v_limit
          FROM f10_public_plans
         WHERE code = v_billing.plan_code;
        IF NOT FOUND OR (
            v_billing.plan_code <> 'unlimited' AND v_limit IS NULL
        ) THEN
            RAISE EXCEPTION 'public entitlement catalog unavailable';
        END IF;
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
    ELSIF v_billing.billing_state NOT IN (
        'trialing', 'active', 'past_due'
    ) THEN
        v_allowed := false;
        v_code := CASE v_billing.billing_state
            WHEN 'trial_expired' THEN 'trial_expired'
            WHEN 'payment_restricted' THEN 'payment_restricted'
            WHEN 'canceled' THEN 'subscription_required'
            ELSE 'entitlement_unavailable'
        END;
        v_retryable := v_code = 'entitlement_unavailable';
    ELSIF v_limit IS NULL THEN
        v_allowed := true;
        v_code := 'accepted';
    ELSIF p_delta > v_limit - v_used THEN
        v_allowed := false;
        v_code := 'limit_reached';
    ELSE
        v_allowed := true;
        v_code := 'accepted';
    END IF;

    IF v_allowed THEN
        v_new_used := greatest(0, v_used + p_delta);
        INSERT INTO f10_usage_counters (
            workspace_id, resource, window_start, used, updated_at
        ) VALUES (
            p_workspace_id, p_resource, v_window_start,
            v_new_used, p_occurred_at
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
        workspace_id, idempotency_key, resource, delta, window_start,
        accepted, decision_code, retryable, used_after, quota_limit,
        remaining, over_limit, occurred_at
    ) VALUES (
        p_workspace_id, p_idempotency_key, p_resource, p_delta,
        v_window_start, v_allowed, v_code, v_retryable, v_new_used,
        v_limit, v_remaining, v_over_limit, p_occurred_at
    );

    RETURN QUERY SELECT
        v_allowed, v_code, v_retryable, v_new_used, v_limit,
        v_remaining, v_over_limit;
END;
$function$;

CREATE OR REPLACE VIEW f10_public_entitlement_usage AS
WITH resources(resource) AS (
    VALUES
        ('members'::text),
        ('channels'::text),
        ('scheduled_publications'::text)
),
billing_windows AS (
    SELECT billing.*,
           CASE
               WHEN billing.billing_state = 'trialing'
                AND now() >= billing.trial_ends_at
               THEN 'start'
               ELSE billing.plan_code
           END AS effective_plan_code,
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
),
usage_with_limits AS (
    SELECT billing.*,
           plans.code AS public_plan_code,
           plans.display_name AS plan_name,
           plans.currency,
           plans.monthly_price_cents,
           plans.annual_price_cents,
           resources.resource,
           coalesce(counters.used, 0) AS used,
           CASE resources.resource
               WHEN 'members' THEN plans.member_limit
               WHEN 'channels' THEN CASE
                   WHEN billing.effective_plan_code <> billing.plan_code
                   THEN plans.channel_limit
                   ELSE billing.channel_quantity
               END
               WHEN 'scheduled_publications'
                   THEN plans.scheduled_publication_limit
           END AS quota_limit
      FROM billing_windows AS billing
      JOIN f10_public_plans AS plans
        ON plans.code = billing.effective_plan_code
     CROSS JOIN resources
      LEFT JOIN f10_usage_counters AS counters
        ON counters.workspace_id = billing.workspace_id
       AND counters.resource = resources.resource
       AND counters.window_start =
           '1970-01-01 00:00:00+00'::timestamptz
)
SELECT workspace_id,
       public_plan_code AS plan_code,
       plan_name,
       currency,
       monthly_price_cents,
       annual_price_cents,
       billing_interval,
       CASE
           WHEN billing_state = 'trialing'
            AND now() >= trial_ends_at
           THEN 'trial_expired'
           ELSE billing_state
       END AS billing_state,
       quota_window_start AS period_start,
       quota_window_end AS period_end,
       resource,
       used,
       quota_limit,
       CASE
           WHEN quota_limit IS NULL THEN NULL
           ELSE greatest(quota_limit - used, 0)
       END AS remaining,
       coalesce(used > quota_limit, false) AS over_limit
  FROM usage_with_limits;
