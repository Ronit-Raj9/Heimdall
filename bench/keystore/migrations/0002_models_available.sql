-- Model ids a provider listed at the key's last successful test (JSON
-- array, sorted, at most 200). The model picker offers them in a dropdown.
ALTER TABLE model_keys ADD COLUMN models_available TEXT NOT NULL DEFAULT '[]';
