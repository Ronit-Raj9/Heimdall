# Agents on the website

An agent is `agents/<name>.yaml` in your project: adapter, image, model,
limits, env, command. Everything below is under **Build > Agents**.

## Create from a template

1. **Agents**, then **New agent** (or **Create agent from template**).
2. **1. Pick a template**: Coding CLIs (Codex, Claude Code, mini-swe and
   more), Frameworks, Write your own, Bring your own. A template that
   **Builds an image** needs a runner build step.
3. **2. Name and model**: lowercase name, a model from your keys, optional
   notes. **Preview YAML** shows the files it writes.
4. **Create agent**.

Every new org starts with the starter agents listed on the Catalog page.

## Edit

Open an agent. Tabs: **Config**, **Checks**, **Tools**.

- **Config**: **Form** for common fields (model, gateway provider, wall
  clock limit, CPUs, memory, turn limit, endpoint URL for remote agents,
  env, command) or **YAML** for everything. **Validate** uses the planner's
  parser. **Save** writes the file; every save is versioned in Project files.
- Never paste a credential into **Environment**: the file is readable by
  every member. Use org secrets ([web-keys-secrets.md](web-keys-secrets.md)).

## Skills

**Tools** tab, skills section. A skill is a folder holding `SKILL.md`.
Upload it as a zip: it is admission-scanned, unpacked to
`agents/<name>/skills/`, and the config's `skills.extra` is updated so runs
install it. Delete removes it. A warning shows if the config does not list
the folder yet.

## MCP tools

**Tools** tab, MCP section. Edit `agents/<name>/mcp.yaml`: each server has an
image pinned by digest and an `mcp` block (path, format). Runs start each
server beside the agent behind the tool proxy, which records and can fault
every call.

## Check

**Checks** tab:

- **Check** (`agent.check`): a free conformance run of the agent on a tiny
  task. On a hosted server it runs as a small experiment on a runner. No
  runner online: the job says so and links Runners.
- **Build** (`agent.build`): builds the per-suite image. Local servers only;
  hosted orgs use the template's prebuilt image or a custom image the server
  admin approves.
- **Diff**: compare two versions of the agent config (also **Agents > Diff**).

Next: [web-suites.md](web-suites.md).
