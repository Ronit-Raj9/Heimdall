# Sharing results

Share one result with people outside your organization, without giving them
an account: a report card, an evaluation, a verdict, a run trace or a
leaderboard view. Press **Share** on that page.

## Share links

A share link is read-only and opens one item only. Whoever has it sees that
item and nothing else in your organization.

- **Expiry**: 1, 7, 30 or 90 days, or never. The default is 7 days. Only an
  organization admin or owner can make a link that never expires.
- **Turn off**: any member can turn a link off in the same dialog. It stops
  working at once.
- **Secrets are never shown.** Keys, tokens, passwords, environment values
  and who created the item are removed before the page is sent.
- **Hide prompts** (optional) also blanks prompts, instructions, tool
  arguments and agent text. Scores, costs and steps stay visible.
- Leaderboard links keep the filters you had on (skill, model, benchmark,
  condition). The held-out board is never shareable.

Members create, copy and turn off links. Viewers see that links exist, but
not the links themselves, so they cannot copy one. Admins see every link of
the organization in [Settings > Shared links](/settings?tab=shares): what it
opens, who made it and when, its expiry and state, with **Revoke**.
The link looks like `https://<your heimdall>/s/<token>`. The token is signed
with a key kept in your organization's project folder, so it cannot be
guessed or moved to another item.

## Downloads

On an evaluation's Share dialog:

| Download | What it is |
|---|---|
| HTML | The report card as one standalone page, no scripts. |
| Markdown | The same tables, ready for a README, wiki or pull request. |
| PDF | Opens the print dialog on the HTML page: pick **Save as PDF**. |
| Reproduce bundle | A zip with the evaluation spec, each benchmark's experiment file and suite lock, the agent configs and pinned version snapshots, and `REPRODUCE.md` with the commands and the hashes a new run must match. |

People with a share link to an evaluation can download the HTML and
Markdown report card from the shared page too.

## Badge

After creating a link to a report card, evaluation or verdict, the dialog
shows Markdown for a badge:

```markdown
[![Heimdall report card](https://<your heimdall>/api/bench/shared/<token>/badge.svg)](https://<your heimdall>/s/<token>)
```

The badge shows the current score (or the verdict's action) and stops
working when the link expires or is turned off.

## API

| Route | Who | What |
|---|---|---|
| `POST /api/bench/shares` | member | `{kind, target, query?, label?, hide_prompts?, expires_in_days?}`; kind is `evaluation`, `report_card`, `verdict`, `run_trace` or `leaderboard`; no `expires_in_days` is 7 days, `0` (never) is for admins only |
| `GET /api/bench/shares?kind=&target=` | viewer | the links of one item, with state `active`, `expired` or `revoked`; a viewer's rows have no token or url |
| `GET /api/bench/shares` | admin | every link of the organization |
| `POST /api/bench/shares/{id}/revoke` | member | turn a link off |
| `GET /api/bench/shared/{token}` | anyone with the link | the item, redacted; 410 when expired or turned off |
| `GET /api/bench/shared/{token}/badge.svg` | anyone with the link | the badge |
| `GET /api/bench/shared/{token}/export?format=html\|md` | anyone with the link | a shared evaluation's report card |
| `GET /api/bench/evaluations/{id}/report/export?format=html\|md\|json` | viewer | the report card document |
| `GET /api/bench/evaluations/{id}/reproduce` | viewer | the reproduce bundle (zip) |
