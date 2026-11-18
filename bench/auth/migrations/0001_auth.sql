-- Auth area (P2): sessions, API tokens, invitations, OAuth states.
-- Every secret is stored as the lowercase hex sha256 of a 32-byte random
-- value; the value itself is shown once and never stored (docs/auth.md).
-- Times are unix milliseconds UTC.

CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,          -- sha256 of the cookie value
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id      TEXT,                      -- current organization, NULL when none
    csrf        TEXT NOT NULL,
    created_at  BIGINT NOT NULL,
    expires_at  BIGINT NOT NULL,           -- absolute expiry
    last_seen   BIGINT NOT NULL,           -- idle expiry is last_seen + session_idle_h
    ip          TEXT NOT NULL DEFAULT '',
    user_agent  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE api_tokens (
    id            TEXT PRIMARY KEY,
    org_id        TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    user_id       TEXT REFERENCES users(id) ON DELETE CASCADE,  -- NULL: an org token with no creator; a deleted creator's tokens go with them
    name          TEXT NOT NULL,
    prefix        TEXT NOT NULL,           -- the 8 characters after hm_, shown in lists
    hash          TEXT NOT NULL UNIQUE,    -- sha256 of the whole token
    role          TEXT NOT NULL,
    created_at    BIGINT NOT NULL,
    last_used_at  BIGINT,
    expires_at    BIGINT,
    revoked_at    BIGINT
);
CREATE INDEX api_tokens_org ON api_tokens (org_id);

CREATE TABLE invitations (
    id           TEXT PRIMARY KEY,
    org_id       TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    email        TEXT NOT NULL,            -- lowercased
    role         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    created_by   TEXT,
    created_at   BIGINT NOT NULL,
    expires_at   BIGINT NOT NULL,
    accepted_at  BIGINT
);
CREATE INDEX invitations_org ON invitations (org_id);

CREATE TABLE oauth_states (
    state_hash  TEXT PRIMARY KEY,
    created_at  BIGINT NOT NULL,
    return_to   TEXT NOT NULL DEFAULT ''
);
