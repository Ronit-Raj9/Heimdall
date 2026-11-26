# Operations

Scripts and checklists for the person who runs the hosted server. They live
in `deploy/hosted/ops/`. Each one prints its own help with `-h`, never
prints a secret value, and, where it changes anything, does a dry run unless
it is given `--apply` from an interactive terminal. Nothing here is installed
by the upgrade script: you copy the units by hand.

## 1. Off-site backups

The server's backup folder sits on the same disk as the data, so a lost disk
loses both. The off-site backup copies it, encrypted, to a storage account in
another region.

Set up once (the owner):

1. Create an Azure storage account in another region than the VM (for
   example the VM in East US, the account in West Europe), a container named
   `heimdall-backups`, and turn on soft delete for 14 days.
2. Create a SAS token limited to that container with read, write and list.
3. On the server, as root, create `/etc/heimdall/restic.pass` (a long random
   password, mode 0600) and `/etc/heimdall/restic.env` (mode 0600) with
   `RESTIC_REPOSITORY=azure:heimdall-backups:/vm1`, `AZURE_ACCOUNT_NAME`,
   `AZURE_ACCOUNT_SAS` and `RESTIC_PASSWORD_FILE=/etc/heimdall/restic.pass`.
   Keep a copy of the restic password offline (section 2).
4. Create `/etc/heimdall/alert.env` (mode 0600) with
   `HEIMDALL_ALERT_WEBHOOK=<a Slack incoming webhook URL>` for the failure
   alert.
5. Initialize the repository once and install the timer (below).

Each run backs up the database, the project files and the encrypted env copy
(the existing backup script), writes a manifest with counts and checksums,
sends the folder to restic, keeps 7 daily, 4 weekly and 6 monthly snapshots,
and on Sundays reads back 5% of the stored data. A failed run posts
"Heimdall off-site backup failed on <host>" to the alert webhook.

From a terminal (on the server, as root):

```sh
set -a; . /etc/heimdall/restic.env; set +a; restic init
cp deploy/hosted/ops/heimdall-offsite-backup.service deploy/hosted/ops/heimdall-offsite-backup.timer /etc/systemd/system/
systemctl daemon-reload && systemctl enable --now heimdall-offsite-backup.timer
systemctl start heimdall-offsite-backup.service   # one run by hand
journalctl -u heimdall-offsite-backup.service -n 50
```

The unit expects the checkout at `/data/Heimdall`; edit its paths if yours
differs.

## 2. Restore drill (monthly)

A backup is only proven by a restore. Run the drill on the laptop or a
scratch machine, never on the live server (the script refuses when a
Heimdall stack is running on the machine). It restores the latest snapshot,
checks every file against the manifest, decrypts the env copy into the work
folder without printing it, starts only the database and the server on
127.0.0.1:18090, restores the dump and the project files, and checks the
health page, the organization count and three random experiments' evidence.
It ends with "restore drill passed: ..." and removes the drill stack.

From a terminal (on the laptop):

```sh
export HEIMDALL_BACKUP_PASSPHRASE_FILE=~/secure/heimdall-backup.pass
deploy/hosted/ops/restore-drill.sh            # the plan only
deploy/hosted/ops/restore-drill.sh --apply
```

Restic settings for the laptop go in `~/.config/heimdall/restic.env` (same
names as on the server, with a SAS token that can read).

## 3. Secret escrow

The master key decrypts every stored provider key and cannot be rotated
today, so it must be kept somewhere safe off the machine. The escrow script
encrypts it, the database passwords and the other server secrets to a public
key; the server can write escrow files but never read them.

Set up once (the owner, on the laptop): create a key pair with age, keep the
private key in the password manager and on paper, and copy only the public
key to the server as `/etc/heimdall/escrow.pub`.

From a terminal:

```sh
# laptop, once
age-keygen -o heimdall-escrow.key
# server, after each change of a secret
deploy/hosted/ops/escrow-secrets.sh --extra-file /etc/heimdall/restic.pass --extra-file <backup passphrase file>
# laptop: check an escrow file against an env file, prints only matches or differs
HEIMDALL_ENV_FILE=<copy of the server env file> deploy/hosted/ops/escrow-secrets.sh --verify heimdall-secrets-<time>.age --identity heimdall-escrow.key
```

The escrow file is written to the backup folder and travels off-site with
the backups.

## 4. Down alert

A check on the server dies with the server, so the alert runs elsewhere.

- Main: a free external uptime monitor on
  `https://20-70-160-61.sslip.io/healthz` every 5 minutes, alerting by email
  and to the Slack incoming webhook.
- Backup: the uptime probe on the owner's laptop, every 5 minutes. Two
  failures in a row post "Heimdall is down: <url> did not answer since
  <time>" and show a desktop notice; the first success after that posts
  "Heimdall is back: down for <minutes> minutes".
