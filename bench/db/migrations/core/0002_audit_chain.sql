-- Hash chain of the audit log (lane X16, audit N11). Every row written from
-- now on carries its org's next sequence number, the previous row's hash and
-- its own hash (db.AuditHash), so an edited, removed or reordered row breaks
-- the chain that GET /orgs/{org}/audit/verify and `bifrost-bench org
-- audit-verify` check. Rows written before this migration keep seq 0 and an
-- empty hash: they are reported as legacy, outside the chain.
ALTER TABLE audit_log ADD COLUMN seq BIGINT NOT NULL DEFAULT 0;
ALTER TABLE audit_log ADD COLUMN prev_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN hash TEXT NOT NULL DEFAULT '';
-- One row per position of an org's chain: two writers racing for the same
-- position fail instead of forking the chain.
CREATE UNIQUE INDEX audit_log_chain ON audit_log (org_id, seq) WHERE seq > 0;

-- Organization deletions (lane X16, audit N10): one row per requested
-- deletion, kept after the org is gone as the deletion receipt. The worker
-- picks up rows that are not done, so a restart resumes a deletion.
CREATE TABLE org_deletions (
    org_id       TEXT PRIMARY KEY,
    slug         TEXT NOT NULL,
    status       TEXT NOT NULL,            -- scheduled | running | done | failed
    requested_by TEXT NOT NULL DEFAULT '',
    requested_at BIGINT NOT NULL,
    finished_at  BIGINT,
    error        TEXT NOT NULL DEFAULT '',
    details      TEXT NOT NULL DEFAULT '{}'
);
