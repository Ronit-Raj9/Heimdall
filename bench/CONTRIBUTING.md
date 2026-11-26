# Contributing to Heimdall

Heimdall is the `bench/` folder and `plugins/heimdall/`. The rest of the repository is upstream Bifrost and is changed only by upstream merges.

## Development setup

- Go 1.26 for `bench/` (`bench/go.mod`) and Go 1.27.3 for `plugins/heimdall/` (`plugins/heimdall/go.mod`).
- Node 26 for the UI (`.nvmrc`).
- Docker 24 or later with Compose v2 for the demo and the integration tests.

Set `GOWORK=off` for every Go command; the two modules are built separately.

## Tests

These are the commands CI runs (`.github/workflows/heimdall-ci.yml`):

```sh
cd bench && GOWORK=off go vet ./... && GOWORK=off go test -count=1 ./...
cd plugins/heimdall && GOWORK=off go vet ./... && GOWORK=off go test -count=1 ./...
cd bench/ui && npm ci && npm run build && npx vitest run
docker compose -f docker-compose.yml config -q
```

Every behaviour change comes with a test that fails before the change and passes after it.

## Formatting

- Go: `gofmt -l bench plugins/heimdall` prints nothing.
- UI: format every new file with `oxfmt` (`cd bench/ui && npx oxfmt <file>`). CI checks changed UI files with `oxfmt --check`.

## Commit messages

One line in the imperative or present tense that says what changed for a person, for example "Comparison page shows the newest re-score by default". Add a body only when the reason is not obvious.

## Text a person reads

UI text, API errors, job logs and docs use plain words. No em dashes or en dashes anywhere: use a plain hyphen. No internal ids where a name exists.
