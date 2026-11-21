# Quickstart on the website

From zero to a verdict in the browser, no CLI. About five minutes plus the
run time of a few small tasks.

## 1. Sign in

1. Open the Heimdall URL your server owner gave you.
2. On **Sign in to Heimdall** press **Continue with GitHub**. A new account
   gets its own organization with you as owner.
3. No GitHub? Use **Create one** (email and a password of 12 or more
   characters), or the judge login your server owner handed you.

If GitHub says the account has no Heimdall account and sign-up is closed,
ask the owner for an invitation link: it opens **You are invited**.

## 2. Add a model key

1. Left nav, **Build > Models & keys**, then **Add a model key**.
2. Pick the provider, paste the API key, optionally set **Allowed models**
   and a **Monthly budget (USD)** (0 means no limit).
3. Press **Save key**, then **Test** on its row. Status must read `works`.

The key is stored encrypted in the server keystore. After saving, only the
label is shown. Details: [web-keys-secrets.md](web-keys-secrets.md).

## 3. Run the welcome wizard

A new organization lands on **Welcome to Heimdall** (or press **Start the
wizard** on Home). Five steps:

| Step | What to pick | If empty |
|---|---|---|
| Suite | a recommended suite | **Browse the catalog**, press **Add** |
| Agent | an agent | **Create agent from template** |
| Model | a model you have a key for | **Add a model key**, Test, come back |
| Run | review, then **Run benchmark** | read-only role: ask an owner for member access |
| Verdict | wait; you may leave the page | Home shows progress |

The first run is small: a few tasks, one trial each. It runs on your
organization's runner, or the shared runner if the server has one
([web-runners.md](web-runners.md)).

## 4. Read the verdict

The verdict step shows one of four headlines:

- **Better than the baseline**: difference larger than noise. Safe to release.
- **Worse than the baseline**: hold the release.
- **No clear difference yet**: run more tasks or trials (Experiment, Actions tab, **Extend**).
- **The run had harness problems**: fix what the full results page shows, run again.

**Open full results** opens the experiment page: verdict card (ACTION: SHIP,
HOLD, NO DECISION or FIX HARNESS), gate checklist, task grid, cost. From
there: **Compare another model**, or **Release** to record a release on this
evidence. Next: [web-analysis.md](web-analysis.md).

## Where next

- Your own agent: [web-agents.md](web-agents.md)
- Your own tasks: [web-suites.md](web-suites.md)
- Bigger experiments: [web-experiments.md](web-experiments.md)
- Every CLI command and its button: [CLI_WEB_PARITY.md](CLI_WEB_PARITY.md)
