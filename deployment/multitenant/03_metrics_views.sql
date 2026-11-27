-- Phase 2 — SQL views the Prometheus exporter scrapes to publish per-tenant
-- counters:
--   tenant_calls_total        monotonic count of every CDR ever written
--   tenant_minutes_total      sum of billsec, in minutes (floor)
--   tenant_calls_today        counter since UTC midnight (for the daily quota)
--   tenant_minutes_today      minutes since UTC midnight
--   tenant_concurrent_estimate how many CDRs look open (end_stamp = start_stamp)
--   tenant_sip_registered     usrloc counts, joined through the opensips DB
-- The exporter's queries.yaml below pulls each one as a series keyed by slug.
-- A view beats a materialized view here — CDR cardinality is low enough that
-- scraping every 15 s is cheap, and the counters have to be live for billing.

CREATE OR REPLACE VIEW tenant_call_metrics AS
SELECT t.id, t.slug, t.name,
       COALESCE(COUNT(c.id), 0)::BIGINT AS calls_total,
       COALESCE(SUM(c.billsec) / 60, 0)::BIGINT AS minutes_total,
       COALESCE(SUM(c.billsec) FILTER (
           WHERE c.start_stamp >= date_trunc('day', NOW() AT TIME ZONE 'UTC')
       ) / 60, 0)::BIGINT AS minutes_today,
       COALESCE(COUNT(c.id) FILTER (
           WHERE c.start_stamp >= date_trunc('day', NOW() AT TIME ZONE 'UTC')
       ), 0)::BIGINT AS calls_today,
       COALESCE(COUNT(c.id) FILTER (
           WHERE c.answer_stamp IS NULL AND c.start_stamp > NOW() - INTERVAL '1 hour'
       ), 0)::BIGINT AS calls_in_flight_estimate
FROM tenants t
LEFT JOIN cdr c ON c.tenant_id = t.id
GROUP BY t.id, t.slug, t.name;

-- Quota helpers Phase 2 dialplan uses to short-circuit an outbound bridge
-- before it ever touches a gateway. Returns NULL for an unlimited plan so the
-- dialplan expression can treat "no cap" as "always allowed".

CREATE OR REPLACE FUNCTION tenant_minutes_remaining(p_tenant_id INTEGER)
RETURNS INTEGER AS $$
    SELECT CASE
        WHEN t.plan_minutes_quota = 0 THEN NULL
        ELSE GREATEST(0, t.plan_minutes_quota - COALESCE(v.minutes_total, 0))::INTEGER
    END
    FROM tenants t
    LEFT JOIN tenant_call_metrics v ON v.id = t.id
    WHERE t.id = p_tenant_id;
$$ LANGUAGE sql STABLE;

CREATE OR REPLACE FUNCTION tenant_can_place_call(p_tenant_id INTEGER)
RETURNS BOOLEAN AS $$
    SELECT CASE
        WHEN t.status <> 'active' THEN FALSE
        WHEN t.plan_calls_max > 0
             AND COALESCE(v.calls_total, 0) >= t.plan_calls_max THEN FALSE
        WHEN t.plan_minutes_quota > 0
             AND COALESCE(v.minutes_total, 0) >= t.plan_minutes_quota THEN FALSE
        ELSE TRUE
    END
    FROM tenants t
    LEFT JOIN tenant_call_metrics v ON v.id = t.id
    WHERE t.id = p_tenant_id;
$$ LANGUAGE sql STABLE;

GRANT SELECT ON tenant_call_metrics TO custompbx;
