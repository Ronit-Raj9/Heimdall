-- Fair queueing (INFRA_I4 item 2): a job's priority within the order the
-- runner claims in (per-org round robin first: the organization with the
-- fewest running jobs of the kind, then priority, then age). 0 is normal;
-- higher runs sooner (a release gate bumped by hand).
ALTER TABLE jobs ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;

-- The claim counts each candidate organization's running jobs of a kind.
CREATE INDEX jobs_org_kind_status ON jobs (org_id, kind, status);
