# Connect an agent from the terminal

Three commands take an agent to a first report card: log in, connect, look.

```sh
heimdall login -url https://heimdall.example.com   # a code in the terminal, approve in the browser
heimdall connect                                   # inside the agent's repository, or: heimdall connect <url | "curl ...">
# connect registers the agent, checks it, then offers the first look and prints the report card link
```

## heimdall login

Device login (RFC 8628). The CLI prints a short code and opens `https://<server>/device?code=...`; where you are signed in, check the code and approve. The server mints an org API token in your current organization (scope `evaluate` by default, `-scope read` or `-scope admin` on request, 90 days) and hands it to the terminal once. It is stored in `~/.config/heimdall/credentials.json` with mode 0600 (`$HEIMDALL_CONFIG_DIR` or `$XDG_CONFIG_HOME/heimdall` when set) and shows under [Settings > Access tokens](/settings?tab=tokens) as "CLI login: heimdall CLI on <host>", where you can revoke it.

- CI or a pasted token: `echo "$HEIMDALL_TOKEN" | heimdall login -with-token -url ...`.
- A local server has no accounts: pass its write token with `-with-token`.
- `HEIMDALL_URL` and `HEIMDALL_TOKEN` always win over the stored login. `heimdall logout` forgets the stored token.

Server side: `POST /api/bench/auth/device/code` and `POST /api/bench/auth/device/token` are public (JSON or form bodies; the token endpoint answers `authorization_pending`, `slow_down`, `access_denied`, `expired_token`); `GET /api/bench/auth/device?user_code=`, `POST /api/bench/auth/device/approve` and `/deny` need a signed-in browser session. Pending codes live in memory for 10 minutes, so a restart only means running `heimdall login` again.

## heimdall connect

| Input | What happens |
|---|---|
| nothing, or a folder | reads the repository (files only, nothing runs): `langgraph.json`, ADK `root_agent`, CrewAI `Crew(...)`, OpenAI Agents SDK and Pydantic AI `Agent(...)`, smolagents, DSPy modules, a plain `agent()` function. Registers a pull agent served with `heimdall serve file.py:object` |
| an http(s) URL | asks the server to detect it (`POST /api/v1/connect/detect`). When the server can reach and map it, Heimdall calls it from its worker. When it can not (a private or local address), or the server has no detection yet, this machine finds the request shape with a connection check code and relays it |
| `"curl ..."` | the request the agent's docs show: the body becomes the template (the input field, or the messages array) and the answer is found by the check code |
| a repository URL, image or platform id | server-side detection only |

It writes `.heimdall/agent.yaml` (commit it; header values that carry a credential are replaced by `${HEIMDALL_AGENT_<HEADER>}` references, never written), registers the agent through API v1, checks it, and offers the first look. While the first look runs, connect also serves a local agent, so one command is enough. Flags: `-name`, `-H 'Header: value'`, `-local`, `-model`, `-yes`, `-no-first-look`, `-json`.

## heimdall serve

```sh
heimdall serve app/agent.py:graph                     # a Python object, wrapped by type
heimdall serve http://support-bot.internal:8080 --map auto   # relay a service in a private network
heimdall serve .                                      # whatever .heimdall/agent.yaml says
```

Pull mode (`docs/pull-agents.md`): the process connects out to Heimdall and answers the evaluation's turns; nothing connects in, no inbound port, no shim.

- **Objects.** The Python package does the wrapping (`heimdall.wrap`): a LangGraph graph gets `.invoke({"messages": [...]})`, a CrewAI crew `.kickoff(inputs=...)`, an OpenAI Agents SDK agent `Runner.run_sync`, a Pydantic AI agent `.run_sync`, an ADK agent an in-memory runner with one session per Heimdall session, a smolagents agent `.run` with its memory reset per session, a DSPy module its first input field, and a plain function is called with the turn, the messages or the text by its signature (async allowed). The binary runs `python3 -m heimdall.cli serve ...`; without the binary use `uvx --from <server>/downloads/<wheel> heimdall serve app.agent:graph`.
- **Relay.** `--map auto` sends a few small requests with a connection check code (`User-Agent: heimdall-connect/1`, no redirects followed): an OpenAI-compatible server (`GET /v1/models`), then the likely paths and body shapes, steered by what validation errors name. A mapping is accepted only when the answer holds the code. `-H 'Authorization: Bearer ${BOT_TOKEN}'` reads secrets from the environment. `--map mapping.json` gives the mapping by hand (`kind`, `method`, `url`, `headers`, `body` with `{{input}}`, `{{messages}}`, `{{session_id}}`, `answer` as a dotted path, `-1` for the last element). Relayed agents answer in text; task tools are offered to wrapped Python functions only.

`heimdall serve` with no target, or with flags only, is still the web server.

## heimdall first-look

`heimdall first-look -agent <id>` (or inside a connected repository): 5 tasks by 1 trial on the fastest benchmark the agent fits, chosen by the server's own estimate, run on the organization's own provider keys. It shows the estimate and asks first (`-yes` skips the question), uses the server's `first_look` preset when it has one, waits and prints the report card link. A first look is too small for a verdict and is never a release gate.

## In the editor

`heimdall mcp serve` adds `connect_agent`, `verify_agent` and `first_look` to the MCP tools (`docs/mcp-server.md`). Integrations > IDE has Add to Cursor and Add to Claude Code; neither carries a token.
