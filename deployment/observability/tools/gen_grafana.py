#!/usr/bin/env python3
"""Sinh dashboards + alert rules (Grafana provisioning) cho observability stack.

Chay: python3 tools/gen_grafana.py   (tu thu muc deployment/observability)
Dau ra: grafana/dashboards/*.json, grafana/provisioning/alerting/*.yml
"""
import json
import os

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DASH = os.path.join(ROOT, "grafana", "dashboards")
ALERT = os.path.join(ROOT, "grafana", "provisioning", "alerting")

PROM = {"type": "prometheus", "uid": "prometheus"}
LOKI = {"type": "loki", "uid": "loki"}
PG = {"type": "grafana-postgresql-datasource", "uid": "pbx-postgres"}

# Mau co dinh (de phan biet tot, an toan cho mu mau): xanh duong / cam / xanh la / do / tim.
C_OK, C_WARN, C_BAD, C_INFO = "#2f9e44", "#f08c00", "#e03131", "#1c7ed6"

_id = [0]


def nid():
    _id[0] += 1
    return _id[0]


def target(ds, expr=None, legend=None, sql=None, fmt=None, ref="A", instant=False):
    t = {"refId": ref, "datasource": ds}
    if ds is PG:
        t.update({"rawSql": sql, "format": fmt or "time_series", "rawQuery": True, "editorMode": "code"})
    elif ds is LOKI:
        t.update({"expr": expr, "queryType": "instant" if instant else "range"})
        if legend:
            t["legendFormat"] = legend
    else:
        t.update({"expr": expr, "instant": instant, "range": not instant})
        if legend:
            t["legendFormat"] = legend
    return t


def panel(kind, title, targets, x, y, w, h, unit=None, desc=None, thresholds=None,
          mappings=None, no_value=None, options=None, custom=None, overrides=None, decimals=None, min_=None, max_=None):
    ds = targets[0]["datasource"]
    fc = {"defaults": {}, "overrides": overrides or []}
    d = fc["defaults"]
    if unit:
        d["unit"] = unit
    if thresholds:
        d["thresholds"] = {"mode": "absolute", "steps": thresholds}
    if mappings:
        d["mappings"] = mappings
    if no_value:
        d["noValue"] = no_value
    if decimals is not None:
        d["decimals"] = decimals
    if min_ is not None:
        d["min"] = min_
    if max_ is not None:
        d["max"] = max_
    if custom:
        d["custom"] = custom
    p = {"id": nid(), "type": kind, "title": title, "datasource": ds, "targets": targets,
         "gridPos": {"x": x, "y": y, "w": w, "h": h}, "fieldConfig": fc, "options": options or {}}
    if desc:
        p["description"] = desc
    return p


def steps(*pairs):
    """steps((None,'green'),(80,'orange'))"""
    return [{"color": c, "value": v} for v, c in pairs]


def ts(title, targets, x, y, w=12, h=8, unit=None, **kw):
    custom = {"lineWidth": 1, "fillOpacity": 12, "showPoints": "never", "spanNulls": True}
    custom.update(kw.pop("custom", {}))
    opts = {"legend": {"displayMode": "list", "placement": "bottom", "calcs": []}, "tooltip": {"mode": "multi", "sort": "desc"}}
    return panel("timeseries", title, targets, x, y, w, h, unit=unit, custom=custom, options=opts, **kw)


def stat(title, targets, x, y, w=4, h=4, unit=None, **kw):
    opts = {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
            "colorMode": "value", "graphMode": "area", "textMode": "auto"}
    return panel("stat", title, targets, x, y, w, h, unit=unit, options=opts, **kw)


def table(title, targets, x, y, w=12, h=8, **kw):
    return panel("table", title, targets, x, y, w, h, options={"showHeader": True}, **kw)


def row(title, y):
    return {"id": nid(), "type": "row", "title": title, "collapsed": False, "gridPos": {"x": 0, "y": y, "w": 24, "h": 1}, "panels": []}


def dashboard(uid, title, panels, tags, templating=None, refresh="30s", time_from="now-6h"):
    return {"uid": uid, "title": title, "tags": ["pbx"] + tags, "timezone": "browser", "schemaVersion": 39,
            "version": 1, "editable": False, "graphTooltip": 1, "refresh": refresh,
            "time": {"from": time_from, "to": "now"}, "templating": {"list": templating or []},
            "annotations": {"list": []}, "panels": panels}


