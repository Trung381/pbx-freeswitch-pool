-- Phase 2 — create the tenant table, its lifecycle columns, and the single
-- `default` tenant that owns every row already in the database when Phase 2
-- lands. The table lives beside custompbx's existing config_* tables; it is
-- intentionally NOT wired into configurations_list because tenants are a
-- deployment-level concept (billing, quota, webhook target), not a FreeSWITCH
-- config object.

CREATE TABLE IF NOT EXISTS tenants (
    id                     SERIAL PRIMARY KEY,
    slug                   VARCHAR(64)  NOT NULL UNIQUE,
    name                   VARCHAR(128) NOT NULL,
    status                 VARCHAR(16)  NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'suspended', 'archived')),
    plan_minutes_quota     INTEGER      NOT NULL DEFAULT 0,  -- 0 = unlimited
    plan_calls_max         INTEGER      NOT NULL DEFAULT 0,  -- cumulative, 0 = unlimited
    plan_extensions_max    INTEGER      NOT NULL DEFAULT 0,  -- 0 = unlimited
    concurrent_max         INTEGER      NOT NULL DEFAULT 0,  -- 0 = unlimited
    webhook_url            VARCHAR(512),
    webhook_secret         VARCHAR(128),
    webhook_events         TEXT[]       NOT NULL DEFAULT ARRAY['call.*']::TEXT[],
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS tenants_status_idx ON tenants (status) WHERE status <> 'archived';

CREATE OR REPLACE FUNCTION tenants_touch_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_tenants_updated_at ON tenants;
CREATE TRIGGER trg_tenants_updated_at BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION tenants_touch_updated_at();

-- Seed the default tenant. Everything that exists today belongs to it and keeps
-- working without any per-call changes. Phase 2b flips the FKs to NOT NULL once
-- backfill is verified.
INSERT INTO tenants (slug, name, status)
VALUES ('default', 'Default Tenant', 'active')
ON CONFLICT (slug) DO NOTHING;