- The Prometheus rules in `deploy/hosted/monitoring/alerts.yml` cover partial
  failures. There is no Alertmanager in the stack, so they show in Grafana
  only.

From a terminal (on the laptop):

```sh
mkdir -p ~/.config/heimdall ~/.config/systemd/user ~/.local/bin
printf 'HEIMDALL_UPTIME_URL=https://20-70-160-61.sslip.io/healthz\nHEIMDALL_UPTIME_WEBHOOK=<webhook URL>\n' > ~/.config/heimdall/uptime.env
chmod 600 ~/.config/heimdall/uptime.env
ln -sf "$PWD/deploy/hosted/ops/uptime-probe.sh" ~/.local/bin/heimdall-uptime-probe
cp deploy/hosted/ops/heimdall-uptime.service deploy/hosted/ops/heimdall-uptime.timer ~/.config/systemd/user/
systemctl --user daemon-reload && systemctl --user enable --now heimdall-uptime.timer
```

## 5. Test leftovers

The pass 6 testers left objects the web app cannot delete
(`pitch/video/pass6/LEFTOVERS.md`). The clean-up script lists them, grouped
by tester and kind, with the paths and the SQL. It moves files into
`/var/lib/heimdall/quarantine/pass6-<time>/` (keeping their paths, so moving
them back undoes it) and deletes only revoked test access tokens and the
unnamed ingest token of the test organization. Files that only mention a
test inbox item but hold other data too are listed and never moved.

`--apply` needs `--i-reviewed-the-list`, an interactive terminal and a
database dump younger than 2 hours. Steps it prints but does not do: remove
the two test inbox sources from `inbox/sources.yaml`, delete the test
organization in Settings > Organization, and rebuild the search index.

From a terminal (on the server):

```sh
deploy/hosted/ops/cleanup-pass6.sh | tee pass6-list.txt     # read all of it
deploy/hosted/backup.sh
deploy/hosted/ops/cleanup-pass6.sh --apply --i-reviewed-the-list
```

## 6. Token rotation checklist

Rotate in this order; the first three were exposed. For each: what, where it
lives, how to rotate, how to check.

1. Modal token pasted in a chat: in the Modal dashboard, revoke it and create
   a new one; paste it in Settings > Workers > Modal. Check: the card says
   connected and an egress probe passes.
2. Temporary SSH key `heimdall-fleet-temp`: remove its line from
   `~/.ssh/authorized_keys` on the VM and from the VM's SSH keys in Azure.
   Check: a login with that key is refused.
3. GitHub token of the `Ronit-Raj9` account used on the laptop: regenerate it
   in GitHub settings with the same scopes and sign in again. Check: the
   GitHub tool reports the new login.
4. Provider keys (OpenAI and others) in Settings > API keys of each
   organization: create a new key at the provider, paste it, press Test,
   then delete the old key at the provider.
5. `BIFROST_SETUP_TOKEN`, `BENCH_FAULTPROXY_TOKEN`,
   `HEIMDALL_GATEWAY_USAGE_SECRET`, `HEIMDALL_SHARED_RUNNER_SECRET`,
   `HEIMDALL_VOICE_RTC_SECRET`, `LIVEKIT_API_SECRET`: set a new random value
   in `deploy/hosted/.env`, then run the upgrade script (it restarts the
   stack). Check: the smoke script passes.
6. `HEIMDALL_GITHUB_CLIENT_SECRET`, `HEIMDALL_STRIPE_SECRET_KEY`,
   `HEIMDALL_STRIPE_WEBHOOK_SECRET` and the mail keys: rotate at the
   provider, set them in the env file, restart. Check: GitHub sign-in works
   and a Stripe test webhook is accepted.
7. `HEIMDALL_PG_PASSWORD`, `BIFROST_PG_PASSWORD`: change the password inside
   Postgres first (`ALTER USER ... PASSWORD`), then in the env file, then
   restart. Check: `/readyz` answers 200.
8. `HEIMDALL_MASTER_KEY`: cannot be rotated today (docs/hosting.md, section
   12). Escrow it (section 3) and keep it.
9. Webhook signing secrets: in Integrations > Notifications, delete and
   recreate the hook, then update the receiver.
10. After all of them: run the escrow again so it holds the new values, and
    one off-site backup by hand.

From a terminal (step 5, one value):

```sh
. deploy/hosted/lib.sh && env_set BIFROST_SETUP_TOKEN "$(openssl rand -hex 32)"
deploy/hosted/upgrade.sh && deploy/hosted/smoke.sh
```

Also for the owner: set `HEIMDALL_FAULT_MODE=plugin` after its check
passes; confirm that the pinned Bifrost `v2.5.1` serves the prompt
repository and skills routes (one `GET /api/prompt-repo/prompts` and one
`GET /api/skills` against the hosted Bifrost); decide the sign-up mode.
