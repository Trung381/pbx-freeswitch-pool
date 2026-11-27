-- OpenSIPS 3.6 PostgreSQL schema for pbx-freeswitch-pool.
-- Extracted from the upstream opensips-postgres-dbschema (3.6.10-1) package
-- and trimmed to the four tables used in Phase 1 (dispatcher, location,
-- subscriber, acc). Phase 2 may add more; versions MUST match whatever the
-- deployed opensips expects or mod_init refuses to start.

CREATE TABLE IF NOT EXISTS version (
    table_name VARCHAR(32) PRIMARY KEY,
    table_version INTEGER NOT NULL DEFAULT 0
);

-- ---- dispatcher (version 9) ----
CREATE TABLE IF NOT EXISTS dispatcher (
    id SERIAL PRIMARY KEY NOT NULL,
    setid INTEGER DEFAULT 0 NOT NULL,
    destination VARCHAR(192) DEFAULT '' NOT NULL,
    socket VARCHAR(128) DEFAULT NULL,
    state INTEGER DEFAULT 0 NOT NULL,
    probe_mode INTEGER DEFAULT 0 NOT NULL,
    weight VARCHAR(64) DEFAULT '1' NOT NULL,
    priority INTEGER DEFAULT 0 NOT NULL,
    attrs VARCHAR(128) DEFAULT NULL,
    description VARCHAR(64) DEFAULT NULL,
    CONSTRAINT dispatcher_setid_dest_key UNIQUE (setid, destination)
);
INSERT INTO version(table_name, table_version) VALUES ('dispatcher', 9)
    ON CONFLICT (table_name) DO UPDATE SET table_version = EXCLUDED.table_version;

-- ---- location (version 1013) ----
CREATE TABLE IF NOT EXISTS location (
    contact_id BIGSERIAL PRIMARY KEY NOT NULL,
    username VARCHAR(64) DEFAULT '' NOT NULL,
    domain VARCHAR(64) DEFAULT NULL,
    contact TEXT NOT NULL,
    received VARCHAR(255) DEFAULT NULL,
    path VARCHAR(255) DEFAULT NULL,
    expires INTEGER NOT NULL,
    q REAL DEFAULT 1.0 NOT NULL,
    callid VARCHAR(255) DEFAULT 'Default-Call-ID' NOT NULL,
    cseq INTEGER DEFAULT 13 NOT NULL,
    last_modified TIMESTAMP WITHOUT TIME ZONE DEFAULT '1900-01-01 00:00:01' NOT NULL,
    flags INTEGER DEFAULT 0 NOT NULL,
    cflags VARCHAR(255) DEFAULT NULL,
    user_agent VARCHAR(255) DEFAULT '' NOT NULL,
    socket VARCHAR(64) DEFAULT NULL,
    methods INTEGER DEFAULT NULL,
    sip_instance VARCHAR(255) DEFAULT NULL,
    kv_store TEXT DEFAULT NULL,
    attr VARCHAR(255) DEFAULT NULL
);
INSERT INTO version(table_name, table_version) VALUES ('location', 1013)
    ON CONFLICT (table_name) DO UPDATE SET table_version = EXCLUDED.table_version;

-- ---- subscriber (version 8), provisioned for Phase 2 auth at the edge ----
CREATE TABLE IF NOT EXISTS subscriber (
    id SERIAL PRIMARY KEY NOT NULL,
    username VARCHAR(64) DEFAULT '' NOT NULL,
    domain VARCHAR(64) DEFAULT '' NOT NULL,
    password VARCHAR(25) DEFAULT '' NOT NULL,
    ha1 VARCHAR(64) DEFAULT '' NOT NULL,
    ha1_sha256 VARCHAR(64) DEFAULT '' NOT NULL,
    ha1_sha512t256 VARCHAR(64) DEFAULT '' NOT NULL,
    CONSTRAINT subscriber_account_idx UNIQUE (username, domain)
);
CREATE INDEX IF NOT EXISTS subscriber_username_idx ON subscriber (username);
INSERT INTO version(table_name, table_version) VALUES ('subscriber', 8)
    ON CONFLICT (table_name) DO UPDATE SET table_version = EXCLUDED.table_version;

-- ---- acc (version 7). Phase 1 does NOT load the acc module — this is wired
-- for Phase 4 when we start a second CDR path alongside FreeSWITCH's ESL-driven
-- writer.
CREATE TABLE IF NOT EXISTS acc (
    id SERIAL PRIMARY KEY NOT NULL,
    method VARCHAR(16) DEFAULT '' NOT NULL,
    from_tag VARCHAR(64) DEFAULT '' NOT NULL,
    to_tag VARCHAR(64) DEFAULT '' NOT NULL,
    callid VARCHAR(64) DEFAULT '' NOT NULL,
    sip_code VARCHAR(3) DEFAULT '' NOT NULL,
    sip_reason VARCHAR(32) DEFAULT '' NOT NULL,
    time TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    duration INTEGER DEFAULT 0 NOT NULL,
    ms_duration INTEGER DEFAULT 0 NOT NULL,
    setuptime INTEGER DEFAULT 0 NOT NULL,
    created TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL
);
CREATE INDEX IF NOT EXISTS acc_callid_idx ON acc (callid);
INSERT INTO version(table_name, table_version) VALUES ('acc', 7)
    ON CONFLICT (table_name) DO UPDATE SET table_version = EXCLUDED.table_version;

-- ---- Dispatcher seed. setid 1 = FS internal (REGISTER + UA), setid 2 = FS
-- external (trunk).
INSERT INTO dispatcher(setid, destination, priority, attrs, description)
VALUES (1, 'sip:freeswitch-host:15060', 10, 'role=internal', 'fs-node-1 internal')
ON CONFLICT (setid, destination) DO NOTHING;

INSERT INTO dispatcher(setid, destination, priority, attrs, description)
VALUES (2, 'sip:freeswitch-host:15080', 10, 'role=external', 'fs-node-1 external')
ON CONFLICT (setid, destination) DO NOTHING;
