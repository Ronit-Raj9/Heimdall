-- Custom Docker image requests (owner: web parity lane L03). An org asks
-- for a digest-pinned image; a server admin approves or rejects it.
-- Portable SQL: runs on SQLite and Postgres. Times are unix milliseconds UTC.
CREATE TABLE image_requests (
    id           TEXT PRIMARY KEY,
    org_id       TEXT NOT NULL,
    image        TEXT NOT NULL,   -- name@sha256:<64 hex>
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    note         TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL,   -- pending | approved | rejected
    requested_by TEXT NOT NULL DEFAULT '',
    decided_by   TEXT NOT NULL DEFAULT '',
    decision     TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL,
    decided_at   BIGINT
);
CREATE INDEX image_requests_org ON image_requests (org_id, created_at);
CREATE INDEX image_requests_status ON image_requests (status, created_at);
