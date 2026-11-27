# Secrets and keys on the website

Two kinds of credential, both stored encrypted in the server keystore and
never shown again after saving.

## Model keys

[Settings > Model keys](/settings?tab=keys), then **Add API key** on your provider's card:

- Provider, label, API key. Optional base URL (needed for some providers).
- **Allowed models**: comma separated; empty allows any model.
- **Monthly budget (USD)**: 0 means no limit. A key past its budget shows
  `over budget` in the table.
- **Test** asks the provider for its model list with the key (free; Sarvam has no list call, so it sends one tiny language check, which may be billed). No chat or completion request is sent. Status: `untested`, `works`, `refused`, `error`.

Runs reach models only through the gateway on the worker: the worker receives
your key for the length of the run and keeps it in memory in its proxy. The
agent container gets a per-run key, never your provider key. This holds for
every worker, the shared one and your own (Settings > Workers). **Edit** with an
empty key field keeps the stored key. **Delete** makes runs using it fail.

Spend per key: [Settings > Usage](/settings?tab=usage), Provider keys section.

## Budgets

Three layers, smallest wins:

- per key: the monthly budget above;
- per experiment: **Budget (USD)** under Budget and limits in the builder;
- per org: the daily spend quota the server admin sets
  ([web-admin.md](web-admin.md)). Schedules and canaries also carry a daily cap.

## Org secrets

[Settings > Secrets](/settings?tab=secrets) (admins add, rotate and delete; members see names only):

- Name: upper case letters, digits and underscores, starting with a letter,
  max 64 (for example `TAVILY_API_KEY`). Some prefixes are reserved for the server.
- Value: up to 16 KiB, pasted once and never shown again. **Rotate** replaces it; **Delete** makes
  runs using it fail until it is added again.

### In tool routes

An external API in a suite (`external:` in `suite.yaml` or `task.yaml`)
names its credential with `auth_env`. When the org has a secret of that name,
the tool proxy injects its value on the way out. The agent never sees it, and
it is redacted from traces. Edit the suite in **Suites**, **Edit** tab.

```yaml
external:
  - id: fx
    base_url: https://api.fx.example/v1
    allow:
      - {methods: [GET], path_prefix: /rates}
    auth_env: FX_API_KEY        # an org secret named FX_API_KEY
```

Details: [external-apis.md](external-apis.md).

### In agent env

Do not paste credentials into an agent's **Environment** in the Config tab:
`agents/<name>.yaml` is a project file every member can read and every save
is versioned. Route the credential through a tool route as above, so the
proxy holds it. Webhook URLs for schedule alerts are also stored as secrets.
