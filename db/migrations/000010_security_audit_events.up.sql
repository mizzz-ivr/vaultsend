CREATE TABLE IF NOT EXISTS security_audit_events (
    id uuid PRIMARY KEY,
    occurred_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    event_type varchar(100) NOT NULL CHECK (event_type ~ '^[a-z0-9][a-z0-9_.-]{2,99}$'),
    severity varchar(20) NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    outcome varchar(20) NOT NULL CHECK (outcome IN ('success', 'denied', 'failure')),
    actor_type varchar(20) NOT NULL CHECK (actor_type IN ('user', 'anonymous', 'recipient', 'system', 'webhook')),
    actor_user_id uuid NULL,
    organization_id uuid NULL,
    resource_type varchar(50) NULL CHECK (resource_type IS NULL OR resource_type ~ '^[a-z0-9][a-z0-9_.-]{1,49}$'),
    resource_id uuid NULL,
    request_id varchar(100) NULL,
    source_service varchar(50) NOT NULL CHECK (source_service IN ('api', 'mail-worker', 'cleanup-worker')),
    http_method varchar(10) NULL,
    route_pattern varchar(200) NULL,
    status_code integer NULL CHECK (status_code IS NULL OR status_code BETWEEN 100 AND 599),
    client_ip_hmac char(64) NULL CHECK (client_ip_hmac IS NULL OR client_ip_hmac ~ '^[0-9a-f]{64}$'),
    user_agent_hmac char(64) NULL CHECK (user_agent_hmac IS NULL OR user_agent_hmac ~ '^[0-9a-f]{64}$'),
    details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(details) = 'object'
        AND octet_length(details::text) <= 8192
    ),
    integrity_key_id varchar(50) NOT NULL,
    integrity_hmac char(64) NOT NULL CHECK (integrity_hmac ~ '^[0-9a-f]{64}$'),
    CHECK (actor_type <> 'user' OR actor_user_id IS NOT NULL)
);

COMMENT ON TABLE security_audit_events IS '追記専用のセキュリティ監査ログ。通常のアプリケーションデータと分離し、更新・削除・TRUNCATEを禁止する。';
COMMENT ON COLUMN security_audit_events.client_ip_hmac IS '接続元IPを監査専用秘密鍵でHMAC-SHA256した仮名化値。匿名情報として扱わない。';
COMMENT ON COLUMN security_audit_events.user_agent_hmac IS 'User-Agentを監査専用秘密鍵でHMAC-SHA256した仮名化値。';
COMMENT ON COLUMN security_audit_events.integrity_hmac IS 'イベント主要項目を監査専用秘密鍵でHMAC-SHA256した改ざん検知値。';

CREATE INDEX IF NOT EXISTS idx_security_audit_events_occurred_at
    ON security_audit_events (occurred_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_security_audit_events_org_occurred_at
    ON security_audit_events (organization_id, occurred_at DESC, id DESC)
    WHERE organization_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_security_audit_events_actor_occurred_at
    ON security_audit_events (actor_user_id, occurred_at DESC, id DESC)
    WHERE actor_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_security_audit_events_type_occurred_at
    ON security_audit_events (event_type, occurred_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_security_audit_events_request_id
    ON security_audit_events (request_id)
    WHERE request_id IS NOT NULL;

CREATE OR REPLACE FUNCTION reject_security_audit_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'security_audit_events is append-only'
        USING ERRCODE = '55000';
END;
$$;

DROP TRIGGER IF EXISTS security_audit_events_reject_update_delete ON security_audit_events;
CREATE TRIGGER security_audit_events_reject_update_delete
BEFORE UPDATE OR DELETE ON security_audit_events
FOR EACH ROW EXECUTE FUNCTION reject_security_audit_event_mutation();

DROP TRIGGER IF EXISTS security_audit_events_reject_truncate ON security_audit_events;
CREATE TRIGGER security_audit_events_reject_truncate
BEFORE TRUNCATE ON security_audit_events
FOR EACH STATEMENT EXECUTE FUNCTION reject_security_audit_event_mutation();
