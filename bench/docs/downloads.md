# Downloads: the command line tool, SDKs and MCP

Everything you need to call Heimdall from a laptop or a CI job comes from your Heimdall server itself, at `/downloads`. Nothing is installed from GitHub, PyPI or npm. The server image builds every file from the commit it runs, so the tool always matches the server.

The **Downloads** page (`/downloads`, no sign-in needed) lists them with sizes and SHA-256 sums. The same list is `GET /api/bench/public/downloads`.

| File | What it is |
|---|---|
| `bifrost-bench-linux-amd64`, `-linux-arm64`, `-darwin-amd64`, `-darwin-arm64` | the command line tool (`heimdall`), one static binary |
| `heimdall_bench-<version>-py3-none-any.whl`, `heimdall_bench-<version>.tar.gz` | the Python SDK (`docs/clients.md`), wheel and source |
| `heimdall-bench-client-<version>.tgz` | the TypeScript SDK, an npm tarball |
| `bench-<commit>.tar.gz` | the bench module at the server's commit (what a worker builds from) |
| `heimdall.gitlab-ci.yml` | the GitLab CI template, with this server's address filled in (`docs/ci.md`) |
| `SHA256SUMS` | one `sha256  name` line per file, the format `sha256sum -c` reads |
| `manifest.json` | the same list as JSON with the commit and version |

## Command line tool

```sh
HEIMDALL_URL=https://heimdall.example.com
asset=bifrost-bench-linux-amd64        # or -linux-arm64, -darwin-amd64, -darwin-arm64
curl -fsSLO "$HEIMDALL_URL/downloads/$asset"
curl -fsSL "$HEIMDALL_URL/downloads/SHA256SUMS" | grep "  $asset\$" | sha256sum -c -   # macOS: shasum -a 256 -c -
install -m 0755 "$asset" /usr/local/bin/heimdall
heimdall version
```

## Python SDK

```sh
pip install "$HEIMDALL_URL/downloads/heimdall_bench-0.2.0-py3-none-any.whl"
```

The wheel has no dependencies (standard library only, Python 3.9 or newer). In a requirements file, write the same URL.

## TypeScript SDK

```sh
npm install "$HEIMDALL_URL/downloads/heimdall-bench-client-0.2.0.tgz"
```

It installs as `@heimdall/bench-client` (ES modules, Node 18 or newer, no dependencies).

## MCP

No install: connect your MCP client to the server's remote endpoint with an org API token (`docs/mcp-server.md`).

```sh
claude mcp add --transport http heimdall "$HEIMDALL_URL/mcp" --header "Authorization: Bearer $HEIMDALL_TOKEN"
```

Or run the local server from the downloaded tool: `HEIMDALL_TOKEN=hm_... heimdall mcp serve -url "$HEIMDALL_URL"`.

## For operators

- The server image builds the files in its `downloads` stage (`deploy/hosted/Dockerfile.server`) and serves them from `HEIMDALL_DOWNLOADS_DIR` (`/srv/heimdall/downloads`). `deploy/hosted/lib.sh` passes the checkout's commit as `HEIMDALL_COMMIT`, so `upgrade.sh` rebuilds them at every upgrade. A build without the commit has no source tarball.
- Only files listed in `manifest.json` are served; anything else in the folder answers 404.
- `/.well-known/security.txt` names `HEIMDALL_SECURITY_CONTACT` (mailto: or https:), or this server's `/security` page when it is empty.
