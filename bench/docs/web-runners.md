# Runners on the website

Every run executes in containers on a runner, never inside the web server.
A runner is a machine with Docker that leases runs from the server, runs
them and uploads the sealed evidence. Two options.

## Shared runner

If the server owner runs a shared runner and has turned **Use the shared
runner pool** on for your organization, experiments run there with the
built-in agents and suites. Nothing to set up: the wizard and QuickRun just
work. The org's pool status (limits, kill switch, shared pool on or off)
is served at `GET /api/bench/pool`; [Settings > Workers](/settings?tab=runners) lists runners.

No shared runner and no runner of your own? Pages that need one say so and
link here. Start your own (below) or ask the server owner to enable the pool.

## Your own runner

Admins only. [Settings > Workers](/settings?tab=runners):

1. **Create runner token**: pick a pool name (letters, digits, dots, dashes,
   underscores). The token shows once.
2. On the runner machine, save the token to a file, then start the runner
   with the command the page shows:

```sh
bifrost-bench runner start -server https://<your heimdall> -token-file ~/.heimdall-runner-token -pool <pool> -name box-01
```

Or set up a fresh Ubuntu or Debian box as a systemd service in one line
(the page shows it filled in):

```sh
curl -fsSLo install.sh https://<your heimdall>/api/bench/runners/install.sh \
  && sudo bash install.sh --server https://<your heimdall> --token-file ./runner-token --pool <pool> --name box-01
```

The runner appears in the **Runners** table: Idle, Running or Lost, slots
busy, version, last seen. **Revoke** a token to stop every runner using it;
**Remove** drops one runner. In the builder, **Runner pool** sends an
experiment to a pool.

This is the one step that needs a shell: a runner is a process on your own
machine. Everything else stays in the browser.

More: [runners.md](runners.md), [runner-safety.md](runner-safety.md).
