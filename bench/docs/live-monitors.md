# Live monitors

A live monitor runs a few real tasks (default 3, at most 5) of a ready benchmark against a deployed
agent's remote endpoint on a schedule (default hourly, minimum 15 minutes). Open it from
Scheduled checks, tab Live monitors, or from an agent page: More, Monitor this agent.

- Every running monitor is a schedule, so the daily budget, overlap lock and hosted guardrails
  of scheduled checks apply. Each monitor also has its own budget per day.
- About once a minute the schedule ticker collects finished checks: pass rate, latency p50/p95,
  cost and errors per check, shown as charts on the monitor page.
- A drop is a statistical test over a rolling window (recent checks against a baseline window),
  never a single failed run. An endpoint that cannot be reached for several checks in a row opens
  an infrastructure incident, kept apart from quality incidents.
- Each incident sends one alert thread: opened, updated, resolved, as `monitor.incident` webhook
  events with links to the failing runs. Route them to Slack, PagerDuty, email or webhooks from
  Integrations.
- Pause and resume stop and restart checks. A public read-only status badge can be turned on
  per monitor.

API: `GET/POST /api/v1/live-monitors`, `GET/PUT/DELETE /api/v1/live-monitors/{id}`,
`POST /api/v1/live-monitors/{id}/pause|resume|run|badge`,
`GET /api/v1/live-monitor-badges/{token}/badge.svg`.
