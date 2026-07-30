-- Owner-requested plan changes are durable and fail closed. A requested
-- target never changes entitlements directly: only the existing verified
-- Paddle webhook path may mark it applied.

CREATE TABLE IF NOT EXISTS f10_subscription_changes (
    workspace_id text NOT NULL,
    idempotency_key text NOT NULL
        CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    source_plan_code text NOT NULL
        CHECK (source_plan_code IN ('start', 'pro', 'team', 'unlimited')),
    source_billing_interval text NOT NULL
        CHECK (source_billing_interval IN ('monthly', 'annual')),
    source_channel_quantity bigint,
    target_plan_code text NOT NULL
        CHECK (target_plan_code IN ('start', 'pro', 'team', 'unlimited')),
    target_billing_interval text NOT NULL
        CHECK (target_billing_interval IN ('monthly', 'annual')),
    target_channel_quantity bigint,
    direction text NOT NULL CHECK (direction IN ('upgrade', 'downgrade')),
    action text NOT NULL
        CHECK (action IN ('update_subscription', 'cancel_subscription')),
    expected_items jsonb NOT NULL CHECK (jsonb_typeof(expected_items) = 'array'),
    catalog_version text NOT NULL,
    provider_subscription_id text NOT NULL,
    status text NOT NULL CHECK (
        status IN ('dispatching', 'pending', 'applied', 'provider_failed')
    ),
    applied_event_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, idempotency_key),
    CHECK (
        (
            target_plan_code = 'start'
            AND target_billing_interval = 'monthly'
            AND target_channel_quantity = 3
            AND action = 'cancel_subscription'
            AND jsonb_array_length(expected_items) = 0
        )
        OR (
            target_plan_code IN ('pro', 'team', 'unlimited')
            AND action = 'update_subscription'
            AND jsonb_array_length(expected_items) BETWEEN 1 AND 3
        )
    ),
    CHECK (
        (target_plan_code = 'pro' AND target_channel_quantity BETWEEN 1 AND 6)
        OR (target_plan_code = 'team' AND target_channel_quantity BETWEEN 1 AND 9)
        OR (target_plan_code = 'start' AND target_channel_quantity = 3)
        OR (
            target_plan_code = 'unlimited'
            AND target_channel_quantity IS NULL
        )
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS f10_subscription_changes_one_active_idx
    ON f10_subscription_changes (workspace_id)
    WHERE status IN ('dispatching', 'pending');

CREATE INDEX IF NOT EXISTS f10_subscription_changes_status_idx
    ON f10_subscription_changes (status, updated_at);

CREATE OR REPLACE FUNCTION f10_plan_change_overages(
    p_workspace_id text,
    p_target_plan_code text,
    p_target_channel_quantity bigint
) RETURNS jsonb
LANGUAGE sql
STABLE
AS $function$
    WITH target_limits AS (
        SELECT plans.member_limit,
               CASE
                   WHEN plans.code = 'unlimited' THEN NULL
                   ELSE p_target_channel_quantity
               END AS channel_limit,
               plans.scheduled_publication_limit
          FROM f10_public_plans AS plans
         WHERE plans.code = p_target_plan_code
    ),
    resources(resource, quota_limit) AS (
        SELECT 'members'::text, member_limit FROM target_limits
        UNION ALL
        SELECT 'channels'::text, channel_limit FROM target_limits
        UNION ALL
        SELECT
            'scheduled_publications'::text,
            scheduled_publication_limit
          FROM target_limits
    ),
    overages AS (
        SELECT resources.resource,
               coalesce(counters.used, 0) AS used,
               resources.quota_limit,
               coalesce(counters.used, 0) - resources.quota_limit AS excess
          FROM resources
          LEFT JOIN f10_usage_counters AS counters
            ON counters.workspace_id = p_workspace_id
           AND counters.resource = resources.resource
           AND counters.window_start =
               '1970-01-01 00:00:00+00'::timestamptz
         WHERE resources.quota_limit IS NOT NULL
           AND coalesce(counters.used, 0) > resources.quota_limit
    )
    SELECT coalesce(
        jsonb_agg(
            jsonb_build_object(
                'resource', resource,
                'used', used,
                'limit', quota_limit,
                'excess', excess
            )
            ORDER BY resource
        ),
        '[]'::jsonb
    )
      FROM overages;
$function$;

CREATE OR REPLACE FUNCTION f10_begin_subscription_change(
    p_workspace_id text,
    p_idempotency_key text,
    p_source_plan_code text,
    p_source_billing_interval text,
    p_source_channel_quantity bigint,
    p_source_subscription_id text,
    p_target_plan_code text,
    p_target_billing_interval text,
    p_target_channel_quantity bigint,
    p_direction text,
    p_action text,
    p_expected_items jsonb,
    p_catalog_version text
) RETURNS TABLE (
    result_code text,
    change_status text,
    subscription_id text,
    overages jsonb
)
LANGUAGE plpgsql
AS $function$
DECLARE
    v_billing f10_workspace_billing%ROWTYPE;
    v_existing f10_subscription_changes%ROWTYPE;
    v_active f10_subscription_changes%ROWTYPE;
    v_overages jsonb := '[]'::jsonb;
BEGIN
    IF length(p_idempotency_key) NOT BETWEEN 1 AND 255
       OR p_source_plan_code NOT IN ('start', 'pro', 'team', 'unlimited')
       OR p_source_billing_interval NOT IN ('monthly', 'annual')
       OR p_target_plan_code NOT IN ('start', 'pro', 'team', 'unlimited')
       OR p_target_billing_interval NOT IN ('monthly', 'annual')
       OR p_direction NOT IN ('upgrade', 'downgrade')
       OR p_action NOT IN ('update_subscription', 'cancel_subscription')
       OR jsonb_typeof(p_expected_items) <> 'array'
       OR p_catalog_version <> 'd09-v2' THEN
        RAISE EXCEPTION 'invalid subscription change registration';
    END IF;

    SELECT *
      INTO v_billing
      FROM f10_workspace_billing
     WHERE workspace_id = p_workspace_id
     FOR UPDATE;
    IF NOT FOUND THEN
        RETURN QUERY SELECT
            'entitlement_unavailable', ''::text, ''::text, '[]'::jsonb;
        RETURN;
    END IF;

    SELECT *
      INTO v_existing
      FROM f10_subscription_changes
     WHERE workspace_id = p_workspace_id
       AND idempotency_key = p_idempotency_key;
    IF FOUND THEN
        IF v_existing.target_plan_code <> p_target_plan_code
           OR v_existing.target_billing_interval <> p_target_billing_interval
           OR v_existing.target_channel_quantity IS DISTINCT FROM
              p_target_channel_quantity
           OR v_existing.direction <> p_direction
           OR v_existing.action <> p_action
           OR v_existing.expected_items <> p_expected_items THEN
            RETURN QUERY SELECT
                'idempotency_conflict',
                v_existing.status,
                v_existing.provider_subscription_id,
                '[]'::jsonb;
            RETURN;
        END IF;
        RETURN QUERY SELECT
            'replay',
            v_existing.status,
            v_existing.provider_subscription_id,
            '[]'::jsonb;
        RETURN;
    END IF;

    IF v_billing.plan_code <> p_source_plan_code
       OR v_billing.billing_interval <> p_source_billing_interval
       OR v_billing.channel_quantity IS DISTINCT FROM
          p_source_channel_quantity
       OR coalesce(v_billing.paddle_subscription_id, '') <>
          p_source_subscription_id THEN
        RETURN QUERY SELECT
            'state_conflict', ''::text, ''::text, '[]'::jsonb;
        RETURN;
    END IF;

    SELECT *
      INTO v_active
      FROM f10_subscription_changes
     WHERE workspace_id = p_workspace_id
       AND status IN ('dispatching', 'pending');
    IF FOUND THEN
        RETURN QUERY SELECT
            'change_in_progress',
            v_active.status,
            v_active.provider_subscription_id,
            '[]'::jsonb;
        RETURN;
    END IF;

    IF p_direction = 'downgrade' THEN
        v_overages := f10_plan_change_overages(
            p_workspace_id,
            p_target_plan_code,
            p_target_channel_quantity
        );
        IF jsonb_array_length(v_overages) <> 0 THEN
            RETURN QUERY SELECT
                'downgrade_blocked',
                ''::text,
                p_source_subscription_id,
                v_overages;
            RETURN;
        END IF;
    END IF;

    INSERT INTO f10_subscription_changes (
        workspace_id,
        idempotency_key,
        source_plan_code,
        source_billing_interval,
        source_channel_quantity,
        target_plan_code,
        target_billing_interval,
        target_channel_quantity,
        direction,
        action,
        expected_items,
        catalog_version,
        provider_subscription_id,
        status
    ) VALUES (
        p_workspace_id,
        p_idempotency_key,
        p_source_plan_code,
        p_source_billing_interval,
        p_source_channel_quantity,
        p_target_plan_code,
        p_target_billing_interval,
        p_target_channel_quantity,
        p_direction,
        p_action,
        p_expected_items,
        p_catalog_version,
        p_source_subscription_id,
        'dispatching'
    );

    RETURN QUERY SELECT
        'dispatch', 'dispatching', p_source_subscription_id, '[]'::jsonb;
END;
$function$;

CREATE OR REPLACE FUNCTION f10_mark_subscription_change_pending(
    p_workspace_id text,
    p_idempotency_key text
) RETURNS text
LANGUAGE plpgsql
AS $function$
DECLARE
    v_status text;
BEGIN
    UPDATE f10_subscription_changes
       SET status = 'pending',
           updated_at = now()
     WHERE workspace_id = p_workspace_id
       AND idempotency_key = p_idempotency_key
       AND status = 'dispatching';

    SELECT status
      INTO v_status
      FROM f10_subscription_changes
     WHERE workspace_id = p_workspace_id
       AND idempotency_key = p_idempotency_key;
    RETURN coalesce(v_status, '');
END;
$function$;

CREATE OR REPLACE FUNCTION f10_mark_subscription_change_failed(
    p_workspace_id text,
    p_idempotency_key text
) RETURNS boolean
LANGUAGE plpgsql
AS $function$
BEGIN
    UPDATE f10_subscription_changes
       SET status = 'provider_failed',
           updated_at = now()
     WHERE workspace_id = p_workspace_id
       AND idempotency_key = p_idempotency_key
       AND status = 'dispatching';
    RETURN FOUND;
END;
$function$;

DO $migration$
BEGIN
    IF to_regprocedure(
        'f10_apply_billing_event_before_plan_changes(text,text,timestamp with time zone,text,text,text,bigint,text,text,text,text,timestamp with time zone,timestamp with time zone,boolean)'
    ) IS NULL THEN
        ALTER FUNCTION f10_apply_billing_event(
            text, text, timestamptz, text, text, text, bigint, text, text,
            text, text, timestamptz, timestamptz, boolean
        ) RENAME TO f10_apply_billing_event_before_plan_changes;
    END IF;
END;
$migration$;

CREATE OR REPLACE FUNCTION f10_apply_billing_event(
    p_event_id text,
    p_event_type text,
    p_occurred_at timestamptz,
    p_workspace_id text,
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
    v_pending f10_subscription_changes%ROWTYPE;
    v_overages jsonb;
    v_first boolean;
    v_changed boolean;
    v_target_changed boolean := false;
    v_is_newer boolean := true;
BEGIN
    SELECT *
      INTO v_current
      FROM f10_workspace_billing
     WHERE workspace_id = p_workspace_id
     FOR UPDATE;
    IF v_current.workspace_id IS NOT NULL
       AND v_current.last_provider_event_created_at IS NOT NULL THEN
        v_is_newer := (p_occurred_at, p_event_id) >
            (
                v_current.last_provider_event_created_at,
                v_current.last_provider_event_id
            );
    END IF;

    SELECT *
      INTO v_pending
      FROM f10_subscription_changes
     WHERE workspace_id = p_workspace_id
       AND status IN ('dispatching', 'pending');

    IF p_apply_state AND v_is_newer AND FOUND
       AND p_event_type IN ('transaction.completed', 'subscription.canceled')
       AND (
           v_pending.target_plan_code <> p_plan_code
           OR v_pending.target_billing_interval <> p_billing_interval
           OR v_pending.target_channel_quantity IS DISTINCT FROM
              p_channel_quantity
       ) THEN
        RAISE EXCEPTION
            'Paddle event conflicts with pending subscription change';
    END IF;

    IF p_apply_state AND v_is_newer
       AND v_current.workspace_id IS NOT NULL THEN
        v_target_changed := v_current.plan_code <> p_plan_code
            OR v_current.channel_quantity IS DISTINCT FROM p_channel_quantity;
        IF v_target_changed THEN
            v_overages := f10_plan_change_overages(
                p_workspace_id,
                p_plan_code,
                p_channel_quantity
            );
            IF jsonb_array_length(v_overages) <> 0 THEN
                RAISE EXCEPTION
                    'Paddle downgrade exceeds current workspace usage: %',
                    v_overages;
            END IF;
        END IF;
    END IF;

    SELECT result.first_delivery, result.state_changed
      INTO v_first, v_changed
      FROM f10_apply_billing_event_before_plan_changes(
          p_event_id,
          p_event_type,
          p_occurred_at,
          p_workspace_id,
          p_plan_code,
          p_billing_interval,
          p_channel_quantity,
          p_billing_state,
          p_customer_id,
          p_subscription_id,
          p_transaction_id,
          p_period_start,
          p_period_end,
          p_apply_state
      ) AS result;

    IF v_first AND v_changed
       AND v_pending.workspace_id IS NOT NULL
       AND v_pending.target_plan_code = p_plan_code
       AND v_pending.target_billing_interval = p_billing_interval
       AND v_pending.target_channel_quantity IS NOT DISTINCT FROM
           p_channel_quantity THEN
        UPDATE f10_subscription_changes
           SET status = 'applied',
               applied_event_id = p_event_id,
               updated_at = p_occurred_at
         WHERE workspace_id = v_pending.workspace_id
           AND idempotency_key = v_pending.idempotency_key
           AND status IN ('dispatching', 'pending');
    END IF;

    RETURN QUERY SELECT v_first, v_changed;
END;
$function$;

DO $migration$
BEGIN
    IF to_regprocedure(
        'f10_apply_usage_before_plan_changes(text,text,bigint,text,timestamp with time zone)'
    ) IS NULL THEN
        ALTER FUNCTION f10_apply_usage(
            text, text, bigint, text, timestamptz
        ) RENAME TO f10_apply_usage_before_plan_changes;
    END IF;
END;
$migration$;

CREATE OR REPLACE FUNCTION f10_apply_usage(
    p_workspace_id text,
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
    v_pending f10_subscription_changes%ROWTYPE;
    v_operation f10_usage_operations%ROWTYPE;
    v_window_start timestamptz :=
        '1970-01-01 00:00:00+00'::timestamptz;
    v_limit bigint;
    v_used bigint := 0;
BEGIN
    SELECT *
      INTO v_billing
      FROM f10_workspace_billing
     WHERE workspace_id = p_workspace_id
     FOR UPDATE;

    IF p_delta > 0 THEN
        SELECT *
          INTO v_pending
          FROM f10_subscription_changes
         WHERE workspace_id = p_workspace_id
           AND direction = 'downgrade'
           AND status IN ('dispatching', 'pending');
    END IF;

    IF v_pending.workspace_id IS NOT NULL THEN
        SELECT *
          INTO v_operation
          FROM f10_usage_operations
         WHERE workspace_id = p_workspace_id
           AND idempotency_key = p_idempotency_key;
        IF FOUND THEN
            RETURN QUERY
            SELECT *
              FROM f10_apply_usage_before_plan_changes(
                  p_workspace_id,
                  p_resource,
                  p_delta,
                  p_idempotency_key,
                  p_occurred_at
              );
            RETURN;
        END IF;

        IF p_resource = 'channels' THEN
            v_limit := v_pending.target_channel_quantity;
        ELSE
            SELECT CASE p_resource
                       WHEN 'members' THEN member_limit
                       WHEN 'scheduled_publications'
                           THEN scheduled_publication_limit
                   END
              INTO v_limit
              FROM f10_public_plans
             WHERE code = v_pending.target_plan_code;
        END IF;

        SELECT counters.used
          INTO v_used
          FROM f10_usage_counters AS counters
         WHERE counters.workspace_id = p_workspace_id
           AND counters.resource = p_resource
           AND counters.window_start = v_window_start;
        v_used := coalesce(v_used, 0);

        IF v_limit IS NOT NULL AND p_delta > v_limit - v_used THEN
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
                false,
                'limit_reached',
                false,
                v_used,
                v_limit,
                greatest(v_limit - v_used, 0),
                v_used > v_limit,
                p_occurred_at
            );
            RETURN QUERY SELECT
                false,
                'limit_reached'::text,
                false,
                v_used,
                v_limit,
                greatest(v_limit - v_used, 0),
                v_used > v_limit;
            RETURN;
        END IF;
    END IF;

    RETURN QUERY
    SELECT *
      FROM f10_apply_usage_before_plan_changes(
          p_workspace_id,
          p_resource,
          p_delta,
          p_idempotency_key,
          p_occurred_at
      );
END;
$function$;
