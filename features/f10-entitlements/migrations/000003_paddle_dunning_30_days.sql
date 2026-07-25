DO $migration$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM pg_attribute
         WHERE attrelid = 'f10_workspace_billing'::regclass
           AND attname = 'grace_ends_at'
           AND NOT attisdropped
    ) AND NOT EXISTS (
        SELECT 1
          FROM pg_attribute
         WHERE attrelid = 'f10_workspace_billing'::regclass
           AND attname = 'dunning_ends_at'
           AND NOT attisdropped
    ) THEN
        ALTER TABLE f10_workspace_billing
            RENAME COLUMN grace_ends_at TO dunning_ends_at;
    END IF;
END;
$migration$;

UPDATE f10_workspace_billing
   SET dunning_ends_at = first_payment_failed_at + interval '30 days'
 WHERE first_payment_failed_at IS NOT NULL
   AND dunning_ends_at IS DISTINCT FROM
       first_payment_failed_at + interval '30 days';

COMMENT ON COLUMN f10_workspace_billing.first_payment_failed_at IS
    'First failed renewal; later Paddle retries never move this anchor.';
COMMENT ON COLUMN f10_workspace_billing.dunning_ends_at IS
    'Thirty days after the first failed renewal; informational only. '
    'Paddle paused/canceled events drive suspension and termination.';

DROP FUNCTION IF EXISTS f10_restrict_expired_grace(timestamptz);

