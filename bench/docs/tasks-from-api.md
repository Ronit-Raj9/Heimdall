# Tasks from your API and policy

Upload an OpenAPI 3 file and your policy document; Heimdall builds a copy of
your app, drafts support tasks that test the policy, proves each task against
the app copy, and writes the survivors into a benchmark as pending review. The
readiness checks are the quality filter. Page: Benchmarks > Import > "Build
tasks from your OpenAPI file and policy" (`/suites/from-api`). Code:
`bench/apptasks`, `handlers/apptasks.go`, job kind `apptasks.generate`
(`cmd/bifrost-bench/jobs_apptasks.go`).

## Steps

1. **Upload** (`POST /api/bench/app-tasks`): benchmark id, app id (the tool
   server's name), the OpenAPI file and the policy as text or Markdown (paste
   a PDF's text). The spec must make an app copy (the Y14 OpenAPI mock); no
   model is called. Drafts live in `uploads/app-tasks/<draft>/`.
2. **Policy rules** (job `apptasks.generate`, `stage: rules`): the pinned
   model lists the policy as numbered rules (must, must never, hand to a
   person, fact to use) tied to the API's operations. A person edits the list
   and approves it once (`PUT /app-tasks/{id}/rules`); tasks are drafted only
   from approved rules.
3. **Draft tasks** (`stage: tasks`): the model writes seed data (checked
   against the spec's schemas: collections, ids, required fields, types,
   enums), then scenarios rule by rule (customer, goal, facts, scripted
   replies, the calls a correct agent makes, the call the rule forbids). Each
   scenario is proved in process with the app copy's rules:
   - every reference step succeeds on the seed;
   - the end state becomes state checks (changed fields, created and deleted
     records; free text such as notes is not compared);
   - the forbidden call becomes a `tool_not_called` check plus state checks
     that its effect did not happen;
   - the reference passes; an agent that does nothing, one that skips the key
     step and one that also makes the forbidden call each fail.
   Dropped scenarios are listed with the reason.
4. **Write** (`POST /app-tasks/{id}/write`): a new benchmark gets
   `services/<app>/` (mock server, `mock.json`, the OpenAPI file and the user
   simulator in one image), grader `checks`, and (when reply rubrics were
   kept) the pinned judge. Each task is a Y14 scenario-row task: a simulated
   customer, `seed/<app>.json`, `verifier/checks.yaml`, reference and
   mutant controls, `provenance.created_by: api policy generator` and
   `instruction_reviewed: false`. An existing benchmark only takes tasks from
   the same app copy.
5. **Readiness and review**: run the readiness checks on the benchmark (they
   build the app image and prove every task with its controls on a worker),
   then accept (marks it reviewed) or reject (removes it) each task
   (`POST /app-tasks/{id}/review`).

## Model and cost

The model is pinned per job (`provider`, `model`) and recorded in the draft
with every call's prompt hash, attempts and cost. Calls go through the
server's Bifrost gateway on a virtual key minted for the job and revoked at
the end. Hosted, the key routes to the org's own model key (never the
server's), the cap is 200 calls and $5 per job (`max_usd` may only lower it,
and the key's month budget lowers it further), the model must be in the price
snapshot, and the spend is added to the key's month spend. The cap is checked
before every call with a high estimate, so a job can not pass it. Replies must
be strict JSON; a bad reply is retried twice with the reason, then reported.
`yes: true` confirms the cost.
