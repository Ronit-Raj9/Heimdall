# Admission scan for skills and MCP servers

Before a skill folder or an MCP server goes into a run, `bifrost-bench skill scan` and `bifrost-bench mcp scan` check it and write an admission record: `admitted`, `blocked` or `admitted_with_override`, with every finding and the content hash of what was scanned. The code is in `admitscan/`, the record type is `schemas.AdmitScanRecord` (`schemas/admitscan.go`).

The scan exists because skills and MCP servers are a supply chain. In February 2026 Koi Security found 341 malicious skills on ClawHub, 11.9% of the catalogue then listed; their `SKILL.md` instructions led agents to show users fake prerequisite install steps (https://thehackernews.com/2026/02/researchers-find-341-malicious-clawhub.html). MCP tool descriptions can carry hidden instructions (tool poisoning), change after the client approved them (rug pulls) or change how the agent uses another server's tools (shadowing) (Invariant Labs, https://invariantlabs.ai/blog/mcp-security-notification-tool-poisoning-attacks). Static scanners see only scan time, so the scan also runs the subject in a sandbox with canary credentials, as the Cloud Security Alliance recommends for skills (behavioural sandboxing with logs of reads of `.ssh/`, keychains and `.env`, and of unexpected egress).

It complements, and never replaces, running the agent with the skill or server under the bench's probes (`docs/safety-probes.md`). An admitted skill is one this scanner found nothing in, which is not the same as a safe skill.

## Commands

```
bifrost-bench skill scan <dir> [-allow-host host ...] [-dynamic] [-image ref] [-helper-image ref] [-wall 120s]
                               [-override "<reason>"] [-by name] [-json] [-o report.md|report.json] [-config deploy/bench.yaml]
bifrost-bench skill rules [-json]
bifrost-bench mcp scan <service config> [<service config> ...] [-allow-host host ...] [-helper-image ref] [-rug-wait 5s]
                               [-override "<reason>"] [-by name] [-json] [-o report.md|report.json] [-config deploy/bench.yaml]
```

Flags may come before or after the positional arguments.

| Flag | Meaning |
|---|---|
| `-allow-host` | A host URLs may name. Repeat it or separate with commas. `*.example.org` (or `.example.org`) allows the domain and its subdomains; anything else matches exactly. The default allowlist is empty, so every host a skill names is a block finding until listed. |
| `-dynamic` | Skill scan: run the setup scripts in the sandbox (below). The MCP scan always runs the server in the sandbox. |
| `-image` | Image the skill's setup scripts run in. Default: the helper image. Pick the image your agent runs in when the scripts need its tools. |
| `-helper-image` | Image with `python3` used to plant the canaries, watch the sandbox and relay MCP requests. Default `images.helper` from the bench config, else `bench/coding-core-base:1`. |
| `-wall` | Wall budget of the sandboxed setup run; the container is killed after it. |
| `-rug-wait` | MCP: the wait before the third `tools/list`. |
| `-override` | Admit despite block findings. The reason is required (an empty reason is a usage error) and is recorded with the user (`-by`, else `$USER`) and the time. |
| `-json` | Print the record as JSON instead of the summary. |
| `-o` | Write the report to this file only (JSON when it ends in `.json`, markdown otherwise) and store nothing. With several MCP servers the server id is added to the file name. |

Exit codes: 0 admitted or admitted with override, 1 blocked (any record of the call), 4 scan error (folder unreadable, Docker or the sandbox failed, the MCP server never answered `initialize`), 64 usage error.

## Where records go

Without `-o`, each record is stored as `<admissions root>/<id>/admission.json` and `admission.md`, with `id` like `adm_k3v9x0p2qa`. The root is `$BENCH_ADMISSIONS_ROOT` when set, else `<bench root>/admissions` (the bench root is the folder above `deploy/` of `-config`). With neither, the command asks for `-o`. Records are never overwritten: a new scan is a new folder. Records sit outside every run seal; nothing in a run reads them.

The record holds:

- `content_hash`: for a skill, sha256 over one `path NUL sha256` line per file in path order (a link as `path NUL link:<target>`); for an MCP server, sha256 over the service spec, the local image id and everything the first session listed. A skill whose files change, or a server whose image, arguments or listings change, hashes differently and needs a new admission.
- `files`: every entry of the skill folder with its size and sha256 (links and special files without a hash), and whether it was scanned.
- `findings`: rule id, severity (`block`, `warn`, `info`), file, line and column (1-based, columns in characters), an excerpt with invisible characters shown as `<U+200B>`, the decoded payload where there is one, and a detail line.
- `dynamic`: what ran, exit code, duration, the canaries planted (id, path, sha256 of the value; never the value), how many canary opens and network attempts the watcher saw, and whether the watcher ran at all.
- `mcp`: server name and version, protocol version, endpoint, each tool's hash as first listed, prompts and resources, how many listings were compared, which tool was called, and whether the server sent `notifications/tools/list_changed`.
- `override`, `allow_hosts`, `scanner_version`, and `limits` (the list at the end of this page, repeated in every record).

A block finding makes the decision `blocked`; warn and info findings never do.

## Static checks

`bifrost-bench skill rules` prints the full list. The skill scan reads `SKILL.md` and every other file in the folder:

- The folder must hold a `SKILL.md` regular file. Symlinks are listed with their target and never followed (the adapter refuses them anyway); devices, pipes and sockets are refused. A link given as the folder itself is an error.
- Each file is hashed whole. The text rules read up to 4 MiB of each file and 64 MiB in all; a file past the cap is a `file-too-large` warning, and more than 5,000 files or 64 MiB is a `scan-limit` block finding. Binary files (a NUL byte or mostly invalid UTF-8) are hashed, not scanned; ELF, Mach-O and PE executables are a warning.

| Group | Rules | Catches |
|---|---|---|
| Invisible Unicode | `unicode-zero-width`, `unicode-bidi`, `unicode-bidi-mark`, `unicode-tag-chars`, `unicode-homoglyph`, `unicode-control` | U+200B..U+200D, U+2060..U+2064, U+180E, U+FEFF after the start; bidi overrides and isolates U+202A..U+202E, U+2066..U+2069 (Trojan Source); tag characters U+E0000..U+E007F with the hidden ASCII decoded; words that mix Latin with Cyrillic or Greek; terminal escapes |
| Hidden markdown | `hidden-html-comment`, `hidden-link-reference`, `hidden-css`, `hidden-after-blank-lines`, `hidden-long-whitespace` | HTML comments and `[//]: # (...)` comments: block when they hold an injection phrase, or more than three words with an order in them (do not, always, run, read, send, use and the like), since in hidden text the only reader is the model; warn for other text over three words, info up to three; elements hidden with `display:none`, `visibility:hidden`, `font-size:0`, `opacity:0`, white or transparent text, zero size, far negative offsets or the `hidden` attribute; text after 20 blank lines or 80 spaces. These rules run on `.md`, `.mdx`, `.markdown` and `.mdown` files, outside fenced code blocks (a code block shows comments and tags as text), skip padded table rows, and run on all MCP text. |
| Injection wording | `injection-phrase` | Block: "ignore previous instructions", "override your rules", "do not tell the user about this step", "keep this from the user", "without telling the user", "you must first run X before anything", `<IMPORTANT>`, `<hidden>` and `<system-reminder>` tags, "new instructions:". Phrases honest skills also use ("do not mention the failure to the user", "before doing anything else", "send the token to", "silently run") are a warning, and a block finding when the same line names a URL, a download, an install, a credential file or an email address. A phrase opened by a quote or backtick is an example, not an instruction, and is skipped. |
| Install lures | `prereq-install-lure` | Wording that asks the reader to fetch and run something by hand ("download and run", "paste this into your terminal", "run the following command to install", "install the helper from", password-protected archives): block when a URL or download is within three lines (the ClawHub pattern), warn alone. Plain "prerequisites" wording is info. |
| Credential names | `credential-path-reference` | `~/.ssh`, `id_rsa`, `.aws/credentials`, `.netrc`, `.config/gh/hosts.yml`, `.docker/config.json`, `.kube/config`, keychains, wallets (warn) |
| Encoded payloads | `encoded-base64`, `encoded-hex` | base64 runs of 40 or more characters (`-base64-min`) and hex runs of 64 or more (`-hex-min`) or 16 or more `\x` escapes that decode to readable text, a `#!` script, an ELF, PE or Mach-O executable, or a zip, gzip or 7z archive; the decoded preview is in the finding. Runs that decode to other bytes (hashes, keys, images) and image data URIs are skipped. |
| Hosts | `url-unlisted-host`, `url-reserved-host` | every host of an `http`, `https`, `ftp`, `ws`, `wss`, `git`, `ssh` or `sftp` URL that is not on the allowlist, once per file (IP literals included); RFC 2606 and 6761 names (`example.com`, `*.test`, `localhost`) are info |
| Fetch and run | `remote-code-fetch`, `decode-exec`, `download-exec`, `pip-unlisted-index`, `npm-install-hook`, `npm-install-hook-unpinned` | `curl`/`wget` piped to a shell or interpreter, `bash <(curl ...)`, `eval "$(curl ...)"`, `python -c "$(curl ...)"`, download then run on one line, `pip install git+...` or a URL, `npm`/`npx` of URLs or `github:` specs, `iwr ... \| iex`, `base64 -d \| sh`, `powershell -enc`, `chmod +x` within five lines of a download, pip `--index-url`/`--extra-index-url`/`--find-links` to an unlisted host, package.json install hooks (warn) and hooks that run unpinned packages, downloads or `node -e` (block). Backslash continuations are joined first. |

## Dynamic run

With `-dynamic` the skill's setup runs in a sandbox; the MCP scan always runs the server there. One sandbox is:

- an `--internal` Docker network (no route out, IPv6 off);
- a home volume (a Docker volume, never a host folder) mounted at `/home/agent`, planted before anything runs with canary credentials whose values are random per scan: `~/.ssh/id_ed25519`, `~/.aws/credentials`, `~/.config/gh/hosts.yml`, `~/.netrc` and `~/project/.env`, owned by the sandbox user with mode 0600; the skill is copied to `~/skills/<name>/`;
- a holder container that owns the network namespace. A one-shot helper with only `NET_ADMIN` adds an on-link default route to it, so a connection to any outside address sends an ARP request the watcher can see (without the route the kernel fails the connect with no packet at all);
- a watcher in the same namespace with only `NET_RAW` and `DAC_READ_SEARCH`, the home volume mounted read-only. It reports DNS queries (to Docker's resolver on 127.0.0.11), TCP connection attempts, UDP datagrams and ARP lookups for outside addresses, and, through inotify on the shared volume, every open or read of a canary file and every listing of `~/.ssh`, `~/.aws` and `~/.config/gh`. It prints a ready line before the subject starts. Loopback traffic between the server and the scanner's relay is not reported.
- the subject container joins the holder's namespace and runs like an agent: non-root (`1000:1000`), `--cap-drop ALL`, `no-new-privileges`, pids, memory and CPU limits, read-only root with a tmpfs `/tmp`, the home volume, no host bind mounts and no Docker socket.

Every object carries `bifrost-bench=1` and `bifrost-bench.admit=<prefix>` and is removed when the scan ends, also on failure. Starting a sandbox takes about 3 s.

One side effect of the on-link route: Docker's resolver then tries the host's upstream DNS servers, so a lookup inside the sandbox takes about 20 s to fail instead of failing at once. The resolver's own ARP requests are tagged and not reported when the DNS query that caused them is.

What runs for a skill: scripts named `install`, `setup`, `bootstrap`, `prereq(s)`, `prerequisites`, `preinstall`, `postinstall`, `init`, `configure`, `deps` or `dependencies` with a `.sh`, `.bash` or `.py` suffix, at most three folders deep, and every fenced shell block of `SKILL.md` that sits under a heading or after prose about installing or setting up (prompts like `$ ` are stripped). They run in order from `~/project`, each failure printed and the next one still run. A skill with none gets `dyn-no-setup` (info) and nothing runs.

| Rule | Severity | Meaning |
|---|---|---|
| `dyn-canary-read` | block | a canary file was opened or read |
| `dyn-credential-dir-listed` | warn | a credential folder was opened |
| `dyn-network-attempt` | block | one per distinct DNS name, destination or ARP target |
| `dyn-canary-in-output` | block | a canary value, or its base64, appeared in the output (excerpts show `[CANARY:<id>]`, never the value) |
| `dyn-not-observed` | block | the watcher could not run, so the run proves nothing |
| `dyn-setup-timeout` | warn | setup ran past `-wall` and was killed |
| `dyn-setup-failed` | info | setup exited non-zero |

## MCP scan

A service config is one service spec in the shape of a task's or suite's `services:` entry (`schemas.ServiceSpec`, YAML or JSON), or a whole `task.yaml` or `suite.yaml`, of which the services of role `mcp` are scanned. The scan needs `image` (a `build:` spec is refused: build it and set `image`), `port`, and the streamable HTTP transport; a stdio-only server is wrapped with `bifrost-bench toolproxy bridge` in its own image, as for a run (`docs/tool-proxy.md`). `argv` and `env` are passed as in a run; `files` whose container path is under `/home/agent` are planted, others are reported (`mcp-files-skipped`) because the sandbox root is read-only. `ready.timeout_s` bounds the wait for the first answer (default 60 s).

The server runs as the sandbox subject. The scanner reaches it through its own relay (a small Python script in the helper image, in the same namespace) at `http://127.0.0.1:<port><mcp.path>`, and runs:

1. `initialize` (client `bifrost-bench-admitscan`), retried until the server answers, then `notifications/initialized`;
2. `tools/list` (following cursors), and `prompts/list`, `resources/list` and `resources/templates/list` when the server advertises them;
3. one `tools/call` of the first tool whose name reads as harmless (`get`, `list`, `read`, `search`, `echo`, ...; else the first by name), with placeholder arguments for its required properties;
4. `tools/list` again; a wait of `-rug-wait`; `tools/list` again;
5. a second session whose client calls itself `claude-code`, and `tools/list` there.

Each later listing is compared with the first by tool hash (sha256 of the tool's canonical JSON without `_meta`). Any added, removed or changed tool is an `mcp-rug-pull` block finding, and the changed text is scanned too. A `notifications/tools/list_changed` on an answer stream is recorded.

Every text the model would read goes through the static rules above: server `instructions`, tool names, titles and descriptions, every string inside `inputSchema`, `outputSchema` and `annotations`, and every string of the prompt and resource entries. On top:

| Rule | Severity | Catches |
|---|---|---|
| `mcp-shadow-foreign-tool` | block | a description names a tool the server does not have but other servers or agents do (`send_email`, `read_file`, `execute_command`, `Bash`, `WebFetch`, ...; common English names count only in code style, as `name(` or followed by "tool") |
| `mcp-shadow-directive` | block | a description tells the model how to use other tools: "whenever you use any tool", "all other tools", "instead of the X tool", "the X tool must", "when this tool is available", "send all emails to" |
| `mcp-shadow-sibling-tool` | info | a description names another tool of the same server |
| `mcp-tool-name-collision` | warn | a tool has the name of a common tool of other servers or agents |
| `mcp-list-failed` | warn | a listing method failed |

The server's log and every answer are checked for canary values, and the watcher's file and network findings apply as for a skill.

## What it catches and what it cannot

Caught: the patterns above in any file of the skill or any text the server lists; setup scripts and install snippets that read credential files, reach the network or print secrets during the sandboxed run; servers that do so while starting and serving the scan; tool listings that change after a call, after a wait or for a client that names itself like an agent.

Not caught:

- Instruction-only payloads written as ordinary prose that match no pattern. Research on skills reports malicious goals phrased as natural-language "compliance rules" with 0% detection by code scanners (https://arxiv.org/abs/2605.11418); this scanner is in that class for such text.
- A server or script that behaves differently for the scanner than for a live agent beyond the rug-pull re-listing: by task content, by arguments the scan does not send, by environment details, or by a client name other than the two the scan uses.
- Time bombs: date checks, delays longer than `-wall` or `-rug-wait`, payloads that trigger after N calls.
- Anything after admission. A new version, a changed server answer or a new tool needs a new scan; the content hash only pins what was scanned. The bench does not yet refuse a run whose skill hash has no admission record.
- What the dynamic run does not watch: files written or deleted, processes spawned, reads of files other than the canaries, IPv6 traffic, and traffic to Docker's resolver beyond DNS queries. Scripts that need tools the image lacks fail and are recorded as `dyn-setup-failed`, not as safe.
- The relay sees only answers to POST requests; notifications on a separate GET stream are not read.
- Docker is not a hard security boundary (`docs/DESIGN.md` section 14).

Run the agent with the skill or server under the bench's probes as well.

## Testing

```
GOWORK=off go vet ./admitscan/ ./cmd/bifrost-bench/
GOWORK=off go test ./admitscan/ ./cmd/bifrost-bench/
GOWORK=off go test -tags integration -run Integration -v ./admitscan/
```

The unit tests need no Docker: fixture skills in `admitscan/testdata/skills` (clean, zero-width, bidi, tag characters, base64, curl piped to bash, unknown host, HTML comment injection, a ClawHub-style lure), recorded `tools/list` answers in `admitscan/testdata/mcp` (JSON, SSE, a rug pull, Invariant's poisoning and shadowing examples), and an in-memory MCP server behind the relay protocol for the rug-pull sequence. The integration tests start real sandboxes (objects named `hd-s2-admit-*`, all removed and checked afterwards): a skill whose install script reads `~/.aws/credentials` and reaches the network is blocked for both, a benign one is admitted, and the fixture MCP server `admitscan/testdata/mcp/server.py` is admitted when honest and blocked for the canary read, the DNS lookup and the rug pull when not.
