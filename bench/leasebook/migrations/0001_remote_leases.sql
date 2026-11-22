-- Durable remote leases (Z11, INFRA_I4 item 3b): one row per slot lease a
-- hosted coordinator handed to a runner, written before the lease is
-- answered. A server restart keeps the open rows, and the next process
-- re-attaches them when the experiment resumes. Portable SQL: SQLite and
-- Postgres. Times are unix milliseconds UTC.
CREATE TABLE remote_leases (
    id           TEXT PRIMARY KEY,
    org_id       TEXT NOT NULL,
    exp_id       TEXT NOT NULL,
    slot_id      TEXT NOT NULL,
    worker_id    TEXT NOT NULL,
    owner        TEXT NOT NULL,
    attempt      INTEGER NOT NULL,
    reserved_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
    gpu_ids      TEXT NOT NULL DEFAULT '[]',
    last_beat    BIGINT NOT NULL,
    state        TEXT NOT NULL,            -- open | ended
    ended_why    TEXT NOT NULL DEFAULT '',
    ended_at     BIGINT,
    record       TEXT NOT NULL             -- engine.LeaseRecord as JSON (worker registration, scope)
);
CREATE INDEX remote_leases_open ON remote_leases (org_id, state);
