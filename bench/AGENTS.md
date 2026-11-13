# AGENTS.md: Bifrost Bench

Rules for every coding agent and every person working in `bench/`. Read this file and `docs/DESIGN.md` before writing code. The repo-level `../AGENTS.md` describes Bifrost itself; this file applies its conventions to the bench.

## What this is

Bifrost Bench is an agent benchmarking platform. It runs two versions of a coding agent on the same admitted tasks, repeated, clean and under probes (faults), with every model call routed through a stock Bifrost gateway. It ends in a release verdict (improved, regressed, inconclusive, invalid) and a diagnostics table. `docs/DESIGN.md` is the design. Sections 8 (extension model), 9 (measurement), 13 (Bifrost), 14 (security) and 16 (contracts) are the ones code must follow.

- Go module: `github.com/maximhq/bifrost/bench`, rooted at `bench/`. It builds on its own and imports no other Bifrost module.
- Go toolchain on this machine: 1.26.5. The module says `go 1.26`.
- Always build and test with `GOWORK=off` so a repo-level `go.work` never interferes.

## Working with other agents in the same tree

Several agents edit this tree at the same time. Each one owns a set of folders.

- Edit only the paths you own. Read anything.
- `schemas/`, `spec/`, `go.mod`, `go.sum`, `AGENTS.md` and `docs/DESIGN.md` belong to the lead. Do not edit them. If a shared type or interface is wrong or missing, stop and say so in your final report, with the exact change you need. Do not work around it with a private copy of a shared type.
- Never run `go mod tidy`, `go get`, or `gofmt -w`/`goimports -w` outside your own folders.
- Never run git commands that change state: no `add`, `commit`, `checkout`, `switch`, `stash`, `reset`, `restore`, `clean`, `rebase`, `merge`. Read-only git (`status`, `diff`, `log`) is fine. The lead commits.
- Test only your own packages: `cd /home/raj/Storage/CODING/Hackathon/Bifrost/repo/bench && GOWORK=off go test ./yourpkg/...` and `GOWORK=off go vet ./yourpkg/...`. Other packages may be half-written while you work.
- If you need a third-party dependency that is not already in `go.mod`, do not add it. Use the standard library or report the need.

Dependencies already in `go.mod`: `github.com/valyala/fasthttp`, `github.com/fasthttp/router`, `github.com/bytedance/sonic`, `gopkg.in/yaml.v3`, `github.com/santhosh-tekuri/jsonschema/v6`.

## Go conventions (from Bifrost)

- `gofmt` and `goimports` formatting.
- **Go filenames have no underscores** except the `_test.go` suffix: `signflip.go`, `pairedt_test.go`, never `paired_t.go`.
- Every package has a package comment. Every exported name has a doc comment. Comments explain why, not what.
- Struct field comments are short trailing one-liners or nothing. Put rationale in the doc comment of the function that uses the field.
- Exported functions and methods first, unexported helpers after.
- Functions stay under about 100 lines.
- Errors: always checked, never discarded with `_`, lowercase, no trailing punctuation, with context: `fmt.Errorf("export diff for run %s: %w", runID, err)`.
- Logging through `schemas.Logger`, never `fmt.Println` or the `log` package in library code. `cmd/` may print to stdout for CLI output.
- JSON tags are `snake_case`. YAML tags are `snake_case` and match the JSON tags.
- Artifacts on disk and `schemas` types use `encoding/json`. The HTTP API server uses `sonic` for responses, as `transports/bifrost-http` does.
- HTTP server code uses `fasthttp` and `fasthttp/router`. Handlers are structs with injected dependencies and a `RegisterRoutes(r *router.Router, middlewares ...)` method. The fault proxy is the one stated exception: it uses `net/http` because it streams.
- One file per implementation. Each implementation has a type constant, a config struct where needed, and an unexported constructor `new<Name><Kind>`. Each plug-in package has a `builtin.go` with a `Builtin()` function that lists what ships. No `init()` side effects.
- Implementation packages never import each other. Shared helpers live in `utils/`. Only `cmd/bifrost-bench` assembles registries.
- Never mutate a shared slice or map in place after construction. Registries are built once and read-only.
- Converter and parser functions are pure: no I/O beyond their input, no logging.
- A value that could not be observed is `schemas.Unknown(...)`, never zero.

## Tests

- Every source file has a `<name>_test.go` beside it. Add to the existing test file; do not create a second test file for the same source file.
- Table-driven tests with `t.Run` subtests.
- Unit tests need no network, no Docker and no API key.
- Tests that need Docker carry the build tag `//go:build integration` and run with `GOWORK=off go test -tags integration ./yourpkg/...`. They must clean up every container, volume and network they create.
- Bug fixes go red before green: write the failing test first.
- "Done" means your packages build, `go vet` is clean, and `go test` passes. Paste the command and its result in your final report.

## Docker

- All containers, volumes and networks the bench creates carry the label `bifrost-bench=1`, plus `bifrost-bench.run=<run_id>` when they belong to a run. The janitor removes labelled leftovers.
- Agent containers: non-root user, `--cap-drop ALL`, `--security-opt no-new-privileges`, `--pids-limit`, `--memory`, `--cpus`, read-only root filesystem with tmpfs for `/tmp` and the home directory, no host bind mounts, no Docker socket, attached only to the internal evaluation network.
- The host never runs `git` or any tool inside agent-written files. Diff export and grading happen in short-lived trusted containers.
- Use the `docker` CLI through `os/exec` (helpers in `utils/`), not the Docker SDK.

## Final report format

End every task with a short report in caveman-ultra style: terse fragments, no filler, each fact once. Code, commands, paths, errors and numbers stay exact. Include: files created or changed, the test command and its result, anything you could not finish, and any change you need in `schemas/` or `spec/`.
