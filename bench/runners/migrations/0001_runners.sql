-- Runners and runner tokens (owner: Runners, P5; docs/runners.md).
-- Portable SQL: runs on SQLite and Postgres. Times are unix milliseconds UTC.

-- A runner token (hmr_<prefix>_<secret>) is scoped to one org and one pool.
-- Only the sha256 of the whole token is stored; the token is shown once.
CREATE TABLE runner_tokens (
    id           TEXT PRIMARY KEY,
    org_id       TEXT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    pool         TEXT NOT NULL,
    name         TEXT NOT NULL DEFAULT '',
    prefix       TEXT NOT NULL,
    hash         TEXT NOT NULL UNIQUE,
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL,
    expires_at   BIGINT,          -- NULL: never
    last_used_at BIGINT,
    revoked_at   BIGINT           -- NULL: active
);
CREATE INDEX runner_tokens_org ON runner_tokens (org_id, created_at);

-- A runner is a machine that registered with a runner token; its name is
-- the worker id it registers under, unique within the org.
CREATE TABLE runners (
    id            TEXT PRIMARY KEY,
    org_id        TEXT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    pool          TEXT NOT NULL,
    labels        TEXT NOT NULL DEFAULT '[]',  -- JSON array of strings
    version       TEXT NOT NULL DEFAULT '',
    host          TEXT NOT NULL DEFAULT '',
    slots         INTEGER NOT NULL DEFAULT 1,
    running       INTEGER NOT NULL DEFAULT 0,  -- slots held at last_seen
    token_id      TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'idle', -- idle | running as last written; lost is derived from last_seen
    last_seen     BIGINT NOT NULL,
    registered_at BIGINT NOT NULL,
    UNIQUE (org_id, name)
);
CREATE INDEX runners_org ON runners (org_id, last_seen);
