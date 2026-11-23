-- Share link index (fix wave 3, N293). A hosted share link's token is opaque
-- (s_...) and names no organization, so the server finds the org through
-- this table: token_hash is the hex SHA-256 of the token (the token itself
-- is never stored), share_id the link inside the org's share store. Rows
-- are written when a link is created and by the start-up backfill; deleting
-- an org removes its rows. Additive only.
CREATE TABLE share_tokens (
    token_hash TEXT PRIMARY KEY,
    org_id     TEXT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    share_id   TEXT NOT NULL,
    created_at BIGINT NOT NULL
);
CREATE INDEX share_tokens_org ON share_tokens (org_id);
