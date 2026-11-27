-- Phase 3 — the webhook stream. tenants already has webhook_url /
-- webhook_secret / webhook_events from 01_tenants.sql; this file adds the
-- delivery log + NOTIFY plumbing the sidecar sees. Every CDR row becomes a
-- `cdr_written` payload the sidecar picks up through LISTEN/NOTIFY.

CREATE TABLE IF NOT EXISTS webhook_delivery_log (
    id             BIGSERIAL PRIMARY KEY,
    tenant_id      INTEGER      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    event          VARCHAR(64)  NOT NULL,
    cdr_uuid       UUID,
    url            VARCHAR(512) NOT NULL,
    attempt        INTEGER      NOT NULL DEFAULT 1,
    status_code    INTEGER,
    response_body  TEXT,
    error          TEXT,
    payload        JSONB        NOT NULL,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    delivered_at   TIMESTAMPTZ,
    next_retry_at  TIMESTAMPTZ
);

-- Pending retries sorted by when they are due.
CREATE INDEX IF NOT EXISTS webhook_delivery_log_pending_idx
    ON webhook_delivery_log (next_retry_at)
    WHERE delivered_at IS NULL AND next_retry_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS webhook_delivery_log_tenant_time_idx
    ON webhook_delivery_log (tenant_id, created_at DESC);

-- Trigger: every CDR row emits a NOTIFY on channel 'webhook' with just the
-- primary key. The sidecar fetches the row itself so the payload has whatever
-- the writer left in the CDR after the hangup hook finished.
CREATE OR REPLACE FUNCTION notify_cdr_written() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('webhook',
        json_build_object(
            'event',     'cdr.written',
            'tenant_id', NEW.tenant_id,
            'cdr_id',    NEW.id,
            'uuid',      NEW.uuid
        )::text
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_cdr_webhook_notify ON cdr;
CREATE TRIGGER trg_cdr_webhook_notify AFTER INSERT ON cdr
    FOR EACH ROW EXECUTE FUNCTION notify_cdr_written();

-- Phase 1 kept the per-call webhook URL in env vars for CustomPBX — Phase 3
-- moves per-tenant overrides into tenants.webhook_url. A migration script seeds
-- the default tenant with whatever the env var already pointed at so nothing
-- regresses during the cut-over:
UPDATE tenants
SET webhook_url = COALESCE(webhook_url, 'https://gmo.techxanh.com/webhooks/pbx/calls'),
    webhook_secret = COALESCE(webhook_secret, 'e6d4f80576a2d702c388af3c8b500fe593c6f9a1168f54b83da4ce363ab708d2')
WHERE slug = 'default' AND webhook_url IS NULL;
