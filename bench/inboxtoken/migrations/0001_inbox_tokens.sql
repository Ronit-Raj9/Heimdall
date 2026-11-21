-- Inbox ingest tokens (gap G02): per-org bearer tokens a tool pushes
-- flagged traces into the org's failure inbox with, on the hosted server
-- (POST /api/bench/orgs/{org}/inbox/ingest). Only the sha256 of a token is
-- stored; the token itself is shown once, when it is created. Revoking
-- deletes the row. Times are unix milliseconds UTC.

CREATE TABLE inbox_tokens (
    id           TEXT PRIMARY KEY,
    org_id       TEXT NOT NULL,
    label        TEXT NOT NULL DEFAULT '',
    token_sha256 TEXT NOT NULL UNIQUE,
    last4        TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL,
    last_used_at BIGINT
);
CREATE INDEX inbox_tokens_org ON inbox_tokens (org_id);
