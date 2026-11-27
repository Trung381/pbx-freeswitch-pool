#!/usr/bin/env python3
"""Sidecar that turns Postgres NOTIFY `webhook` payloads into signed POSTs.

Runs one asyncio loop:
  * LISTEN on `webhook` for live CDR inserts;
  * every tick, sweep `webhook_delivery_log` for rows whose `next_retry_at` has
    come due;
  * for each event, resolve the tenant's webhook_url / webhook_secret, sign the
    body with HMAC-SHA256 and POST it. A row in `webhook_delivery_log` records
    attempt, status code and response (truncated).

The retry backoff is deterministic (10s, 30s, 2m, 10m, 1h, 6h, 24h) — after the
last attempt the row is left with delivered_at IS NULL and next_retry_at NULL,
which an operator can re-queue by setting next_retry_at = NOW().
"""
from __future__ import annotations

import asyncio
import hashlib
import hmac
import json
import logging
import os
import signal
from datetime import datetime, timedelta, timezone

import asyncpg
import httpx

DSN = os.environ["DATABASE_URL"]
HTTP_TIMEOUT = float(os.environ.get("HTTP_TIMEOUT", "10"))
MAX_ATTEMPTS = int(os.environ.get("MAX_ATTEMPTS", "7"))
SWEEP_INTERVAL = float(os.environ.get("SWEEP_INTERVAL", "15"))
BACKOFF = [10, 30, 120, 600, 3600, 21600, 86400]  # 7 steps, matches MAX_ATTEMPTS

logging.basicConfig(
    level=os.environ.get("LOG_LEVEL", "INFO"),
    format="%(asctime)s %(levelname)s %(message)s",
)
log = logging.getLogger("webhook-dispatcher")


def sign(secret: str, body: bytes) -> str:
    return hmac.new(secret.encode("utf-8"), body, hashlib.sha256).hexdigest()


def next_retry(attempt: int) -> datetime | None:
    if attempt >= MAX_ATTEMPTS:
        return None
    delay = BACKOFF[min(attempt, len(BACKOFF) - 1)]
    return datetime.now(timezone.utc) + timedelta(seconds=delay)


async def deliver_row(pool: asyncpg.Pool, client: httpx.AsyncClient, row: asyncpg.Record) -> None:
    body = json.dumps(row["payload"], separators=(",", ":"), default=str).encode("utf-8")
    secret = row["webhook_secret"] or ""
    headers = {
        "Content-Type": "application/json",
        "X-Pbx-Event": row["event"],
        "X-Pbx-Tenant": row["tenant_slug"],
        "X-Pbx-Delivery": str(row["id"]),
    }
    if secret:
        headers["X-Pbx-Signature"] = f"sha256={sign(secret, body)}"

    status_code = None
    response = ""
    error = None
    try:
        resp = await client.post(row["url"], content=body, headers=headers, timeout=HTTP_TIMEOUT)
        status_code = resp.status_code
        response = resp.text[:2048]
        ok = 200 <= resp.status_code < 300
    except Exception as exc:
        ok = False
        error = f"{type(exc).__name__}: {exc}"[:2048]

    if ok:
        await pool.execute(
            "UPDATE webhook_delivery_log "
            "SET status_code=$2, response_body=$3, delivered_at=NOW(), next_retry_at=NULL "
            "WHERE id=$1",
            row["id"], status_code, response,
        )
        log.info("delivered tenant=%s event=%s id=%s code=%s",
                 row["tenant_slug"], row["event"], row["id"], status_code)
    else:
        attempt = row["attempt"]
        nxt = next_retry(attempt)
        await pool.execute(
            "UPDATE webhook_delivery_log "
            "SET status_code=$2, response_body=$3, error=$4, attempt=$5, next_retry_at=$6 "
            "WHERE id=$1",
            row["id"], status_code, response, error, attempt + 1, nxt,
        )
        log.warning("failed tenant=%s event=%s id=%s attempt=%s code=%s next=%s err=%s",
                    row["tenant_slug"], row["event"], row["id"], attempt, status_code, nxt, error)


