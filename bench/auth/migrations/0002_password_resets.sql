-- Account safety (L22): one-time password reset links an org owner makes
-- for a member. The token is shown once; only its sha256 is stored. Times
-- are unix milliseconds UTC.

CREATE TABLE password_resets (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id      TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    created_by  TEXT NOT NULL,
    created_at  BIGINT NOT NULL,
    expires_at  BIGINT NOT NULL,
    used_at     BIGINT
);
CREATE INDEX password_resets_user ON password_resets (user_id);
