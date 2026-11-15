# Agent skills

A skill is a folder holding a `SKILL.md` (front matter with `name` and `description`, then instructions) and any files it refers to. Claude Code, Codex and OpenCode each read skills from a folder of their own and offer them to the model by name and description; the model opens a skill when the task calls for it. The bench installs skills per run, records their content in the manifest, and can compare the same agent with and without them.

## Where skills come from

| Source | Field | Resolved against |
|---|---|---|
| The task | `skills:` in `task.yaml` (one folder) | the task folder |
| The agent config | `skills.extra:` (a list of folders) | the bench root |

Each folder is either one skill (it holds `SKILL.md`) or a collection whose subfolders are skills. In a collection every visible subfolder must hold `SKILL.md`; hidden entries (`.git`) and loose files (`README.md`) are skipped. A skill's folder name is its name in the container and must match `[A-Za-z0-9][A-Za-z0-9._-]*`.

The adapter refuses, at plan time and again at run time:

- a path that leaves its root (the task folder or the bench root), symlinks included;
- a skill folder without `SKILL.md`, or whose `SKILL.md` is not a regular file;
- any symlink inside a skill folder or a collection (the folder is copied from the host, and a link could carry a host file into the agent's container);
- two skills with the same name (task and config, or two config entries).

## Where they are installed

```yaml
skills:
  enabled: true          # default; false installs none
  dir: ""                # empty: the CLI's own folder (table below)
  extra: [skills/house-style, skills/shared]
```

| Agent (by `parser:`) | Folder | Discovers it natively | Checked with |
|---|---|---|---|
| Claude Code (`claudecode`) | `$CLAUDE_CONFIG_DIR/skills` (`/home/agent/.claude/skills`) | yes | the `skills` list of the stream-json `system/init` line, Claude Code 2.1.325 |
| Codex (`codex`) | `$CODEX_HOME/skills` (`/home/agent/.codex/skills`) | yes | `codex debug prompt-input` lists the skill under root `r0`, codex-cli 0.169.0 |
| OpenCode (`opencode`) | `$XDG_CONFIG_HOME/opencode/skills` (`/home/agent/.config/opencode/skills`) | yes | `opencode debug skill`, OpenCode 1.19.5 (`skill/` works too) |
| anything else | `/opt/bench/skills` | no | the agent finds them only through `BENCH_SKILLS_DIR` |

The command adapter runs every CLI, so the agent config's `parser:` is what names the CLI. `CLAUDE_CONFIG_DIR`, `CODEX_HOME` and `XDG_CONFIG_HOME` are read from the config's `env:` when it sets them to a literal absolute path; otherwise the image defaults above apply. `skills.dir` overrides the folder for any agent and must be absolute and outside the workspace (skills in the workspace would land in the diff).

Each skill becomes one `RunPlan.Dirs` entry, `<folder>/<name>` to the host folder, which the environment copies in read-only before the agent starts, and the plan sets `BENCH_SKILLS_DIR=<folder>`. The instruction is never changed: an agent that does not discover skills on its own gets nothing but the variable. A run with no skills, or with `skills.enabled: false`, gets exactly the plan it had before skills existed (golden tests in `adapters/testdata/plans` pin this).

All three CLIs follow a symlinked skill folder, so an environment may also link `<folder>/<name>` to a copy elsewhere.

### Skills the CLIs bring themselves

The "without skills" arm is not skill-free:

- Codex installs its bundled skills (`imagegen`, `openai-docs`, `skill-creator`, `skill-installer`) in `$CODEX_HOME/skills/.system` on start.
- Claude Code 2.1.325 offers bundled skills (`deep-research`, `dataviz`, `code-review` and others); `CLAUDE_CODE_DISABLE_BUNDLED_SKILLS` exists in the binary but was not tested here.
- OpenCode has one built-in skill (`customize-opencode`). The shipped config sets `OPENCODE_DISABLE_EXTERNAL_SKILLS=1` and `OPENCODE_DISABLE_CLAUDE_CODE=1`, so it never reads `~/.claude/skills` or `~/.agents/skills`.

Both arms of a comparison get the same bundled skills, so the paired difference is the installed ones.

## Content in the manifest

Two records pin what an arm installs:

- The planner adds, per task, `"<task id>:<container path>": <sha256>` for every file of every skill the arm gets to the arm's `files` map in the manifest (`adapters.AddSkillHashes`), only for arms on the command adapter, and only when the arm installs skills. A skill whose content changes changes the manifest hash.
- The suite lock hashes the task's skills folder, and the manifest's `scenario` map hashes each `skills.extra` folder per arm.

`skills.enabled` and `skills.extra` are part of the agent config, so two arms that differ in skills never share a config hash.

## A skill comparison

Two arms, one config, one difference:

```yaml
arms:
  A: {label: no skills, set: {skills.enabled: false}}
  B: {label: skills}
```

The usual paired test decides it.

## Metric: `skill_files_read`

Report only. Distinct files inside the installed skill folders that the agent's own stream shows it reading:

- every `tool_call` target that names a path `<folder>/<skill>/<file>` (a Claude Code `Read`, an OpenCode `read`, a Codex or OpenCode shell command such as `cat ~/.codex/skills/pdf/SKILL.md`); the folder is matched as an absolute path and as `~/`, `$HOME/` and `${HOME}/`;
- each distinct call of a tool that loads a skill by name (Claude Code `Skill`, OpenCode `skill`). The parsers give no target for these calls, so they are told apart by their arguments, and a bundled skill loaded this way counts too.

Hidden folders (Codex's `.system`), listings of a folder and glob patterns are not reads. The value is `not_applicable` when the run installed no skills, `unknown` when the agent's stream produced no events (no parser), and a lower bound otherwise: a file reached through a relative path or a variable the metric cannot expand is missed.