CREATE OR REPLACE FUNCTION f10_apply_billing_event(
    p_event_id text,
    p_event_type text,
    p_occurred_at timestamptz,
    p_workspace_id uuid,
    p_plan_code text,
    p_billing_interval text,
    p_channel_quantity bigint,
    p_billing_state text,
    p_customer_id text,
    p_subscription_id text,
    p_transaction_id text,
    p_period_start timestamptz,
    p_period_end timestamptz,
    p_apply_state boolean
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
        'paddle',
        p_event_id,
        p_event_type,
        p_occurred_at,
        p_workspace_id
    )
    ON CONFLICT (provider, event_id) DO NOTHING;
    IF NOT FOUND THEN
        RETURN QUERY SELECT false, false;
        RETURN;
    END IF;

    IF NOT p_apply_state THEN
        RETURN QUERY SELECT true, false;
        RETURN;
    END IF;

    IF p_plan_code NOT IN ('start', 'pro', 'team')
       OR p_billing_interval NOT IN ('monthly', 'annual')
       OR p_channel_quantity NOT BETWEEN 1 AND 50
       OR p_billing_state NOT IN (
           'trialing', 'active', 'past_due', 'trial_expired',
           'payment_restricted', 'canceled'
       )
       OR p_period_end <= p_period_start THEN
        RAISE EXCEPTION 'invalid Paddle billing event';
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
            channel_quantity,
            paddle_customer_id,
            paddle_subscription_id,
            provider_period_start,
            provider_period_end,
            quota_anchor,
            first_payment_failed_at,
            dunning_ends_at,
            last_provider_event_created_at,
            last_provider_event_id,
            created_at,
            updated_at
        ) VALUES (
            p_workspace_id,
            p_plan_code,
            p_billing_interval,
            p_billing_state,
            p_channel_quantity,
            nullif(p_customer_id, ''),
            nullif(p_subscription_id, ''),
            p_period_start,
            p_period_end,
            p_period_start,
            CASE WHEN p_billing_state = 'past_due' THEN p_occurred_at END,
            CASE WHEN p_billing_state = 'past_due'
                 THEN p_occurred_at + interval '30 days' END,
            p_occurred_at,
            p_event_id,
            p_occurred_at,
            p_occurred_at
        );
        v_changed := true;
        v_is_latest := true;
    ELSIF v_current.last_provider_event_created_at IS NULL
       OR (p_occurred_at, p_event_id)
          > (v_current.last_provider_event_created_at,
             v_current.last_provider_event_id) THEN
        v_changed := v_current.plan_code <> p_plan_code
            OR v_current.billing_interval <> p_billing_interval
            OR v_current.channel_quantity <> p_channel_quantity
            OR v_current.billing_state <> p_billing_state
            OR v_current.provider_period_start <> p_period_start
            OR v_current.provider_period_end <> p_period_end;

        UPDATE f10_workspace_billing
           SET plan_code = p_plan_code,
               billing_interval = p_billing_interval,
               billing_state = p_billing_state,
               channel_quantity = p_channel_quantity,
               paddle_customer_id = coalesce(
                   nullif(p_customer_id, ''),
                   v_current.paddle_customer_id
               ),
               paddle_subscription_id = coalesce(
                   nullif(p_subscription_id, ''),
                   v_current.paddle_subscription_id
               ),
               provider_period_start = p_period_start,
               provider_period_end = p_period_end,
               quota_anchor = CASE
                   WHEN v_current.paddle_subscription_id IS NULL
                   THEN p_period_start
                   ELSE v_current.quota_anchor
               END,
               first_payment_failed_at = CASE
                   WHEN p_billing_state = 'past_due'
                   THEN coalesce(v_current.first_payment_failed_at, p_occurred_at)
                   WHEN p_billing_state = 'active' THEN NULL
                   ELSE v_current.first_payment_failed_at
               END,
               dunning_ends_at = CASE
                   WHEN p_billing_state = 'past_due'
                   THEN coalesce(
                       v_current.dunning_ends_at,
                       p_occurred_at + interval '30 days'
                   )
                   WHEN p_billing_state = 'active' THEN NULL
                   ELSE v_current.dunning_ends_at
               END,
               last_provider_event_created_at = p_occurred_at,
               last_provider_event_id = p_event_id,
               updated_at = p_occurred_at
         WHERE workspace_id = p_workspace_id;
        v_is_latest := true;
    END IF;

    IF p_transaction_id <> '' AND v_is_latest THEN
        UPDATE f10_checkout_sessions
           SET customer_id = nullif(p_customer_id, ''),
               subscription_id = nullif(p_subscription_id, ''),
               completed_at = p_occurred_at
         WHERE session_id = p_transaction_id
           AND workspace_id = p_workspace_id
           AND completed_at IS NULL;
    END IF;

    UPDATE f10_checkout_sessions
       SET plan_code = p_plan_code,
           billing_interval = p_billing_interval,
           channel_quantity = p_channel_quantity
     WHERE subscription_id = p_subscription_id
       AND v_is_latest;

    UPDATE f10_provider_events
       SET state_changed = v_changed
     WHERE provider = 'paddle'
       AND event_id = p_event_id;

    RETURN QUERY SELECT true, v_changed;
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

    IF p_resource = 'channels' THEN
        v_limit := v_billing.channel_quantity;
    ELSE
        SELECT CASE p_resource
                   WHEN 'members' THEN member_limit
                   WHEN 'scheduled_publications'
                       THEN scheduled_publication_limit
               END
          INTO v_limit
          FROM f10_public_plans
         WHERE code = CASE
             WHEN v_billing.billing_state = 'trialing' THEN 'team'
             ELSE v_billing.plan_code
         END;
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
           WHEN 'members' THEN CASE
               WHEN billing.billing_state = 'trialing' THEN 15
               ELSE plans.member_limit
           END
           WHEN 'channels' THEN billing.channel_quantity
           WHEN 'scheduled_publications'
               THEN plans.scheduled_publication_limit
       END AS quota_limit,
       greatest(
           CASE resources.resource
               WHEN 'members' THEN CASE
                   WHEN billing.billing_state = 'trialing' THEN 15
                   ELSE plans.member_limit
               END
               WHEN 'channels' THEN billing.channel_quantity
               WHEN 'scheduled_publications'
                   THEN plans.scheduled_publication_limit
           END - coalesce(counters.used, 0),
           0
       ) AS remaining,
       coalesce(counters.used, 0) > CASE resources.resource
           WHEN 'members' THEN CASE
               WHEN billing.billing_state = 'trialing' THEN 15
               ELSE plans.member_limit
           END
           WHEN 'channels' THEN billing.channel_quantity
           WHEN 'scheduled_publications'
               THEN plans.scheduled_publication_limit
       END AS over_limit
  FROM billing_windows AS billing
  JOIN f10_public_plans AS plans ON plans.code = billing.effective_plan_code
 CROSS JOIN resources
  LEFT JOIN f10_usage_counters AS counters
    ON counters.workspace_id = billing.workspace_id
   AND counters.resource = resources.resource
   AND counters.window_start = '1970-01-01 00:00:00+00'::timestamptz;
