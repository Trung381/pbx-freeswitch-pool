# Observability cho PBX-FREESWITCH-POOL

Stack gon cho VPS RAM chat: **Grafana + Loki + Prometheus + Grafana Alloy + node_exporter + cAdvisor**.
Moi du an 1 Grafana rieng. Trace: Tempo (tuy chon, xem muc Tracing). Khong Mimir; alert dung Grafana Alerting (khong Alertmanager).
Retention: log 5 ngay (`loki/loki.yml`), metrics 10 ngay / 1GB (`.env`).

## Thanh phan
| File | Muc dich |
|---|---|
| `docker-compose.observability.yml` | Stack giam sat (project `pbx-observability`), moi container co `mem_limit` + `cpus` |
| `docker-compose.observability-fs-log.yml` | Override cho compose chinh: named volume log FreeSWITCH (thay volume an danh), xoay log 20MB x3 |
| `fs_conf/logfile.conf.xml` | Cau hinh mod_logfile (xoay log, bo debug, prefix UUID) |
| `alloy/config.alloy` | Thu log stdout 4 container + file log FreeSWITCH, parse `level`, `call_uuid`, che gia tri nhay cam |
| `tempo/tempo.yml` | Tempo single-binary (OTLP 4317/4318), retention 72h |
| `prometheus/prometheus.yml` | Scrape node, cadvisor, loki, alloy, grafana, custompbx:9102 |
| `postgres/create-grafana-ro.sql` | User Postgres chi doc cho Grafana (chi cot can cho CDR/HEP) |
| `grafana/provisioning/*` | Datasource, dashboard provider, alert rules, contact point Telegram |
| `grafana/dashboards/*.json` | Ha tang, Log explorer, Bao cao CDR/HEP, Suc khoe PBX |
| `tools/gen_grafana.py` | Sinh dashboards + alert rules (sua o day roi chay lai) |

## Trien khai
```bash
cd deployment/observability
cp .env.example .env && chmod 600 .env     # dien GRAFANA_ADMIN_PASSWORD, GRAFANA_PG_PASSWORD
# 1) Tao user Postgres chi doc (mot lan)
docker exec -i postgres-host psql -U custompbx -d custompbx -v pw="'<GRAFANA_PG_PASSWORD>'" < postgres/create-grafana-ro.sql
# 2) Doi volume log FreeSWITCH (restart freeswitch-host; lam khi khong co cuoc goi)
cd <repo-goc> && docker compose -f docker-compose.yml -f deployment/observability/docker-compose.observability-fs-log.yml up -d --no-deps freeswitch
# 3) Chay stack giam sat
cd deployment/observability && docker compose -f docker-compose.observability.yml --env-file .env up -d
```
Truy cap: `http://<GRAFANA_BIND_ADDR>:3000` **qua VPN WireGuard** (mac dinh `http://10.77.0.1:3000`). Khong publish ra IP cong khai.

## Bao mat
- Grafana bind vao IP wg0; Loki/Prometheus/Alloy/exporters chi trong docker network (khong `ports`).
- Alloy mount `docker.sock` (read-only) de doc log container: quyen tuong duong root tren host; chap nhan cho dev.
  Thay the an toan hon: doc file log json cua docker (`/var/lib/docker/containers`) hoac dung logging driver Loki.
- cAdvisor chay `privileged` (can de doc cgroup).
- Endpoint `/metrics` cua custompbx nghe o cong **9102** rieng (bien `CUSTOMPBX_METRICS_ADDR`, rong = tat), KHONG qua router web/nginx va khong co trong `ports` cua compose => chi Prometheus trong `local_network` goi duoc.
- User Postgres `grafana_ro`: `default_transaction_read_only`, `statement_timeout 20s`, khong cap `hep_payload`/`sip_body`/`sip_auth_*`.
- Log duoc che `password|secret|token|authorization|api_key = ...` o Alloy truoc khi luu.

## Telegram (dang HOLD)
Contact point `telegram-pbx` doc `TELEGRAM_BOT_TOKEN` / `TELEGRAM_CHAT_ID` tu `.env`. De trong thi Grafana van chay,
alert hien trong UI (Alerting > Alert rules), gui Telegram bao loi trong log. Dien token roi `docker compose up -d grafana`.

## Metrics ung dung (`/metrics`, cong 9102)
`pbx_calls_active`, `pbx_channels`, `pbx_calls_total{hangup_cause,direction}`, `pbx_esl_connected`, `pbx_database_connected`,
`pbx_esl_reconnect_attempts_total`, `pbx_gateway_registered{gateway}`, `pbx_gateway_state{gateway,state}`,
`pbx_hep_packets_total{result}` + metrics Go/process. Chi co sau khi build/deploy lai custompbx. Loi call webhook duoc canh bao qua log
(`dispatcher.go` chua co counter rieng).

## Log custompbx (JSON, log/slog)
custompbx ghi log JSON ra stdout (package `src/custompbx/logx`), moi dong co: `time`, `level` (debug|info|warn|error), `msg`,
`service=custompbx`, `component` (fsesl, db, web, websocket, cdr, hep, xmlcurl, cfg, ...), `call_uuid` (khi co) va `err`.
Thu vien ben thu ba dung `log` chuan van ra JSON voi `component=stdlog`. Gia tri nhay cam (password/token/secret/URL co mat khau) bi che `[REDACTED]`.

