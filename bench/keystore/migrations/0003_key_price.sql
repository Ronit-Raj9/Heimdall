-- What the org pays on this key per million tokens, JSON
-- {"input_per_mtok","output_per_mtok","cache_per_mtok"}; '' for none. Runs of
-- a model the pinned price snapshot does not list (a custom endpoint) are
-- costed from it.
ALTER TABLE model_keys ADD COLUMN price TEXT NOT NULL DEFAULT '';
