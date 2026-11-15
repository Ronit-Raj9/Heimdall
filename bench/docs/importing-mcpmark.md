# Importing MCPMark

`bifrost-bench import mcpmark` turns the filesystem tasks of
[MCPMark](https://github.com/eval-sys/mcpmark) into a suite of track `mcp`.
In each task the agent works on a folder of files through the same MCP server
MCPMark uses, the folder is exported when the agent is done, and MCPMark's own
verify script decides the outcome, offline.

Only the filesystem family runs fully local. The other families are listed as
skipped with the reason; see [What is not imported](#what-is-not-imported).

## What upstream is

Checked against commit `cd45b7f57923b9b3985467f5139927575f83141c`
(12 Jun 2026). License: Apache-2.0.

- Tasks live in `tasks/<family>/<difficulty>/<category>/<task>/`, where
  difficulty is `easy` or `standard`. Each task folder holds
  `description.md` (the instruction), `meta.json` (ids, a difficulty `L1` to
  `L3`, tags, the MCP servers used) and `verify.py`.
- Families at this commit: filesystem (40 tasks: 10 easy, 30 standard),
  github (33), notion (38), playwright (4), playwright_webarena (31),
  postgres (31). The `supabase` and `insforge` folders hold only empty
  category folders.
- A filesystem task runs in a test environment: the folder of its category
  (`meta.json` `category_id`), downloaded on first use from
  `https://storage.mcpmark.ai/filesystem/<category>.zip` and copied fresh for
  every task. The ten categories are 10 KB to 31 MB zipped, about 52 MB in all.
  Some tasks are about file modification times, which the archives carry.
- The MCP server is `@modelcontextprotocol/server-filesystem@2025.12.18`
  (pinned in `src/agents/base_agent.py`), started over stdio with the test
  environment as its only allowed folder.
- `verify.py` reads the environment variable `FILESYSTEM_TEST_DIR`, checks the
  folder with the Python standard library only, and exits 0 on a pass. Some
  scripts hard-code the expected answers for the archive's content.
- MCPMark ships no reference solutions.

## Getting the data

```
git clone --depth 1 https://github.com/eval-sys/mcpmark.git datasets/mcpmark
mkdir -p datasets/mcpmark/zips datasets/mcpmark/test_environments
cd datasets/mcpmark
for c in desktop desktop_template file_context file_property folder_structure \
         legal_document papers student_database threestudio votenet; do
  curl -fsSL -o zips/$c.zip https://storage.mcpmark.ai/filesystem/$c.zip
  unzip -q -o zips/$c.zip -d test_environments
done
rm -rf test_environments/__MACOSX
```

`datasets/` is git-ignored. A task whose category folder is missing is skipped
with the download command in the reason.

## Importing

```
bifrost-bench import mcpmark -from datasets/mcpmark -suite mcpmark-fs
bifrost-bench suite lint mcpmark-fs
```

Settings (`-set key=value`):

| Key | Default | Meaning |
|---|---|---|
| `difficulty` | `all` | `easy`, `standard` or `all` |
| `state_dir` | `<from>/test_environments` | where the unzipped category folders are |
| `references` | none | folder of your reference controls, `<task id>.sh` each |
| `families` | `filesystem` | any other family is refused with its reason |
| `ref` | the clone's HEAD | upstream commit, when `-from` is not a git clone |

`-ids` takes bench ids (`fs-standard-file-context-uppercase`) or upstream ids
(`filesystem/standard/file_context/uppercase`). Task ids are
`fs-<difficulty>-<category>-<task>`.

## What a task looks like

```
tasks/fs-easy-file-context-uppercase/
  task.yaml
  instruction.md          where the files are, then upstream's description
  repo/README.md          the workspace is not part of the result
  service/Dockerfile      the MCP server image
  service/state/...       the category's test environment, file times kept
  verifier/tests/verify.py      upstream, hidden from the agent
  verifier/tests/runverify.py   unpacks the exported state, runs verify.py
  verifier/tests/meta.json
  controls/reference.sh  controls/empty.sh  controls/exploit.sh
```

- **Service** `fs`, role `mcp`, port 8080. Its image is node with the pinned
  filesystem server, run behind `bifrost-bench toolproxy bridge` (the server
  speaks only stdio), over `/data`, which holds the test environment. The
  image is built at the first run with network (`environment.build_network`),
  on top of the bench image (`BENCH_IMAGE` build argument, default
  `bench/bifrost-bench:dev`), which must carry the `toolproxy bridge` command.
  The agent reaches it through the tool proxy, so every call is in the
  ledger and tool probes apply.
- **Output** `state`: `/data` is exported as `state/fs/data` (a tar), with a
  size cap of twice the environment plus 16 MiB.
- **Verifier**: `python3 /grader/tests/runverify.py` in the task image
  (`python:3.12-slim`, no network). It unpacks `/grader/state/fs/data` into a
  scratch folder, runs upstream `verify.py` with `FILESYSTEM_TEST_DIR` set to
  it, and writes `/grader/out/reward.json`: 1 when `verify.py` exits 0, else 0,
  with the exit code as a dimension. With no exported state it writes no
  reward, so the grade is undecided, not a failure.
- **Controls**: `empty.sh` does nothing (the untouched folder must fail);
  `exploit.sh` claims success without a tool call. `reference.sh` refuses
  (exit 1) unless you supplied one: upstream has none, and a task is trusted
  only after its own reference passes here.
- `difficulty` comes from `meta.json`: L1 easy, L2 medium, L3 hard.

## Writing a reference control

A reference runs in the agent's place with the services up and must leave the
folder in a passing state. It reaches the server at the URL in
`BENCH_MCP_SERVERS` (`{"fs": {"url": "http://toolproxy:9200/s/fs/mcp"}}`).
A minimal JSON-RPC client in Python is enough: `initialize`, then
`tools/call` with `read_text_file`, `write_file`, `create_directory`,
`move_file` and the other tools `tools/list` names. Put it in a folder as
`<task id>.sh` and import with `-set references=<folder>`, or write it to the
task's `controls/reference.sh`.

This was checked end to end for `fs-easy-file-context-uppercase` with the
real images: a scripted client through the tool proxy and the bridge to the
upstream server (14 ledger rows), the folder exported with `docker cp`, and
`runverify.py` in `python:3.12-slim`: reward 1 for the solved folder, 0 for
the untouched one, no reward with no state.

## What is not imported

| Family | Why |
|---|---|
| postgres | needs a PostgreSQL service restored from upstream's database dumps (`storage.mcpmark.ai/postgres/<db>.backup`), `postgres-mcp` behind the bridge, and a verifier that restores the exported dump into a live database before `verify.py` connects. Not built yet. |
| github, notion | need a hosted account, a token and the network at run time |
| playwright | drives a browser against a hosted site (`eval-web.mcpmark.ai`) |
| playwright_webarena | needs the WebArena site containers and a browser service |
| supabase, insforge | no tasks at this commit |

## Not comparable with the leaderboard

MCPMark's own harness runs its agent with its own prompts and turn limits.
Here the agent is yours, the instruction gets a short preamble naming the
server and `/data`, and the server is reached through the bridge and the
proxy. The task, the files, the server version and the verdict of
`verify.py` are upstream's; scores from a different agent harness are not
the published ones.