- Muc log: bien moi truong `LOG_LEVEL` cua container custompbx (`debug|info|warn|error`, mac dinh `info`). Event nhieu/chi tiet o muc `debug`.
- Alloy: dong JSON -> label `level` (DEBUG/INFO/WARNING/ERROR, dong nhat voi FreeSWITCH) + `component`; `call_uuid` la **structured metadata**
  (khong phai label, tranh cardinality). Dong khong phai JSON (panic Go...) van duoc nhan level bang heuristic cu.
- Dashboard "PBX - Log explorer": bien `Component`, `Call UUID`, panel "Trace theo call_uuid" (gop custompbx + FreeSWITCH).

LogQL mau:
```logql
# Moi log cua 1 cuoc goi (custompbx + FreeSWITCH, structured metadata call_uuid)
{source=~"stdout|file"} | call_uuid="3f1c0c7e-aaaa-bbbb-cccc-1234567890ab"
# Loi custompbx theo component, hien thi gon
{container="custompbx-host", level="ERROR"} | json | line_format "{{.component}} {{.msg}} {{.err}}"
# So loi theo component trong 10 phut
sum by (component) (count_over_time({container="custompbx-host", level="ERROR"}[10m]))
# Loc theo truong bat ky trong JSON
{container="custompbx-host", component="fsesl"} | json | delay_seconds > 60
```
Luu y: `dispatcher.go` (call webhook) van dung `log` chuan nen chuoi "Call webhook delivery failed ..." nam trong truong `msg`; rule
`pbx-webhook-fail` dem theo chuoi nay nen van hoat dong.

## Van hanh
- Dung luong: `docker system df`; Loki/Prometheus tu xoa theo retention.
- Doi thoi gian giu log: sua `retention_period` trong `loki/loki.yml` roi `docker compose up -d loki`.
- Rollback FreeSWITCH log: bo `-f ...fs-log.yml` va `up -d --no-deps freeswitch` (log cu o container layer se mat khi recreate; backup xem bao cao trien khai).

## Tracing (Grafana Tempo + OpenTelemetry)
Moi cuoc goi = 1 trace (trace id sinh co dinh tu `call_uuid`/linked_id, nen cac leg va ca sau khi restart van chung 1 trace). Span:
`esl.channel_create`, `esl.channel_answer`, `esl.channel_hangup` (CHANNEL_DESTROY), `callwebhook.deliver` (con, kem client span
HTTP `POST` va attribute `event_id`), `cdr.insert` (ghi CDR). Moi span co attribute `call_uuid`. Them server span cho web/websocket
(`custompbx.web`). Chua co span `bridge` (custompbx chua subscribe CHANNEL_BRIDGE). XML-CURL khong trace (poll lien tuc, qua on).

**Bat:**
1. `cd deployment/observability && docker compose -f docker-compose.observability.yml --env-file .env up -d tempo grafana`
   (tempo nam tren `local_network` nen custompbx goi duoc; khong publish port ra host).
2. Trong `.env` cua compose chinh dat `CUSTOMPBX_OTEL_ENDPOINT=tempo:4317` (exporter OTLP/**gRPC**, dang `host:port`, khong co duong dan;
   `http://tempo:4317` van duoc hieu la gRPC khong TLS, `https://` = TLS). Roi `docker compose up -d custompbx` (recreate).
   Co the dung bien chuan `OTEL_EXPORTER_OTLP_ENDPOINT`. De trong = tat han, khong ton tai nguyen.
3. Lay mau: `OTEL_TRACES_SAMPLER_ARG` (mac dinh `1.0`; vd `0.2` = 20% cuoc goi; quyet dinh theo trace id nen cung 1 cuoc goi luon
   duoc giu hoac bo tron ven).  Dat trong `.env` cua compose chinh (da duoc truyen vao container).

**Tim trace theo call_uuid:** Grafana > Explore > datasource **Tempo** > tab *TraceQL*:
```
{ span.call_uuid = "<call_uuid>" }
```
(call_uuid = `variable_linked_id`/`call_uuid` cua cuoc goi, cung gia tri voi truong `call_uuid` trong log; webhook: `pbx_call_id`).
Trong trace, nut *Logs for this span* nhay sang Loki (loc theo trace id). Nguoc lai, o Explore > Loki, dong log custompbx co
`trace_id` (log trong ngu canh span, vd "call webhook delivered", "CDR persistence failed") hien nut **Xem trace**.

**RAM/dia:** Tempo `mem_limit 192m` (thuc te ~80-150MB luc nhan it trace), `cpus 0.5`, retention 72h tren volume `tempo-data`
(vai chuc MB/ngay voi tai PBX nho). custompbx them ~vai MB (batch exporter, queue 1024 span). Khi tat tracing custompbx khong them gi.
Neu RAM VPS cang: giam `OTEL_TRACES_SAMPLER_ARG` hoac dung `docker compose stop tempo` (custompbx se bo qua loi export, khong anh huong cuoc goi).
