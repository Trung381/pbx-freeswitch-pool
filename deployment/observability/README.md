# Observability cho PBX-FREESWITCH-POOL

Stack gon cho VPS RAM chat: **Grafana + Loki + Prometheus + Grafana Alloy + node_exporter + cAdvisor**.
Moi du an 1 Grafana rieng. Khong co trace (khong Tempo/Mimir), alert dung Grafana Alerting (khong Alertmanager).
Retention: log 5 ngay (`loki/loki.yml`), metrics 10 ngay / 1GB (`.env`).

## Thanh phan
| File | Muc dich |
|---|---|
| `docker-compose.observability.yml` | Stack giam sat (project `pbx-observability`), moi container co `mem_limit` + `cpus` |
| `docker-compose.observability-fs-log.yml` | Override cho compose chinh: named volume log FreeSWITCH (thay volume an danh), xoay log 20MB x3 |
| `fs_conf/logfile.conf.xml` | Cau hinh mod_logfile (xoay log, bo debug, prefix UUID) |
| `alloy/config.alloy` | Thu log stdout 4 container + file log FreeSWITCH, parse `level`, `call_uuid`, che gia tri nhay cam |
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

## Van hanh
- Dung luong: `docker system df`; Loki/Prometheus tu xoa theo retention.
- Doi thoi gian giu log: sua `retention_period` trong `loki/loki.yml` roi `docker compose up -d loki`.
- Rollback FreeSWITCH log: bo `-f ...fs-log.yml` va `up -d --no-deps freeswitch` (log cu o container layer se mat khi recreate; backup xem bao cao trien khai).
