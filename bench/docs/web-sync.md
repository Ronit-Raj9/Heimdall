# Sync with the CLI

The website and the CLI work on the same project files: `agents/`,
`benchmarks/`, `experiments/`, `policies/`, uploads. Edit on either side.

## On the website

- [Settings > Files](/settings?tab=files) (page title **Project files**): browse, edit, create and delete files; every
  save is versioned (**Versions**, **Load into editor** to restore).
  **Download project (.zip)** takes everything.
- **Upload** ([open](/upload), reached from Benchmarks): bring sealed runs, a project folder or an agent
  folder from your machine as a zip. Sealed runs are checked against their
  seal: imported, skipped or refused.

## From the CLI

Create an access token: [Settings > Access tokens](/settings?tab=tokens), **Create token**, pick a
role (never more than your own). It shows once.

```sh
# upload changed files from this folder
bifrost-bench push -server https://<your heimdall> -token "$HEIMDALL_TOKEN" -dir . -dry-run
bifrost-bench push -server https://<your heimdall> -token "$HEIMDALL_TOKEN" -dir .

# download the server's files here
bifrost-bench pull -server https://<your heimdall> -token "$HEIMDALL_TOKEN" -dir .
```

- `-dry-run` shows what would change. `-delete` also removes files the
  other side no longer has. `push -force` overwrites files that changed on
  the server since your last pull.
- Results: run locally, then **Upload** the sealed run folders, or let a
  runner run them for the server so they land there directly.

Keep tokens out of shell history and files you commit.
