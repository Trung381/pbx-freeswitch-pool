#!/bin/sh
# Xoa goi HEP (hep_packets) cu hon N ngay (mac dinh 14). Chay hang ngay bang cron:
#   17 3 * * * /opt/pbx-freeswitch-pool/deployment/observability/tools/hep-retention.sh >> /var/log/hep-retention.log 2>&1
DAYS="${HEP_RETENTION_DAYS:-14}"
case "$DAYS" in ''|*[!0-9]*) echo "HEP_RETENTION_DAYS khong hop le" >&2; exit 1;; esac
docker exec postgres-host psql -U custompbx -d custompbx -tAc \
  "WITH d AS (DELETE FROM hep_packets WHERE hep_timestamp < now() - interval '$DAYS days' RETURNING 1) SELECT now() || ' deleted ' || count(*) FROM d"
