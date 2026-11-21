-- Public API v1 (docs/api-v1.md): API token scopes. Comma-separated subset
-- of read, evaluate, admin; '' for tokens made before scopes existed, which
-- their role alone limits.

ALTER TABLE api_tokens ADD COLUMN scopes TEXT NOT NULL DEFAULT '';
