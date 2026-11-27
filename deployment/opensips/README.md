# OpenSIPS SBC overlay — Phase 1

Puts OpenSIPS 3.6 in front of the existing FreeSWITCH + CustomPBX stack, so
FreeSWITCH no longer binds the public SIP ports. A single ingress host
(43.240.117.122) now looks like:

```
  UDP/TCP 5060, 5080 ─► opensips-host
                            │ dispatcher
                            ▼
                      freeswitch-host
                     :15060 internal
                     :15061 internal-tls
                     :15080 external
```

rtpengine is loaded alongside OpenSIPS and will take over media once the Phase 1
dialplan hands media to it; for now FreeSWITCH is still happy to send RTP
directly on 16384-16399 if rtpengine is not attached.

## Files

| Path                                              | What it is                                           |
|---------------------------------------------------|------------------------------------------------------|
| `deployment/opensips/opensips.cfg`                | Full opensips script (SBC routing + dispatcher + rtpengine) |
| `deployment/opensips/schema.sql`                  | OpenSIPS 3.6 tables (dispatcher, location, subscriber, acc) + seed |
| `deployment/opensips/init-opensips.sql`           | Create the `opensips` role + database inside the shared cluster |
| `deployment/opensips/rtpengine.conf`              | rtpengine config (two interfaces, port 10000-10999)  |
| `deployment/opensips/migrate-ports-15xxx.sql`     | Move Sofia bindings from 5060/5061/5080 to 15060/15061/15080 (idempotent) |
| `docker/opensips/Dockerfile`                      | `opensips/opensips:3.6` + `opensips-postgres-module` |
| `docker-compose.opensips.yml`                     | Compose overlay                                      |

## Deploy

```bash
# From /opt/pbx-freeswitch-pool on the target host:
docker exec -i postgres-host psql -U custompbx -d custompbx \
    < deployment/opensips/migrate-ports-15xxx.sql   # one-shot CustomPBX update

docker compose \
    -f docker-compose.yml \
    -f docker-compose.opensips.yml \
    up -d
```

The `opensips-init` sidecar provisions the `opensips` DB and schema; it is
idempotent and exits with `completed_successfully` so the OpenSIPS container
can depend on it.

## Rollback

```bash
docker compose -f docker-compose.yml up -d
```

This drops the overlay, FreeSWITCH republishes 5060/5061/5080, and the SIP
world works exactly as it did before this phase (modulo the port numbers in
CustomPBX — re-run the inverse of `migrate-ports-15xxx.sql` if you want the
Sofia profiles back on the public defaults).

## Dispatcher seeds

| setid | destination                  | role      |
|-------|------------------------------|-----------|
| 1     | `sip:freeswitch-host:15060`  | internal  |
| 2     | `sip:freeswitch-host:15080`  | external  |

Add more FreeSWITCH nodes by inserting additional rows and calling `opensips-cli
-x mi ds_reload`.

## Known gaps (picked up in later phases)

- TLS on 5061 is deliberately **not** enabled. Phase 4 wires the
  `wss.td1.tekomi.vn` certificate into the OpenSIPS container.
- Per-tenant routing lives in Phase 2; today every registration lands in setid 1
  and every PSTN hop lands in setid 2.
- `acc` is provisioned but not loaded. FreeSWITCH's ESL-driven CDR writer is
  still the only CDR producer.
