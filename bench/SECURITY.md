# Heimdall security

This file covers Heimdall, the `bench/` folder and `plugins/heimdall/`. The rest of the repository is upstream Bifrost; see the root `SECURITY.md` for that.

## How to report

On a hosted server, reports go to the address its operator sets in `HEIMDALL_SECURITY_CONTACT` (published at `/.well-known/security.txt`, see [`docs/hosting.md`](docs/hosting.md)). For the code in this repository, open a private security advisory on the GitHub repository. Please do not open a public issue for a vulnerability.

## Supported versions

Only the newest tag gets security fixes.

## Threat model in short

- **Sandboxes.** Agents run in gVisor containers on the shared worker, one container per run, with resource limits.
- **Egress control.** A run reaches only what its task allows; model calls go through Bifrost and tool calls through the tool proxy.
- **Per-run virtual keys with budgets.** Each run gets its own Bifrost virtual key, revoked after the run, with a hard budget from the evaluation's spending limit.
- **Encrypted organization keys.** Provider keys an organization adds are stored encrypted with the server's master key and handed only to the gateway.
- **Sealed evidence.** Every finished run is sealed with a hash of every file, and each experiment keeps a chained seals log; reports are computed from sealed evidence only.

Hardening a hosted server: [`docs/hosting.md`, section 11](docs/hosting.md#11-hardening-checklist).
