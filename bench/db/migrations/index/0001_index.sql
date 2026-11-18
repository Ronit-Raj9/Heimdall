-- Results index tables (owner: Database, P1). Derived data: every row is read
-- from verified sealed run folders (resultsindex derivation) and the whole set
-- can be dropped and rebuilt (db.Rebuild). No verdict, leaderboard or release
-- reads these tables.

CREATE TABLE idx_experiments (
    org_id        TEXT NOT NULL,
    exp_id        TEXT NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    suite         TEXT NOT NULL DEFAULT '',   -- id@version
    arms          TEXT NOT NULL DEFAULT '[]', -- JSON array of arm ids, sorted
    state         TEXT NOT NULL DEFAULT '',   -- created | running | done (derived from sealed runs)
    decision      TEXT NOT NULL DEFAULT '',   -- newest analysis
    action        TEXT NOT NULL DEFAULT '',
    analysis_id   TEXT NOT NULL DEFAULT '',
    cost_usd      DOUBLE PRECISION,           -- sum over verified runs with a cost; NULL when none
    created_at    BIGINT NOT NULL DEFAULT 0,
    frozen_at     BIGINT NOT NULL DEFAULT 0,
    finished_at   BIGINT,                     -- newest seal when every slot has a verified run
    runner_pool   TEXT NOT NULL DEFAULT '',
    manifest_hash TEXT NOT NULL DEFAULT '',
    slots         INTEGER NOT NULL DEFAULT 0,
    runs          INTEGER NOT NULL DEFAULT 0,
    rejected      INTEGER NOT NULL DEFAULT 0,
    fingerprint   TEXT NOT NULL DEFAULT '',   -- what the rows were built from (seals.log, analyses)
    indexed_at    BIGINT NOT NULL,
    PRIMARY KEY (org_id, exp_id)
);
CREATE INDEX idx_experiments_created ON idx_experiments (org_id, created_at);
CREATE INDEX idx_experiments_state ON idx_experiments (org_id, state, created_at);
CREATE INDEX idx_experiments_suite ON idx_experiments (org_id, suite, created_at);

CREATE TABLE idx_runs (
    org_id         TEXT NOT NULL,
    exp_id         TEXT NOT NULL,
    run_id         TEXT NOT NULL,
    pair_id        TEXT NOT NULL DEFAULT '',
    slot_id        TEXT NOT NULL DEFAULT '',
    suite          TEXT NOT NULL DEFAULT '',
    task           TEXT NOT NULL DEFAULT '',
    arm            TEXT NOT NULL DEFAULT '',
    condition      TEXT NOT NULL DEFAULT '',
    trial          INTEGER NOT NULL DEFAULT 0,
    outcome        TEXT NOT NULL DEFAULT '',
    score          DOUBLE PRECISION,
    success        DOUBLE PRECISION,
    cost_usd       DOUBLE PRECISION,
    tokens         DOUBLE PRECISION,
    wall_s         DOUBLE PRECISION,
    started_at     BIGINT NOT NULL DEFAULT 0,
    exp_created_at BIGINT NOT NULL DEFAULT 0,
    seal_hash      TEXT NOT NULL DEFAULT '',
    synthetic      INTEGER NOT NULL DEFAULT 0,
    agent_id       TEXT NOT NULL DEFAULT '',
    config_hash    TEXT NOT NULL DEFAULT '',
    model          TEXT NOT NULL DEFAULT '',
    grade_source   TEXT NOT NULL DEFAULT '',
    not_repeatable INTEGER NOT NULL DEFAULT 0,
    metrics        TEXT NOT NULL DEFAULT '{}', -- JSON object of observed metric values
    problems       TEXT NOT NULL DEFAULT '[]', -- JSON array
    PRIMARY KEY (org_id, exp_id, run_id)
);
CREATE INDEX idx_runs_started ON idx_runs (org_id, started_at);
CREATE INDEX idx_runs_task ON idx_runs (org_id, task, started_at);
CREATE INDEX idx_runs_agent ON idx_runs (org_id, agent_id, started_at);
CREATE INDEX idx_runs_seal ON idx_runs (org_id, seal_hash);

CREATE TABLE idx_analyses (
    org_id       TEXT NOT NULL,
    exp_id       TEXT NOT NULL,
    analysis_id  TEXT NOT NULL,
    decision     TEXT NOT NULL DEFAULT '',
    action       TEXT NOT NULL DEFAULT '',
    policy       TEXT NOT NULL DEFAULT '',
    grade_source TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, exp_id, analysis_id)
);

-- Runs or experiments that failed verification: listed, never indexed.
CREATE TABLE idx_rejected (
    org_id    TEXT NOT NULL,
    exp_id    TEXT NOT NULL,
    run_id    TEXT NOT NULL DEFAULT '',
    seal_hash TEXT NOT NULL DEFAULT '',
    reason    TEXT NOT NULL
);
CREATE INDEX idx_rejected_exp ON idx_rejected (org_id, exp_id);

CREATE TABLE idx_meta (
    org_id     TEXT PRIMARY KEY,
    built_at   BIGINT NOT NULL DEFAULT 0, -- last full rebuild
    updated_at BIGINT NOT NULL DEFAULT 0  -- last write of any kind
);
