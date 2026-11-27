# Multi-tenant + webhook overlay (Phase 2 + Phase 3)

Layered on top of Phase 1. Three schema migrations, a Prometheus
postgres-exporter and a Python sidecar that dispatches signed webhooks.

## What ships

| File                                                  | What it is                                            |
|-------------------------------------------------------|-------------------------------------------------------|
| `deployment/multitenant/01_tenants.sql`               | `tenants` table + seed `default`                      |
| `deployment/multitenant/02_tenant_fk.sql`             | `tenant_id` FK on directory_domains, config_sofia_profile_gateways, dialplan_contexts, cdr |
| `deployment/multitenant/03_metrics_views.sql`         | `tenant_call_metrics` view + quota helper functions   |
| `deployment/multitenant/04_webhooks.sql`              | `webhook_delivery_log` + NOTIFY trigger on CDR insert |
| `deployment/multitenant/pg_exporter_queries.yaml`     | Prometheus postgres-exporter queries                  |
| `deployment/multitenant/grafana-tenant-dashboard.json`| Per-tenant panel pack                                 |
| `deployment/multitenant/webhook/`                     | Dockerized dispatcher sidecar                         |
| `docker-compose.multitenant.yml`                      | Overlay wiring all of the above                       |

## Deploy

```bash
docker compose \
    -f docker-compose.yml \
    -f docker-compose.opensips.yml \
    -f docker-compose.multitenant.yml \
    up -d
```

`multitenant-init` applies the four `.sql` files in order and exits
`completed_successfully`. `postgres-exporter` scrapes the metrics view every 15s
at `postgres-exporter:9187/metrics` — add the target to Prometheus via
`deployment/observability/prometheus/prometheus.yml`. `webhook-dispatcher`
listens on `webhook` channel and dispatches.

## Per-tenant webhook flow

1. CustomPBX's existing CDR writer inserts a row into `cdr` as before.
2. `trg_cdr_webhook_notify` fires a `pg_notify('webhook', ...)` with the CDR id
   and tenant id.
3. The sidecar picks it up, resolves `tenants.webhook_url` +
   `tenants.webhook_secret` for the tenant, signs the body with HMAC-SHA256
   (`X-Pbx-Signature: sha256=...`), POSTs, and writes an entry in
   `webhook_delivery_log`.
4. Failed attempts get `next_retry_at` bumped using exponential backoff:
   `10s -> 30s -> 2m -> 10m -> 1h -> 6h -> 24h` (seven attempts total). A
   periodic sweep wakes every 15s to catch retries.

## Backfill

Every existing row (domains, gateways, dialplan contexts, CDRs) is assigned to
the `default` tenant during the migration. The env-var webhook URL + secret the
CustomPBX container reads today are copied onto the default tenant so the
current receiver stays live without any producer-side change.

## Metrics published

| Metric                                            | Labels      | Kind    |
|---------------------------------------------------|-------------|---------|
| `pg_tenant_calls_calls_total`                     | slug, name  | counter |
| `pg_tenant_calls_minutes_total`                   | slug, name  | counter |
| `pg_tenant_calls_calls_today`                     | slug, name  | gauge   |
| `pg_tenant_calls_minutes_today`                   | slug, name  | gauge   |
| `pg_tenant_calls_calls_in_flight_estimate`        | slug, name  | gauge   |
| `pg_tenant_registered_registered`                 | slug        | gauge   |

Grafana dashboard `grafana-tenant-dashboard.json` is a drop-in; import and pick
Prometheus as the datasource.

## Quota enforcement

Phase 2 ships the primitives, not the dialplan hook (that belongs in a
CustomPBX change the Go WIP is already touching):

- `tenant_can_place_call(tenant_id)` — bool, true when the tenant is active and
  under both the cumulative calls and minutes cap.
- `tenant_minutes_remaining(tenant_id)` — integer, NULL for an unlimited plan.

Wire them in from the outbound Lua / dialplan expression:

```xml
<extension name="guarded_outbound" continue="false">
  <condition field="${pg_query postgres://.../custompbx SELECT tenant_can_place_call($tenant_id)}" expression="^t$"/>
  <action application="bridge" data="sofia/gateway/.../${destination_number}"/>
</extension>
```

## Phase 2c — admin UI (deferred)

RBAC + tenant CRUD in `src/cweb-app` + the matching Go endpoints collide
head-on with the WIP observability refactor already on `main`. Those changes
belong in their own PR once the WIP lands; the schema here is the contract that
unblocks them.
