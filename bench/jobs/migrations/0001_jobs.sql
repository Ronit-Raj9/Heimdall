-- Jobs area (P4): background jobs and their bounded logs. Portable SQL:
-- times are unix milliseconds, booleans 0/1, JSON as TEXT.
CREATE TABLE jobs (
    id               TEXT PRIMARY KEY,
    org_id           TEXT NOT NULL,
    kind             TEXT NOT NULL,
    -- target names what the job works on (exp:<id>, suite:<id>, ...), empty
    -- for none; at most one queued or running job per (org, target), of
    -- any kind, so a run and a regrade of one experiment never overlap.
    target           TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL,
    params           TEXT NOT NULL DEFAULT '{}',
    progress         TEXT,
    result           TEXT,
    error            TEXT NOT NULL DEFAULT '',
    created_by       TEXT NOT NULL DEFAULT '',
    created_at       BIGINT NOT NULL,
    started_at       BIGINT,
    finished_at      BIGINT,
    cancel_requested INTEGER NOT NULL DEFAULT 0,
    attempt          INTEGER NOT NULL DEFAULT 0,
    lease_owner      TEXT,
    lease_until      BIGINT
);

CREATE INDEX jobs_org_created ON jobs (org_id, created_at);

CREATE INDEX jobs_status_created ON jobs (status, created_at);

CREATE INDEX jobs_org_target ON jobs (org_id, kind, target, created_at);

CREATE UNIQUE INDEX jobs_active_target ON jobs (org_id, target)
    WHERE target <> '' AND status IN ('queued', 'running');

CREATE TABLE job_logs (
    job_id TEXT NOT NULL,
    seq    BIGINT NOT NULL,
    at     BIGINT NOT NULL,
    level  TEXT NOT NULL,
    line   TEXT NOT NULL,
    PRIMARY KEY (job_id, seq)
);
