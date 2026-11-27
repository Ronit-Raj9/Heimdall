# Quickstart on the website

From zero to a report card for your own agent, in the browser, no CLI. About
ten minutes of clicking plus the run time of the benchmarks you pick.

Heimdall benchmarks **your agent**: you register it once, pick benchmarks,
and get one report card per agent version and model. The page that does it
is **Evaluate your agent** (`/evaluate`).

## 1. Sign in

1. Open the Heimdall URL of your server.
2. On **Sign in to Heimdall** press **Continue with GitHub**, or use email
   and a password of 12 or more characters (**Create one** on the sign-in
   page, when the server allows sign-up).
3. A new account gets its own organization with you as owner.

If sign-up is closed, ask an owner of your organization for an invitation
link: it opens **You are invited**.

## 2. Add your model key

Runs use your organization's own provider keys. Heimdall never pays for
model calls with a key of its own. An evaluation always uses your own model
key: with no working key for its provider it is refused before anything runs.
To check that your agent runs at all for free, use Check agent on the agent's
page: one task with a practice model, no API key, no model cost.

1. Open [Settings > Model keys](/settings?tab=keys) from the sidebar.
2. Press **Add key** on your provider's card.
3. Paste the API key; optionally set **Allowed models** and a monthly spending limit in USD (0 means no limit).
4. Press **Save key**.
5. Press **Test** on the saved key: it asks your provider for its model list (free; Sarvam has no list call, so it sends one tiny language check), and the status must read `works`.

The key is stored encrypted on the server. After saving, only its label is
shown, and nobody can read the value back. A worker gets it only for the
length of a run it was handed, never for anything else. Details:
[web-keys-secrets.md](web-keys-secrets.md).

## 3. Register your agent

1. Open [Agents](/agents) from the sidebar.
2. Press **New agent** and start from a template (table below).
3. Save it: saving makes no model call.

| Your agent is | Template |
|---|---|
| A CLI coding agent (Codex, Claude Code, OpenCode, Gemini CLI, mini-swe-agent) | the matching built-in agent |
| An HTTP service you run | **Your HTTP endpoint**, **OpenAI-compatible endpoint** or **A2A agent card** |
| A program you can start from a shell | **Any CLI** or **Custom Python** |
| A Docker image | **Your Docker image**, pinned by digest |
| A config file you already have | upload it on the **Upload** page |

Every saved change to an agent is a new **version**, so a report card always
names the exact version it measured. You can skip this step and evaluate a
built-in agent first. More: [web-agents.md](web-agents.md).

## 4. Evaluate your agent

1. Press **Evaluate an agent** at the top of the page, or **Evaluate your agent** on Home ([open](/evaluate)).
2. Step **Your agent**: pick your agent and its version.
3. Step **What to learn**: pick **How good is my agent?** for a first score.
4. Step **Benchmarks**: pick a pack or single benchmarks; ones your agent cannot run are marked and left out.
5. Step **Model and setup**: pick the model, the number of tasks and repeats, and a spending cap.
6. Step **Launch**: read the estimate of runs, cost and time.
7. Press **Launch evaluation**: this is the step that costs money, billed by your provider to the key from step 2.

| Step | What you pick | Tip |
|---|---|---|
| Your agent | one of your agents, or a built-in one, and a version | each card shows what the agent can do (shell, tools, browser...) |
| What to learn | **How good is my agent?**, **Is my new version better?**, **Which model works best with my agent?** or **How does my agent compare to a well-known agent?** | the first gives a score; the others run two setups side by side |
| Benchmarks | a pack for one skill (coding, terminal, support, memory, research, voice, safety) or single benchmarks | benchmarks your agent cannot run are marked and left out |
| Model and setup | the model, tasks per benchmark, repeats per task, faults, a spending cap | only providers you have a key for can be picked |
| Launch | review the estimate (runs, cost, time), then **Launch evaluation** | read-only role: ask an owner for member access |

**Faults** run the same tasks again with something going wrong (the model
goes down, a tool breaks, misleading instructions), so the report card shows
how your agent copes, not just how it does on a good day.

## 5. Where it runs

Runs execute in containers on a **worker**, never inside the web server.
Every organization can use the server's shared worker with the built-in
agents and benchmarks: nothing to set up. To run on your own machine or
cloud account instead, connect your own worker in [Settings > Workers](/settings?tab=runners)
([web-runners.md](web-runners.md)).

## 6. Read the report card

Find your evaluation under [Evaluations](/evaluations) in the sidebar, which lists every evaluation. Each one has a live
page that turns into its **report card** as benchmarks finish:

- **Score** with a likely range per benchmark and per skill. Few runs give a
  wide range.
- **Cost**, tokens, steps, tool calls and time per run. A model with no
  official price shows cost as unknown instead of a guess.
- **When things go wrong**: the score under each fault and how far it
  dropped.
- **Safety**: attacks resisted and cheating caught.
- For two setups: who wins on each benchmark and overall, or **No clear
  winner yet** when the evidence cannot tell them apart. Add tasks or
  repeats and run again.

**Open comparison** on a benchmark row opens the full results: verdict, task
grid, every run with its trace and evidence.

## 7. Share it

**Share** on the report card makes a read-only link for people outside your
organization: pick how long it lasts, copy it, revoke it any time. Secrets
are never in a shared page, and prompts can be hidden too. From the same
menu you can download the report card as HTML, Markdown or PDF, copy a
badge for a README, or download a **reproduce bundle** with the exact
evaluation spec, agent version, benchmark locks and commands. More: [sharing.md](sharing.md).

## The same from the command line

Prefer a terminal? The command line path is in [quickstart.md](quickstart.md),
and every command's place on the website is in [CLI_WEB_PARITY.md](CLI_WEB_PARITY.md).

## Where next

- Your own tasks: [web-suites.md](web-suites.md)
- Compare runs in depth: [web-experiments.md](web-experiments.md),
  [web-analysis.md](web-analysis.md)
- Everything about evaluations: [evaluations.md](evaluations.md)
