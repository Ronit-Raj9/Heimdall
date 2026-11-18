-- Core tables (owner: Database, P1). Portable SQL: runs on SQLite and Postgres.
-- Times are unix milliseconds UTC, booleans INTEGER 0/1, JSON is TEXT.

CREATE TABLE orgs (
    id          TEXT PRIMARY KEY,
    slug        TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    public_read INTEGER NOT NULL DEFAULT 0,
    created_at  BIGINT NOT NULL
);

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,   -- stored lowercased and trimmed
    name          TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '', -- argon2id PHC string; '' for OAuth-only users
    github_id     TEXT UNIQUE,            -- NULL when not linked
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    BIGINT NOT NULL
);

CREATE TABLE memberships (
    org_id     TEXT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    created_at BIGINT NOT NULL,
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX memberships_user ON memberships (user_id);

-- No foreign key on org_id: the audit trail outlives a deleted org.
-- org_id '' is an instance-level event (signup, login failure).
CREATE TABLE audit_log (
    id          TEXT PRIMARY KEY,
    org_id      TEXT NOT NULL DEFAULT '',
    actor_type  TEXT NOT NULL,              -- user | token | runner | system | anonymous
    actor_id    TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,              -- dotted verb, e.g. member.add
    target_type TEXT NOT NULL DEFAULT '',
    target_id   TEXT NOT NULL DEFAULT '',
    ip          TEXT NOT NULL DEFAULT '',
    at          BIGINT NOT NULL,
    details     TEXT NOT NULL DEFAULT '{}'  -- JSON object
);
CREATE INDEX audit_log_org_at ON audit_log (org_id, at, id);
CREATE INDEX audit_log_actor ON audit_log (org_id, actor_type, actor_id, at);
CREATE INDEX audit_log_target ON audit_log (org_id, target_type, target_id, at);
CREATE INDEX audit_log_action ON audit_log (org_id, action, at);

-- org_id '' holds instance-wide settings.
CREATE TABLE settings (
    org_id     TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at BIGINT NOT NULL,
    PRIMARY KEY (org_id, key)
);
