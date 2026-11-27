-- Phase 2 — add tenant_id FK to every table whose rows we scope per tenant:
--   directory_domains  (users, auth)
--   config_sofia_profile_gateways (per-tenant trunks)
--   dialplan_contexts  (per-tenant routing)
--   cdr                (per-tenant billing and the webhook stream)
-- Each column is added NULLable, backfilled to the `default` tenant, then set
-- NOT NULL. Phase 2c (RBAC in the admin UI) relies on these FKs; nothing in
-- Phase 2a enforces visibility yet.

DO $$
DECLARE
    v_default_tenant INTEGER;
BEGIN
    SELECT id INTO v_default_tenant FROM tenants WHERE slug = 'default';
    IF v_default_tenant IS NULL THEN
        RAISE EXCEPTION 'default tenant missing — run 01_tenants.sql first';
    END IF;

    -- directory_domains
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name='directory_domains' AND column_name='tenant_id') THEN
        ALTER TABLE directory_domains ADD COLUMN tenant_id INTEGER
            REFERENCES tenants(id) ON DELETE RESTRICT;
    END IF;
    UPDATE directory_domains SET tenant_id = v_default_tenant WHERE tenant_id IS NULL;
    ALTER TABLE directory_domains ALTER COLUMN tenant_id SET NOT NULL;
    CREATE INDEX IF NOT EXISTS directory_domains_tenant_id_idx
        ON directory_domains (tenant_id);

    -- config_sofia_profile_gateways
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name='config_sofia_profile_gateways' AND column_name='tenant_id') THEN
        ALTER TABLE config_sofia_profile_gateways ADD COLUMN tenant_id INTEGER
            REFERENCES tenants(id) ON DELETE RESTRICT;
    END IF;
    UPDATE config_sofia_profile_gateways SET tenant_id = v_default_tenant WHERE tenant_id IS NULL;
    ALTER TABLE config_sofia_profile_gateways ALTER COLUMN tenant_id SET NOT NULL;
    CREATE INDEX IF NOT EXISTS config_sofia_profile_gateways_tenant_id_idx
        ON config_sofia_profile_gateways (tenant_id);

    -- dialplan_contexts
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name='dialplan_contexts' AND column_name='tenant_id') THEN
        ALTER TABLE dialplan_contexts ADD COLUMN tenant_id INTEGER
            REFERENCES tenants(id) ON DELETE RESTRICT;
    END IF;
    UPDATE dialplan_contexts SET tenant_id = v_default_tenant WHERE tenant_id IS NULL;
    ALTER TABLE dialplan_contexts ALTER COLUMN tenant_id SET NOT NULL;
    CREATE INDEX IF NOT EXISTS dialplan_contexts_tenant_id_idx
        ON dialplan_contexts (tenant_id);

    -- cdr (no RESTRICT — archived tenants keep their history)
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name='cdr' AND column_name='tenant_id') THEN
        ALTER TABLE cdr ADD COLUMN tenant_id INTEGER REFERENCES tenants(id);
    END IF;
    UPDATE cdr SET tenant_id = v_default_tenant WHERE tenant_id IS NULL;
    ALTER TABLE cdr ALTER COLUMN tenant_id SET NOT NULL;
    CREATE INDEX IF NOT EXISTS cdr_tenant_id_start_stamp_idx
        ON cdr (tenant_id, start_stamp DESC);

    -- Column default so writers that don't know about tenant_id yet (the on-main
    -- Go WIP) keep working. Phase 2c teaches the writer to carry the caller's
    -- tenant through and remove this default.
    EXECUTE format('ALTER TABLE directory_domains ALTER COLUMN tenant_id SET DEFAULT %s', v_default_tenant);
    EXECUTE format('ALTER TABLE config_sofia_profile_gateways ALTER COLUMN tenant_id SET DEFAULT %s', v_default_tenant);
    EXECUTE format('ALTER TABLE dialplan_contexts ALTER COLUMN tenant_id SET DEFAULT %s', v_default_tenant);
    EXECUTE format('ALTER TABLE cdr ALTER COLUMN tenant_id SET DEFAULT %s', v_default_tenant);
END $$;
