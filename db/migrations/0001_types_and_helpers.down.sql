-- Rollback di 0001.

DROP FUNCTION IF EXISTS set_updated_at();
DROP FUNCTION IF EXISTS has_unique_elements(anyarray);

DROP TYPE IF EXISTS ai_provider;
DROP TYPE IF EXISTS sync_status;
DROP TYPE IF EXISTS repository_provider;
DROP TYPE IF EXISTS notification_status;
DROP TYPE IF EXISTS notification_event;
DROP TYPE IF EXISTS alert_channel;
DROP TYPE IF EXISTS billing_period;
DROP TYPE IF EXISTS subscription_status;
DROP TYPE IF EXISTS execution_trigger;
DROP TYPE IF EXISTS execution_status;
DROP TYPE IF EXISTS retry_backoff;
DROP TYPE IF EXISTS http_method;
DROP TYPE IF EXISTS user_role;
DROP TYPE IF EXISTS environment;
