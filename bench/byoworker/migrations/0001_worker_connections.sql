-- Bring your own worker (owner: byoworker; FIX_BRIEF decision 6).
-- Portable SQL: runs on SQLite and Postgres. Times are unix milliseconds UTC.

-- One worker an org connected: a platform driver's box (Runloop, Fly
-- Machines, Kubernetes, ...) or a VM the org installs itself. name is also
-- the worker's runner name and runner pool. Secrets live in the keystore
-- (model_keys): secret_key_id holds the platform credential, runner_key_id
-- the runner token; neither value is ever in this table.
CREATE TABLE worker_connections (
    id              TEXT PRIMARY KEY,
    org_id          TEXT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    platform        TEXT NOT NULL,
    settings        TEXT NOT NULL DEFAULT '{}',  -- JSON object of non-secret settings
    secret_key_id   TEXT NOT NULL DEFAULT '',
    runner_token_id TEXT NOT NULL DEFAULT '',
    runner_key_id   TEXT NOT NULL DEFAULT '',
    instance_id     TEXT NOT NULL DEFAULT '',    -- the platform's id of the started box
    instance_state  TEXT NOT NULL DEFAULT '',    -- starting | running | stopped | failed | unknown
    instance_detail TEXT NOT NULL DEFAULT '',
    health          TEXT NOT NULL DEFAULT '',    -- JSON of the last check
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      BIGINT NOT NULL,
    updated_at      BIGINT NOT NULL,
    UNIQUE (org_id, name)
);

-- The worker an org's evaluations run on when they name none.
CREATE TABLE worker_prefs (
    org_id       TEXT PRIMARY KEY REFERENCES orgs (id) ON DELETE CASCADE,
    default_pool TEXT NOT NULL DEFAULT '',
    updated_by   TEXT NOT NULL DEFAULT '',
    updated_at   BIGINT NOT NULL
);