def write(name, d):
    with open(os.path.join(DASH, name), "w") as f:
        json.dump(d, f, indent=1, ensure_ascii=False)
        f.write("\n")


CN = 'name=~"freeswitch-host|custompbx-host|postgres-host|pbx-freeswitch-pool-minio-.*|pbx-obs.*|.*grafana.*|.*loki.*|.*prometheus.*|.*alloy.*|.*cadvisor.*|.*node-exporter.*"'

# ---------------------------------------------------------------- (a) Ha tang
def infra():
    _id[0] = 0
    P = []
    P.append(row("Host", 0))
    P.append(stat("CPU dang dung", [target(PROM, '100 - avg(rate(node_cpu_seconds_total{mode="idle"}[$__rate_interval])) * 100', instant=False)],
                  0, 1, unit="percent", thresholds=steps((None, C_OK), (70, C_WARN), (90, C_BAD)), decimals=0))
    P.append(stat("RAM kha dung", [target(PROM, '100 * node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes')],
                  4, 1, unit="percent", thresholds=steps((None, C_BAD), (10, C_WARN), (20, C_OK)), decimals=0,
                  desc="MemAvailable / MemTotal. Canh bao khi < 10%."))
    P.append(stat("Swap dang dung", [target(PROM, 'node_memory_SwapTotal_bytes - node_memory_SwapFree_bytes')],
                  8, 1, unit="bytes", thresholds=steps((None, C_OK), (2e9, C_WARN), (4e9, C_BAD))))
    P.append(stat("O dia / da dung", [target(PROM, '100 * (1 - node_filesystem_avail_bytes{mountpoint="/",fstype!~"tmpfs|overlay"} / node_filesystem_size_bytes{mountpoint="/",fstype!~"tmpfs|overlay"})')],
                  12, 1, unit="percent", thresholds=steps((None, C_OK), (80, C_WARN), (90, C_BAD)), decimals=0,
                  desc="Warning >= 80%, critical >= 90%."))
    P.append(stat("Load 1m", [target(PROM, 'node_load1')], 16, 1, thresholds=steps((None, C_OK), (2, C_WARN), (4, C_BAD)), decimals=2,
                  desc="VPS co 2 vCPU: load > 2 la bao hoa."))
    P.append(stat("Uptime", [target(PROM, 'time() - node_boot_time_seconds')], 20, 1, unit="s", thresholds=steps((None, C_INFO))))
    P.append(ts("CPU theo mode", [target(PROM, 'sum by (mode) (rate(node_cpu_seconds_total{mode!="idle"}[$__rate_interval])) * 100', "{{mode}}")],
                0, 5, unit="percent", custom={"stacking": {"mode": "normal"}}))
    P.append(ts("Bo nho", [target(PROM, 'node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes', "da dung"),
                          target(PROM, 'node_memory_MemAvailable_bytes', "kha dung", ref="B"),
                          target(PROM, 'node_memory_SwapTotal_bytes - node_memory_SwapFree_bytes', "swap", ref="C")],
                12, 5, unit="bytes"))
    P.append(ts("Dung luong o dia /", [target(PROM, 'node_filesystem_size_bytes{mountpoint="/",fstype!~"tmpfs|overlay"} - node_filesystem_avail_bytes{mountpoint="/",fstype!~"tmpfs|overlay"}', "da dung")],
                0, 13, unit="bytes"))
    P.append(ts("Disk IO", [target(PROM, 'sum(rate(node_disk_read_bytes_total[$__rate_interval]))', "doc"),
                           target(PROM, 'sum(rate(node_disk_written_bytes_total[$__rate_interval]))', "ghi", ref="B")],
                12, 13, unit="Bps"))
    P.append(ts("Mang (host)", [target(PROM, 'sum(rate(node_network_receive_bytes_total{device!~"lo|veth.*|br-.*|docker.*"}[$__rate_interval]))', "rx"),
                               target(PROM, 'sum(rate(node_network_transmit_bytes_total{device!~"lo|veth.*|br-.*|docker.*"}[$__rate_interval]))', "tx", ref="B")],
                0, 21, unit="Bps"))
    P.append(ts("Pressure (PSI) bo nho/CPU", [target(PROM, 'rate(node_pressure_memory_waiting_seconds_total[$__rate_interval])', "memory"),
                                               target(PROM, 'rate(node_pressure_cpu_waiting_seconds_total[$__rate_interval])', "cpu", ref="B"),
                                               target(PROM, 'rate(node_pressure_io_waiting_seconds_total[$__rate_interval])', "io", ref="C")],
                12, 21, unit="percentunit"))
    P.append(row("Container", 29))
    P.append(ts("CPU theo container", [target(PROM, f'sum by (name) (rate(container_cpu_usage_seconds_total{{{CN}}}[$__rate_interval])) * 100', "{{name}}")],
                0, 30, unit="percent"))
    P.append(ts("RAM (working set) theo container", [target(PROM, f'container_memory_working_set_bytes{{{CN}}}', "{{name}}")],
                12, 30, unit="bytes"))
    P.append(ts("Mang theo container (rx)", [target(PROM, f'sum by (name) (rate(container_network_receive_bytes_total{{{CN}}}[$__rate_interval]))', "{{name}}")],
                0, 38, unit="Bps"))
    P.append(table("Container: uptime va so lan restart (1h)", [
        target(PROM, f'time() - container_start_time_seconds{{{CN}}}', instant=True, ref="A"),
        target(PROM, f'changes(container_start_time_seconds{{{CN}}}[1h])', instant=True, ref="B")],
        12, 38, overrides=[], desc="Cot Value #A = uptime (giay), Value #B = so lan khoi dong lai trong 1 gio."))
    P[-1]["transformations"] = [{"id": "joinByField", "options": {"byField": "name", "mode": "outer"}},
                                {"id": "organize", "options": {"excludeByName": {"Time": True, "Time 1": True, "Time 2": True},
                                                               "renameByName": {"Value #A": "uptime (s)", "Value #B": "restart/1h"}}}]
    return dashboard("pbx-infra", "PBX - Ha tang (host + container)", P, ["infra"])


