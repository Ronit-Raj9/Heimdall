# Repository maintenance

Steps for the repository owner. They change the GitHub repository itself, so they are done by hand once a push is allowed, never by an agent.

## After the first push of Heimdall CI

1. Close the open dependabot pull requests. Dependabot now has four Heimdall entries (`.github/dependabot.yml`) and opens at most three pull requests per ecosystem each week.
2. Delete stale branches: `ci-test`, `integration`, `feat/*` and `dependabot/*`.
3. In the repository settings, turn off "Dependabot security updates" for the upstream Bifrost folders.
4. Set the repository description, the homepage (the live site) and the topics `agent-benchmarking`, `llm-gateway`, `bifrost`, `evaluation` and `fault-injection`.

## Upstream workflows

Bifrost's own workflows are kept in `.github/workflows-upstream/` with their names unchanged, so a later upstream merge follows the rename. GitHub runs only `.github/workflows/`, where Heimdall's CI (`heimdall-ci.yml`) and the release gate (`bench-check.yml`) live.

## Releases

A release is a local annotated tag on the integration commit, for example `git tag -a heimdall-v0.1.0 -m "Heimdall 0.1.0"`, with a matching section in `bench/CHANGELOG.md`.
