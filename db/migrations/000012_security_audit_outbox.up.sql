CREATE TABLE IF NOT EXISTS security_audit_outbox (
    id uuid PRIMARY KEY,
    occurred_at timestamptz NOT NULL,
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
    available_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (actor_type <> 'user' OR actor_user_id IS NOT NULL)
);

COMMENT ON TABLE security_audit_outbox IS '業務更新と同一トランザクションで記録し、audit-workerがsecurity_audit_eventsへ配送する一時outbox。';
COMMENT ON COLUMN security_audit_outbox.processed_at IS 'security_audit_eventsへの冪等配送が完了した時刻。';

CREATE INDEX IF NOT EXISTS idx_security_audit_outbox_pending
    ON security_audit_outbox (available_at ASC, created_at ASC, id ASC)
    WHERE processed_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_security_audit_outbox_processed_at
    ON security_audit_outbox (processed_at ASC)
    WHERE processed_at IS NOT NULL;