# ---------------------------------------------------------------- (b) Logs
def logs():
    _id[0] = 0
    tpl = [
        {"name": "container", "label": "Container", "type": "query", "datasource": LOKI, "query": {"label": "container", "type": 1, "refId": "c"},
         "includeAll": True, "allValue": ".+", "multi": True, "current": {"text": "All", "value": "$__all"}, "refresh": 2},
        {"name": "level", "label": "Level", "type": "custom", "includeAll": True, "allValue": ".*", "multi": True,
         "query": "ERROR,WARNING,ERR,CRIT,ALERT,NOTICE,INFO", "current": {"text": "All", "value": "$__all"}},
        {"name": "search", "label": "Tim kiem (regex)", "type": "textbox", "query": "", "current": {"text": "", "value": ""}},
    ]
    sel = '{container=~"$container", level=~"$level"} |~ "(?i)$search"'
    P = []
    P.append(ts("Luong log theo container", [target(LOKI, 'sum by (container) (count_over_time({container=~"$container"}[$__interval]))', "{{container}}")],
                0, 0, w=12, h=6, custom={"drawStyle": "bars", "fillOpacity": 60, "stacking": {"mode": "normal"}}))
    P.append(ts("Loi / canh bao theo container", [target(LOKI, 'sum by (container, level) (count_over_time({container=~"$container", level=~"ERROR|ERR|CRIT|ALERT|WARNING"}[$__interval]))', "{{container}} {{level}}")],
                12, 0, w=12, h=6, custom={"drawStyle": "bars", "fillOpacity": 60, "stacking": {"mode": "normal"}}))
    lp = panel("logs", "Log", [target(LOKI, sel)], 0, 6, 24, 14,
               options={"showTime": True, "wrapLogMessage": True, "enableLogDetails": True, "sortOrder": "Descending", "dedupStrategy": "none"})
    P.append(lp)
    P.append(panel("logs", "FreeSWITCH (file) - chi WARNING/ERR/CRIT", [target(LOKI, '{job="freeswitch", level=~"WARNING|ERR|CRIT|ALERT"}')],
                   0, 20, 24, 10, options={"showTime": True, "wrapLogMessage": True, "sortOrder": "Descending"}))
    P.append(panel("logs", "custompbx - loi", [target(LOKI, '{container="custompbx-host", level="ERROR"}')],
                   0, 30, 12, 10, options={"showTime": True, "wrapLogMessage": True, "sortOrder": "Descending"}))
    P.append(panel("logs", "postgres - loi / canh bao", [target(LOKI, '{container="postgres-host"} |~ "(?i)error|fatal|warning|panic"')],
                   12, 30, 12, 10, options={"showTime": True, "wrapLogMessage": True, "sortOrder": "Descending"}))
    return dashboard("pbx-logs", "PBX - Log explorer", P, ["logs"], templating=tpl, time_from="now-3h")


