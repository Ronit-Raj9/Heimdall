-- Verified email, server admin grants and signup settings (Wave A, audit
-- B1, B10). Times are unix milliseconds UTC.

-- email_verifications: the address a user proved they read. A user counts
-- as verified only while users.email still equals this email. method is
-- email (they opened the emailed link) or operator (bifrost-bench user
-- verify). GitHub-linked accounts count as verified through GitHub.
CREATE TABLE email_verifications (
    user_id      TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    email        TEXT NOT NULL,
    method       TEXT NOT NULL,
    verified_at  BIGINT NOT NULL
);

-- email_verify_tokens: one-time verification links. Only the sha256 of the
-- token is stored; a newer link replaces the pending one.
CREATE TABLE email_verify_tokens (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  BIGINT NOT NULL,
    expires_at  BIGINT NOT NULL,
    used_at     BIGINT
);
CREATE INDEX email_verify_tokens_user ON email_verify_tokens (user_id);

-- server_admin_grants: server admins bound to a user id. source cli is a
-- grant made by `bifrost-bench admin grant` (kept until revoked); source
-- email binds a verified HEIMDALL_SERVER_ADMINS address to the user who
-- first signed in with it verified (it applies only while the address is
-- still listed).
CREATE TABLE server_admin_grants (
    user_id     TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    source      TEXT NOT NULL,
    granted_by  TEXT NOT NULL DEFAULT '',
    granted_at  BIGINT NOT NULL
);

-- auth_settings: server-wide auth settings a server admin changed from the
-- web (signup mode and allowed domains); they override deploy/bench.yaml.
CREATE TABLE auth_settings (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    updated_by  TEXT NOT NULL DEFAULT '',
    updated_at  BIGINT NOT NULL
);
