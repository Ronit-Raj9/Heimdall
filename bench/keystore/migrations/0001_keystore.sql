-- Keystore area (web parity L04): provider keys and secrets per org.
-- value_enc is "v1:" + base64(nonce || AES-256-GCM ciphertext) under the
-- server master key (HEIMDALL_MASTER_KEY), with org_id and id as associated
-- data. The plaintext is never stored. Times are unix milliseconds UTC.

CREATE TABLE model_keys (
    id               TEXT PRIMARY KEY,
    org_id           TEXT NOT NULL,
    kind             TEXT NOT NULL,            -- provider | secret
    provider         TEXT NOT NULL DEFAULT '',
    label            TEXT NOT NULL DEFAULT '',
    base_url         TEXT NOT NULL DEFAULT '',
    last4            TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'untested',
    created_by       TEXT NOT NULL DEFAULT '',
    models           TEXT NOT NULL DEFAULT '[]', -- JSON array of model names
    budget_usd       DOUBLE PRECISION NOT NULL DEFAULT 0,  -- monthly; 0 = no limit
    month_spent_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
    spend_month      TEXT NOT NULL DEFAULT '', -- YYYY-MM the spend counts toward
    value_enc        TEXT NOT NULL,
    created_at       BIGINT NOT NULL,
    updated_at       BIGINT NOT NULL,
    last_tested_at   BIGINT
);
CREATE INDEX model_keys_org ON model_keys (org_id, kind);