async def enqueue_cdr(pool: asyncpg.Pool, payload: dict) -> int | None:
    """Insert a pending delivery row for the CDR NOTIFY, picking the tenant's
    current webhook_url. Returns the delivery id (None if the tenant has no
    webhook_url or doesn't subscribe to this event)."""
    cdr_id = payload["cdr_id"]
    event = payload["event"]
    tenant_id = payload["tenant_id"]

    tenant = await pool.fetchrow(
        "SELECT slug, webhook_url, webhook_secret, webhook_events "
        "FROM tenants WHERE id=$1",
        tenant_id,
    )
    if not tenant or not tenant["webhook_url"]:
        return None
    if not any(_matches(e, event) for e in tenant["webhook_events"]):
        return None

    cdr = await pool.fetchrow(
        "SELECT id, uuid, caller_id_name, caller_id_number, destination_number, "
        "       context, start_stamp, answer_stamp, end_stamp, duration, billsec, "
        "       hangup_cause, recording_object_key, tenant_id "
        "FROM cdr WHERE id=$1",
        cdr_id,
    )
    if cdr is None:
        return None

    body = {
        "event": event,
        "tenant": tenant["slug"],
        "cdr": {k: cdr[k] for k in cdr.keys()},
    }
    row_id = await pool.fetchval(
        "INSERT INTO webhook_delivery_log "
        "(tenant_id, event, cdr_uuid, url, payload, next_retry_at) "
        "VALUES ($1,$2,$3,$4,$5,NOW()) RETURNING id",
        tenant_id, event, cdr["uuid"], tenant["webhook_url"], json.dumps(body, default=str),
    )
    return row_id


def _matches(pattern: str, event: str) -> bool:
    if pattern == event:
        return True
    if pattern.endswith(".*"):
        return event.startswith(pattern[:-2] + ".")
    return pattern == "*"


async def listener_loop(pool: asyncpg.Pool, queue: asyncio.Queue) -> None:
    conn = await asyncpg.connect(DSN)
    try:
        await conn.add_listener(
            "webhook",
            lambda _c, _pid, _ch, payload: queue.put_nowait(payload),
        )
        log.info("LISTEN on webhook channel")
        while True:
            await asyncio.sleep(3600)
    finally:
        await conn.close()


async def worker_loop(pool: asyncpg.Pool, client: httpx.AsyncClient, queue: asyncio.Queue) -> None:
    while True:
        payload_text = await queue.get()
        try:
            payload = json.loads(payload_text)
            row_id = await enqueue_cdr(pool, payload)
            if row_id is None:
                continue
            row = await pool.fetchrow(
                "SELECT d.id, d.event, d.url, d.attempt, d.payload::jsonb AS payload, "
                "       t.slug AS tenant_slug, t.webhook_secret "
                "FROM webhook_delivery_log d "
                "JOIN tenants t ON t.id = d.tenant_id "
                "WHERE d.id=$1",
                row_id,
            )
            await deliver_row(pool, client, row)
        except Exception:
            log.exception("worker failed")


async def sweep_loop(pool: asyncpg.Pool, client: httpx.AsyncClient) -> None:
    while True:
        try:
            rows = await pool.fetch(
                "SELECT d.id, d.event, d.url, d.attempt, d.payload::jsonb AS payload, "
                "       t.slug AS tenant_slug, t.webhook_secret "
                "FROM webhook_delivery_log d "
                "JOIN tenants t ON t.id = d.tenant_id "
                "WHERE d.delivered_at IS NULL "
                "  AND d.next_retry_at IS NOT NULL "
                "  AND d.next_retry_at <= NOW() "
                "  AND d.attempt < $1 "
                "ORDER BY d.next_retry_at LIMIT 50",
                MAX_ATTEMPTS,
            )
            for row in rows:
                await deliver_row(pool, client, row)
        except Exception:
            log.exception("sweep failed")
        await asyncio.sleep(SWEEP_INTERVAL)


async def main() -> None:
    pool = await asyncpg.create_pool(DSN, min_size=1, max_size=5)
    client = httpx.AsyncClient()
    queue: asyncio.Queue = asyncio.Queue(maxsize=1024)

    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, stop.set)

    tasks = [
        asyncio.create_task(listener_loop(pool, queue)),
        asyncio.create_task(worker_loop(pool, client, queue)),
        asyncio.create_task(sweep_loop(pool, client)),
    ]
    log.info("webhook-dispatcher started")
    await stop.wait()
    for t in tasks:
        t.cancel()
    await client.aclose()
    await pool.close()


if __name__ == "__main__":
    asyncio.run(main())
