-- Open models served on an organization's own Modal account (lane Z05,
-- package openmodel). One row per deployment; data is the deployment as
-- JSON (no secret: the API key lives in the keystore as the org's provider
-- key key_id). Times are unix milliseconds UTC.

CREATE TABLE open_model_deployments (
    id          TEXT PRIMARY KEY,
    org_id      TEXT NOT NULL,
    state       TEXT NOT NULL,
    data        TEXT NOT NULL,
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);
CREATE INDEX open_model_deployments_org ON open_model_deployments (org_id, created_at);
CREATE INDEX open_model_deployments_state ON open_model_deployments (state);
