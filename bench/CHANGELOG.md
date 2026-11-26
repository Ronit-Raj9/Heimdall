# Changelog

All notable changes to Heimdall are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.1.0] - 2026-11-27

### Added

- A release gate for agent changes through Bifrost, with verdicts of better, worse, or too early to tell from an exact paired test.
- The heimdall Bifrost plugin, which injects faults inside the gateway and tags every call with its run and evaluation.
- One Bifrost virtual key per run, with a hard budget from the evaluation's spending limit.
- A library of benchmarks whose tasks are proven by controls before they count.
- Report cards with a score range, cost per solved task, resilience per fault and the failure patterns behind the score.
- The Through Bifrost panel on every run: provider, model, fallbacks, cache hits, faults, tokens, cost and latency per call.
- Failover and cache tests, the MCP gateway and the routing export.
- Hosted organizations with sign-in, encrypted model keys, a shared gVisor worker and sealed evidence.
- A root `docker-compose.yml` that serves the recorded experiments with no keys.
- Heimdall CI: Go, plugin, UI, lint, Docker and secret scan jobs.

### Fixed

- Wave 1: the first full pass over the web journeys, from sign-up to the report card.
- Wave 2: failing and blocked tester rows of the second test pass, with root causes recorded.
- Wave 3: rows N172 to N297 of the third test pass, seven re-checks and the lead's items L1 to L5.
- Re-scores: the comparison page and the verdict checks read the newest re-score, with a link to the original.
- Plain text across the UI: no raw ids, internal slot names or server paths where a name exists.

### Security

- SEC-A: the shared worker builds agent images only through the organization's Docker policy; the host fallback refuses there.
- SEC-B: skill scan folders can be written only by the scan handler, and on hosted servers `skills.extra` may name only an agent's upload collection.
- Share links use opaque tokens resolved through a platform index, with paths, people and worker fields removed from shared pages.
- No write-token box on the hosted server; the labeller is the signed-in member.
