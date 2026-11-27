# Integrations on the website

[Settings > Integrations](/settings?tab=integrations) has three tabs: CI, Exports, Webhooks. Live
scoring and Production failures live under **Production** in the sidebar.

## CI gate

Connect GitHub first ([Settings > GitHub](/settings?tab=github), connect and pick repositories).
Then **Integrations > CI**:

1. **Repository**, **Mode**:
   - **Recorded**: re-check a sealed experiment on every pull request. No
     runner needed.
   - **Live**: fresh baseline vs candidate refs on a self-hosted runner.
2. **Agent both arms start from**.
3. Read the **Workflow preview** (or **Copy** it), then **Open pull
   request**. Heimdall opens a PR that adds the workflow to the repo.

The workflow exits non-zero on a regressed verdict, so branch protection
can block the merge.

## Exports

**Integrations > Exports**: pick a format and an experiment (or a suite for
Harbor), press **Export**. It runs as a job; the file appears in **Export
files** with **Download** and **Delete**.

| Format | For |
|---|---|
| OTLP traces | Langfuse, LangSmith, Phoenix, Jaeger, any OpenTelemetry backend |
| Every Eval Ever records | shared eval result format |
| Run table CSV / JSON lines | spreadsheets, notebooks |
| Harbor task folders | the suite, verifiers withheld |

Options: include tool arguments and results (redacted and capped), allow
synthetic results (labelled). A single run's trace: run page, **Download
trace (OTLP)**.

## Webhooks

**Integrations > Webhooks**: payload URL, description, events (**Experiment
finished**, **Background job failed**), **Add webhook**. The signing secret
shows once: store it, then **I stored it**. **Send test**, **Enable** /
**Disable**, **Delete**, and recent deliveries per hook. Hosted servers
deliver only to public https URLs.

## Inbox ingest

[Production failures](/failures): flagged production sessions become task drafts.

- **Push failures in**: add the shown snippet to `inbox/sources.yaml` and
  point your tracing tool's webhook at the ingest URL. Works hosted.
- **Pull now** reads your trace tools with a credential, so it runs only on
  a local Heimdall. Hosted orgs use **Push failures in** instead.
- Each item: accept into a suite as a draft regression task, reject, or snooze.

## Live scoring

**Production > Live traffic**: score real traffic from the Bifrost gateway,
read-only.

1. **Set up live scoring**: gateway URL (empty uses the server's gateway)
   and the name of a server environment variable holding the token, never
   the token itself.
2. **Pull now** reads the gateway's logs once (optionally with the paid
   judge); **Recompute comparison** tests the two configs on them. Each runs
   as a background job with a live log.
3. **Start a new comparison from now** resets the window.
