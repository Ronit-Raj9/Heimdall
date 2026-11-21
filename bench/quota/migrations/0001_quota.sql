-- Per-org limits and platform switches (owner: web parity lane L03, shared
-- runner pool and guardrails). Portable SQL: runs on SQLite and Postgres.
-- Times are unix milliseconds UTC.

-- One org's limits as JSON (quota.Limits). An org with no row gets the
-- store's defaults.
CREATE TABLE org_quotas (
    org_id     TEXT PRIMARY KEY,
    limits     TEXT NOT NULL,
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at BIGINT NOT NULL
);

-- Platform-wide switches, e.g. the kill switch (name 'kill_switch',
-- value '1' or '0').
CREATE TABLE platform_settings (
    name       TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at BIGINT NOT NULL
);