# ---------------------------------------------------------------- (c) CDR/HEP
def cdr():
    _id[0] = 0
    FAIL = "hangup_cause NOT IN ('NORMAL_CLEARING','ORIGINATOR_CANCEL','USER_BUSY','NO_ANSWER','NO_USER_RESPONSE','CALL_REJECTED','NORMAL_UNSPECIFIED')"
    P = []
    P.append(stat("Tong so cuoc goi", [target(PG, sql="SELECT count(*) AS calls FROM cdr WHERE $__timeFilter(start_stamp)", fmt="table")], 0, 0, w=4,
                  thresholds=steps((None, C_INFO))))
    P.append(stat("Cuoc goi co tra loi", [target(PG, sql="SELECT count(*) AS answered FROM cdr WHERE $__timeFilter(start_stamp) AND billsec > 0", fmt="table")], 4, 0, w=4,
                  thresholds=steps((None, C_OK))))
    P.append(stat("Ty le loi", [target(PG, sql=f"SELECT COALESCE(100.0 * count(*) FILTER (WHERE {FAIL}) / NULLIF(count(*),0), 0) AS fail_pct FROM cdr WHERE $__timeFilter(start_stamp)", fmt="table")],
                  8, 0, w=4, unit="percent", decimals=1, thresholds=steps((None, C_OK), (20, C_WARN), (50, C_BAD)),
                  desc="Loi = hangup_cause khong thuoc nhom ket thuc binh thuong (NORMAL_CLEARING, ORIGINATOR_CANCEL, USER_BUSY, NO_ANSWER, NO_USER_RESPONSE, CALL_REJECTED, NORMAL_UNSPECIFIED)."))
    P.append(stat("Thoi luong TB (billsec)", [target(PG, sql="SELECT COALESCE(avg(billsec) FILTER (WHERE billsec > 0), 0) AS avg_billsec FROM cdr WHERE $__timeFilter(start_stamp)", fmt="table")],
                  12, 0, w=4, unit="s", decimals=0, thresholds=steps((None, C_INFO))))
    P.append(stat("Tong thoi gian noi may", [target(PG, sql="SELECT COALESCE(sum(billsec),0) AS total_billsec FROM cdr WHERE $__timeFilter(start_stamp)", fmt="table")],
                  16, 0, w=4, unit="s", thresholds=steps((None, C_INFO))))
    P.append(stat("Cuoc goi moi nhat", [target(PG, sql="SELECT max(start_stamp) AS last_call FROM cdr", fmt="table")],
                  20, 0, w=4, unit="dateTimeFromNow", thresholds=steps((None, C_INFO))))
    P.append(ts("So cuoc goi theo gio", [
        target(PG, sql="SELECT $__timeGroupAlias(start_stamp,'1h'), count(*) FILTER (WHERE billsec > 0) AS \"co tra loi\", count(*) FILTER (WHERE billsec = 0) AS \"khong tra loi\" FROM cdr WHERE $__timeFilter(start_stamp) GROUP BY 1 ORDER BY 1")],
        0, 4, w=16, h=8, custom={"drawStyle": "bars", "fillOpacity": 70, "stacking": {"mode": "normal"}},
        overrides=[{"matcher": {"id": "byName", "options": "co tra loi"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": C_OK}}]},
                   {"matcher": {"id": "byName", "options": "khong tra loi"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": C_WARN}}]}]))
    P.append(panel("piechart", "Theo hangup cause", [target(PG, sql="SELECT hangup_cause AS metric, count(*) AS value FROM cdr WHERE $__timeFilter(start_stamp) GROUP BY 1 ORDER BY 2 DESC", fmt="table")],
                   16, 4, 8, 8, options={"legend": {"displayMode": "table", "placement": "right", "values": ["value", "percent"]}, "pieType": "donut",
                                         "reduceOptions": {"calcs": ["lastNotNull"], "fields": "/^value$/", "values": True}}))
    P.append(ts("Ty le loi theo gio (%)", [target(PG, sql=f"SELECT $__timeGroupAlias(start_stamp,'1h'), 100.0 * count(*) FILTER (WHERE {FAIL}) / NULLIF(count(*),0) AS \"ty le loi\" FROM cdr WHERE $__timeFilter(start_stamp) GROUP BY 1 ORDER BY 1")],
                0, 12, w=12, unit="percent", min_=0, max_=100))
    P.append(ts("Thoi luong TB theo gio (giay)", [target(PG, sql="SELECT $__timeGroupAlias(start_stamp,'1h'), avg(billsec) FILTER (WHERE billsec > 0) AS \"billsec TB\", avg(duration) AS \"duration TB\" FROM cdr WHERE $__timeFilter(start_stamp) GROUP BY 1 ORDER BY 1")],
                12, 12, w=12, unit="s"))
    P.append(table("Hangup cause: so luong, ty le, thoi luong TB", [target(PG, fmt="table", sql="SELECT hangup_cause, count(*) AS calls, round(100.0*count(*)/sum(count(*)) OVER (),1) AS pct, round(avg(billsec)) AS avg_billsec FROM cdr WHERE $__timeFilter(start_stamp) GROUP BY 1 ORDER BY 2 DESC")],
                   0, 20, w=12, h=8))
    P.append(table("Top so goi di (caller)", [target(PG, fmt="table", sql="SELECT caller_id_number, count(*) AS calls, sum(billsec) AS billsec FROM cdr WHERE $__timeFilter(start_stamp) GROUP BY 1 ORDER BY 2 DESC LIMIT 15")],
                   12, 20, w=12, h=8))
    P.append(table("100 cuoc goi gan nhat", [target(PG, fmt="table", sql="SELECT start_stamp, caller_id_number, destination_number, billsec, hangup_cause, sip_hangup_disposition, read_codec FROM cdr WHERE $__timeFilter(start_stamp) ORDER BY start_stamp DESC LIMIT 100")],
                   0, 28, w=24, h=10))
    P.append(row("HEP / SIP (hep_packets) - trong neu chua bat HEP capture", 38))
    P.append(ts("SIP message theo gio", [target(PG, sql="SELECT $__timeGroupAlias(hep_timestamp,'1h'), count(*) AS \"SIP messages\" FROM hep_packets WHERE $__timeFilter(hep_timestamp) GROUP BY 1 ORDER BY 1")],
                0, 39, w=12, no_value="Chua co du lieu HEP", custom={"drawStyle": "bars", "fillOpacity": 70}))
    P.append(table("SIP response / method", [target(PG, fmt="table", sql="SELECT COALESCE(NULLIF(sip_first_resp,''), sip_first_method) AS sip, sip_first_resp_text, count(*) AS n FROM hep_packets WHERE $__timeFilter(hep_timestamp) GROUP BY 1,2 ORDER BY 3 DESC LIMIT 25")],
                   12, 39, w=12, no_value="Chua co du lieu HEP"))
    return dashboard("pbx-cdr", "PBX - Bao cao cuoc goi (CDR/HEP)", P, ["cdr", "report"], refresh="5m", time_from="now-7d")


# ---------------------------------------------------------------- (d) PBX health
def health():
    _id[0] = 0
    NV = "Chua deploy /metrics"
    onoff = [{"type": "value", "options": {"0": {"text": "MAT KET NOI", "color": C_BAD}, "1": {"text": "OK", "color": C_OK}}}]
    P = []
    P.append(stat("custompbx /metrics", [target(PROM, 'up{job="custompbx"}')], 0, 0, mappings=[{"type": "value", "options": {"0": {"text": "DOWN", "color": C_BAD}, "1": {"text": "UP", "color": C_OK}}}],
                  no_value=NV, thresholds=steps((None, C_BAD), (1, C_OK)),
                  desc="DOWN cho den khi custompbx duoc build/deploy lai voi endpoint /metrics (cong 9102, noi bo docker)."))
    P.append(stat("ESL -> FreeSWITCH", [target(PROM, 'pbx_esl_connected')], 4, 0, mappings=onoff, no_value=NV, thresholds=steps((None, C_BAD), (1, C_OK))))
    P.append(stat("Database", [target(PROM, 'pbx_database_connected')], 8, 0, mappings=onoff, no_value=NV, thresholds=steps((None, C_BAD), (1, C_OK))))
    P.append(stat("Cuoc goi dang dien ra", [target(PROM, 'pbx_calls_active')], 12, 0, no_value=NV, thresholds=steps((None, C_INFO))))
    P.append(stat("Channel dang theo doi", [target(PROM, 'pbx_channels')], 16, 0, no_value=NV, thresholds=steps((None, C_INFO))))
    P.append(stat("HEP packets/s", [target(PROM, 'sum(rate(pbx_hep_packets_total{result="received"}[$__rate_interval]))')], 20, 0, unit="pps", decimals=1, no_value=NV, thresholds=steps((None, C_INFO))))
    P.append(ts("Cuoc goi dang dien ra", [target(PROM, 'pbx_calls_active', "answered"), target(PROM, 'pbx_channels', "channels", ref="B")], 0, 4, no_value=NV))
    P.append(ts("Cuoc goi ket thuc theo hangup cause (/phut)", [target(PROM, 'sum by (hangup_cause) (increase(pbx_calls_total[$__rate_interval])) * 60 / $__interval_ms * 1000', "{{hangup_cause}}")],
                12, 4, no_value=NV, custom={"drawStyle": "bars", "fillOpacity": 60, "stacking": {"mode": "normal"}}))
    P.append(table("Trang thai gateway/trunk", [target(PROM, 'pbx_gateway_state', instant=True, fmt=None)], 0, 12, w=12, no_value="Chua co su kien gateway_state (hoac chua deploy /metrics)",
                   desc="Trang thai lay tu su kien sofia::gateway_state ke tu luc custompbx khoi dong. REGED = da dang ky."))
    P[-1]["transformations"] = [{"id": "organize", "options": {"excludeByName": {"Time": True, "Value": True, "__name__": True, "instance": True, "job": True}}}]
    P.append(ts("ESL reconnect attempts", [target(PROM, 'increase(pbx_esl_reconnect_attempts_total[$__rate_interval])', "reconnect")], 12, 12, no_value=NV))
    P.append(ts("HEP packets/s", [target(PROM, 'sum by (result) (rate(pbx_hep_packets_total[$__rate_interval]))', "{{result}}")], 0, 20, unit="pps", no_value=NV))
    P.append(ts("custompbx: RAM (RSS) / goroutines", [target(PROM, 'process_resident_memory_bytes{job="custompbx"}', "RSS"),
                                                       target(PROM, 'go_goroutines{job="custompbx"}', "goroutines", ref="B")], 12, 20, no_value=NV,
                overrides=[{"matcher": {"id": "byName", "options": "RSS"}, "properties": [{"id": "unit", "value": "bytes"}]}]))
    P.append(panel("logs", "custompbx - log loi", [target(LOKI, '{container="custompbx-host", level="ERROR"}')], 0, 28, 24, 8,
                   options={"showTime": True, "wrapLogMessage": True, "sortOrder": "Descending"}))
    return dashboard("pbx-health", "PBX - Suc khoe he thong", P, ["health"])


# ---------------------------------------------------------------- Alerts
def rule(uid, title, expr, op, thr, for_, sev, summary, ds=PROM, nodata="OK", err="Error", lookback=600, fn="last", desc=None):
    kind = "loki" if ds is LOKI else "prom"
    q = {"datasourceUid": ds["uid"], "refId": "A", "relativeTimeRange": {"from": lookback, "to": 0},
         "model": {"refId": "A", "expr": expr, "intervalMs": 1000, "maxDataPoints": 43200, "datasource": ds,
                   **({"queryType": "instant", "editorMode": "code"} if kind == "loki" else {"instant": True, "range": False, "editorMode": "code"})}}
    red = {"datasourceUid": "__expr__", "refId": "B", "relativeTimeRange": {"from": lookback, "to": 0},
           "model": {"refId": "B", "type": "reduce", "expression": "A", "reducer": fn, "settings": {"mode": "dropNN"}, "datasource": {"type": "__expr__", "uid": "__expr__"}}}
    thr_ = {"datasourceUid": "__expr__", "refId": "C", "relativeTimeRange": {"from": lookback, "to": 0},
            "model": {"refId": "C", "type": "threshold", "expression": "B", "datasource": {"type": "__expr__", "uid": "__expr__"},
                      "conditions": [{"evaluator": {"type": op, "params": [thr]}, "operator": {"type": "and"}, "query": {"params": ["C"]}, "reducer": {"type": "last", "params": []}, "type": "query"}]}}
    return {"uid": uid, "title": title, "condition": "C", "data": [q, red, thr_], "noDataState": nodata, "execErrState": err,
            "for": for_, "annotations": {"summary": summary, **({"description": desc} if desc else {})}, "labels": {"severity": sev, "project": "pbx"}}


PBX_CONT = "freeswitch-host|custompbx-host|postgres-host|pbx-freeswitch-pool-minio-[0-9]+"


def alerts():
    host = [
        rule("pbx-disk-warn", "O dia / >= 80%", '100 * (1 - node_filesystem_avail_bytes{mountpoint="/",fstype!~"tmpfs|overlay"} / node_filesystem_size_bytes{mountpoint="/",fstype!~"tmpfs|overlay"})',
             "gt", 80, "10m", "warning", "O dia / dung {{ $values.B }}% (>= 80%). Don build cache/log/image cu."),
        rule("pbx-disk-crit", "O dia / >= 90%", '100 * (1 - node_filesystem_avail_bytes{mountpoint="/",fstype!~"tmpfs|overlay"} / node_filesystem_size_bytes{mountpoint="/",fstype!~"tmpfs|overlay"})',
             "gt", 90, "5m", "critical", "O dia / dung {{ $values.B }}% (>= 90%) - sap day, Postgres/Loki co the ngung ghi."),
        rule("pbx-ram-low", "RAM kha dung < 10%", '100 * node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes',
             "lt", 10, "5m", "warning", "RAM kha dung chi con {{ $values.B }}%."),
        rule("pbx-infra-target-down", "Target giam sat ha tang DOWN", 'min(up{job=~"node|cadvisor|loki|prometheus|alloy"})',
             "lt", 1, "5m", "warning", "Mot target giam sat (node/cadvisor/loki/alloy) khong scrape duoc.", nodata="Alerting"),
    ]
    cont = [
        rule("pbx-container-restart", "Container PBX restart", f'sum(changes(container_start_time_seconds{{name=~"{PBX_CONT}"}}[15m]))',
             "gt", 0, "0s", "warning", "Co container PBX vua khoi dong lai trong 15 phut gan day.", lookback=900),
    ]
    for uid, nm, pat in [("freeswitch", "FreeSWITCH", "freeswitch-host"), ("custompbx", "custompbx", "custompbx-host"), ("postgres", "Postgres", "postgres-host")]:
        cont.append(rule(f"pbx-{uid}-down", f"{nm} down", f'count(time() - container_last_seen{{name="{pat}"}} < 120) or vector(0)',
                         "lt", 1, "2m", "critical", f"Container {pat} khong chay.", nodata="Alerting", lookback=300))
    app = [
        rule("pbx-esl-down", "ESL mat ket noi FreeSWITCH", 'pbx_esl_connected', "lt", 1, "2m", "critical",
             "custompbx mat ket noi ESL toi FreeSWITCH.", nodata="OK", desc="Chi hoat dong sau khi custompbx duoc deploy voi /metrics."),
        rule("pbx-metrics-target-down", "custompbx /metrics DOWN (da tung UP)", 'up{job="custompbx"} == 0 and max_over_time(up{job="custompbx"}[7d]) == 1',
             "gt", 0, "10m", "warning", "Prometheus khong scrape duoc custompbx:9102 (truoc do da UP). Khong canh bao khi chua tung deploy.", nodata="OK"),
        rule("pbx-call-failures", "Cuoc goi loi tang dot bien",
             'sum(increase(pbx_calls_total{hangup_cause=~"NO_ROUTE_DESTINATION|BEARERCAPABILITY_NOTAUTH|INCOMPATIBLE_DESTINATION|DESTINATION_OUT_OF_ORDER|NETWORK_OUT_OF_ORDER|NORMAL_TEMPORARY_FAILURE|SWITCH_CONGESTION|RECOVERY_ON_TIMER_EXPIRE|MEDIA_TIMEOUT|GATEWAY_DOWN|SERVICE_UNAVAILABLE|CHAN_NOT_IMPLEMENTED|MANDATORY_IE_MISSING|PROTOCOL_ERROR"}[15m])) or vector(0)',
             "gt", 20, "0s", "warning", "{{ $values.B }} cuoc goi loi trong 15 phut (> 20).", lookback=900,
             desc="Neu NO_ROUTE_DESTINATION lien tuc: co the dang bi quet SIP (SIP scanning) hoac sai dialplan."),
        rule("pbx-gateway-unreg", "Trunk/gateway mat dang ky", 'count(pbx_gateway_state{state=~"FAIL_WAIT|FAILED|UNREGED|UNREGISTER|TRYING|DOWN"}) or vector(0)',
             "gt", 0, "5m", "critical", "Co gateway FreeSWITCH khong o trang thai REGED (FAIL_WAIT/FAILED/UNREGED/TRYING).", nodata="OK"),
    ]
    logs = [
        rule("pbx-webhook-fail", "Call webhook gui loi", 'sum(count_over_time({container="custompbx-host"} |= "Call webhook delivery failed" [10m]))',
             "gt", 5, "0s", "warning", "{{ $values.B }} lan gui call webhook that bai trong 10 phut.", ds=LOKI, lookback=600,
             desc="Dem tu log (dispatcher.go chua co metric rieng)."),
        rule("pbx-fs-errors", "FreeSWITCH nhieu log ERR/CRIT", 'sum(count_over_time({job="freeswitch", level=~"ERR|CRIT|ALERT"}[10m]))',
             "gt", 30, "0s", "warning", "{{ $values.B }} dong log ERR/CRIT/ALERT cua FreeSWITCH trong 10 phut.", ds=LOKI, lookback=600),
    ]
    groups = [("Host", host), ("Container", cont), ("PBX app", app), ("Log", logs)]
    out = ["apiVersion: 1", "groups:"]
    # YAML == JSON superset: dung json.dumps cho moi rule de tranh loi indent.
    for name, rules in groups:
        out.append(f"  - orgId: 1\n    name: {json.dumps(name)}\n    folder: PBX Alerts\n    interval: 1m\n    rules:")
        for r in rules:
            out.append("      - " + json.dumps(r, ensure_ascii=False))
    with open(os.path.join(ALERT, "rules.yml"), "w") as f:
        f.write("# SINH TU DONG boi tools/gen_grafana.py - dung sua tay.\n" + "\n".join(out) + "\n")

    with open(os.path.join(ALERT, "contactpoints.yml"), "w") as f:
        f.write("""apiVersion: 1
# Telegram: TAM HOLD. Dien TELEGRAM_BOT_TOKEN / TELEGRAM_CHAT_ID trong .env roi `docker compose up -d grafana`.
# Khi de trong, compose dung token/chat placeholder: Grafana van khoi dong, alert hien trong UI, gui Telegram se loi (ghi log) cho den khi co token that.
contactPoints:
  - orgId: 1
    name: telegram-pbx
    receivers:
      - uid: telegram-pbx
        type: telegram
        disableResolveMessage: false
        settings:
          bottoken: $__env{TELEGRAM_BOT_TOKEN}
          chatid: $__env{TELEGRAM_CHAT_ID}
          parse_mode: None
""")
    with open(os.path.join(ALERT, "policies.yml"), "w") as f:
        f.write("""apiVersion: 1
policies:
  - orgId: 1
    receiver: telegram-pbx
    group_by: [alertname]
    group_wait: 30s
    group_interval: 5m
    repeat_interval: 4h
    routes:
      - receiver: telegram-pbx
        object_matchers:
          - [severity, "=", critical]
        repeat_interval: 1h
""")


if __name__ == "__main__":
    os.makedirs(DASH, exist_ok=True)
    os.makedirs(ALERT, exist_ok=True)
    write("pbx-infra.json", infra())
    write("pbx-logs.json", logs())
    write("pbx-cdr.json", cdr())
    write("pbx-health.json", health())
    alerts()
    print("ok")
