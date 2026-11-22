# Authentication and sessions (hosted mode)

Package `bench/auth`, routes in `handlers/auth.go`, contract in
[platform-design.md](platform-design.md). This page says what the auth area
does, how to configure it, and what it protects against and what it does not.

## Modes

`auth.mode: local` (the default) is today's single-user server, unchanged:
reads are open, every write route needs `Authorization: Bearer
<BENCH_WRITE_TOKEN>`, and every request carries the principal
`{kind: local, org: local, role: owner}`. Only `GET /api/bench/auth/config`
and `GET /api/bench/auth/me` answer in local mode (describing that
principal); the other auth routes answer 404.

`auth.mode: hosted` turns on accounts, organizations, sessions and API
tokens. The auth middleware (`auth.Middleware`) resolves every request to a
`Principal` before the tenancy dispatcher or any handler runs:

| Credential | Principal | CSRF check |
|---|---|---|
| `Authorization: Bearer hm_...` (API token) | `token`, the token's org and role | none (see below) |
| `Authorization: Bearer hmr_...` (runner token) | `runner`, from the verifier the runners area registers | none |
| `heimdall_session` cookie | `user`, the session's current org and the user's role there | every write |
| nothing | `anonymous` | foreign `Origin` refused on writes |

A bearer token that is malformed (including `Bearer` with an empty or blank
token), unknown, revoked or expired is a 401; it never falls back to the
cookie. A runner token that can not be checked because the database is
unavailable is a 503, so a runner retries instead of giving up. A cookie that names no live session is
cleared and the request continues as anonymous.

## Configuration

```yaml
auth:
  mode: hosted
  public_url: https://heimdall.example.com   # origin only; https makes cookies Secure
  session_ttl_h: 168        # absolute session lifetime
  session_idle_h: 24        # a session unused this long expires
  signup: invite            # invite | open | closed
  invite_ttl_h: 168
  trusted_proxies: [172.16.0.0/12]   # reverse proxies whose X-Forwarded-For is believed
  github:                   # optional; on only when both variables are set
    client_id_env: HEIMDALL_GITHUB_CLIENT_ID
    client_secret_env: HEIMDALL_GITHUB_CLIENT_SECRET
```

`server.cors_origins` are also allowed origins for cookie writes (a UI dev
server, for example). Set `trusted_proxies` to the address range of the
reverse proxy (Caddy in `deploy/hosted`): without it every request appears
to come from the proxy, and the per-IP rate limits and audit IPs see one
address. Only a peer inside that range has `X-Forwarded-For` read, and then
the right-most address that is not itself a trusted proxy is used.

GitHub sign-in is on only when both variables named under `auth.github`
hold values; if either is empty or unset the server starts with GitHub
sign-in off and logs one line saying so. The GitHub OAuth app's callback
URL is `<public_url>/api/bench/auth/github/callback`.

## Credentials

**Passwords.** argon2id (`golang.org/x/crypto/argon2`), 64 MiB, 3 passes, 2
lanes, 16-byte salt, 32-byte key, stored as a PHC string
(`$argon2id$v=19$m=65536,t=3,p=2$salt$key`) so parameters can change: a
login against a hash made with other parameters re-hashes it. Comparison is
constant time. Parameters read back from a stored hash are bounded, so a
corrupted row can not make a login exhaust memory, and at most one hash per
CPU (up to 8) runs at once, so a burst of logins queues instead of taking
64 MiB each. Policy: 12 to 1024
characters, valid UTF-8, not one repeated character, not the email or close
to it, not on a small embedded list of common passwords
(`auth/common_passwords.txt`), and not a common word with only digits or
symbols appended (`password2026!`). The list catches the obvious; it is not
a breach corpus.

**Sessions.** The cookie value is 32 random bytes (base64url); the
`sessions` row stores only its sha256. Cookie: `heimdall_session`,
`HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` when `public_url` is https,
`Max-Age` the absolute lifetime. A session ends at `session_ttl_h` after
login whatever happens, or `session_idle_h` after its last request
(`last_seen` is written at most once a minute, so the idle check allows
that minute on top: a session lives at least `session_idle_h` after its
last request and at most one minute longer). Login, signup and GitHub
sign-in delete the caller's previous session (the id rotates); logout
deletes the row and clears the cookie; a password change ends every other
session of the user; disabling a user ends all of theirs. The session's
role is read from `memberships` on every request, so a demotion or removal
applies at once.

**CSRF.** Every cookie-authenticated request other than GET, HEAD and
OPTIONS needs both:

1. `X-Heimdall-CSRF` equal to the session's CSRF value (32 random bytes,
   returned by `GET /auth/me` and by login and signup), compared in
   constant time; and
2. proof of an allowed origin: the `Origin` header in the allowed set
   (`public_url`'s origin plus `server.cors_origins`), or, when the browser
   sent no Origin or `Origin: null`, the Referer's origin in that set, or
   failing both `Sec-Fetch-Site: same-origin`. The UI is served with
   `Referrer-Policy: no-referrer`, under which browsers send `Origin: null`
   and no Referer even on same-origin writes; `Sec-Fetch-Site` is set by the
   browser and can not be set by a page, which is why it is accepted there.
   A cookie write with no origin signal at all is refused.

Bearer-token requests are exempt: a browser never attaches an
`Authorization` header to a cross-site request on its own, and a page that
wants to send one needs a CORS preflight the server only grants to
`cors_origins`. Anonymous writes (login, signup) are refused when they show
a foreign origin, which stops login CSRF (an attacker signing a victim's
browser into the attacker's account); a non-browser client that sends no
origin signal is let through.

**API tokens.** `hm_<8 char lookup prefix>_<43 char secret>`: the secret is
32 random bytes, the row stores the sha256 of the whole token, and the token
is shown once. Tokens belong to one organization and carry a role no higher
than their creator's at creation. Only a signed-in user (a session, not
another token) with role admin in the org may create one; admins of the org
list and revoke them. At use, a token created by a user is worth at most
that user's current role in the org: demoting the user narrows the token,
removing the user from the org or disabling the account stops it. Optional
expiry (`expires_in_days`, at most 3650). `last_used_at` is written at most
once every five minutes per token.

**Runner tokens** (`hmr_...`) are the runners area's. The middleware hands
them to the verifier registered with `auth.RegisterRunnerVerifier`, and
accepts the answer only if it is a principal of kind `runner` with an org.
With no verifier registered every `hmr_` token is a 401.

**Invitations.** A token of 32 random bytes, stored as sha256, valid for
`invite_ttl_h`, for one email address (lowercased) and one role no higher
than the inviter's. Re-inviting an address replaces its pending
invitation. The link is `<public_url>/invite/<token>`; the server sends no
email. Accepting needs a signed-in user whose email is the invited one, or a
signup with that email and the token. An invitation works once. Accepting
raises an existing membership to the invited role but never lowers it.

**GitHub sign-in.** `GET /auth/github/start?return_to=/path` stores the
sha256 of a random state with the return path and binds the state to the
browser with a short-lived `HttpOnly` cookie (path
`/api/bench/auth/github/`). The callback needs the state in the URL to equal
the cookie, and the stored state to exist and be under ten minutes old; it
is deleted on use. The code is exchanged server-side (10 s timeout, answers
capped at 1 MiB), then the user's id and primary verified email are read.
The account is the one linked to that GitHub id, else the one with that
verified email (which gets linked; an account already linked to another
GitHub id is refused), else a new one when the signup policy allows (below,
where a pending invitation for the verified email counts as an invitation).
Linking by email retires the account's earlier credentials, since password
signup never verified that email: its password is cleared and every session
ends (audit `auth.github_link`). The owner can then set a new password from
the GitHub session with no current password.
`return_to` must be a same-site path; anything else becomes `/`. Failures
redirect to `/login?error=github` (or `signup_not_allowed`) with details in
the server and audit logs; a rate-limited callback is `429` with
`Retry-After` instead.

## Signup policy and the first account

| Policy | Web signup |
|---|---|
| `invite` (default) | only with an invitation token for that email |
| `allowlist` | an invitation, or an address at one of `auth.signup_domains` (exact domain match), which owns a new organization; with no domains it is `invite` |
| `open` | anyone; each new account owns a new organization (or joins one with an invitation) |
| `closed` | none, not even the first account; accounts come from the CLI |

The policy applies to GitHub sign-in too. Hosted installs default to
`invite`; set `HEIMDALL_SIGNUP=allowlist` and
`HEIMDALL_SIGNUP_DOMAINS=acme.com,acme.io` in `deploy/hosted/.env` (then
`upgrade.sh --no-pull`) to let a company's own addresses in. A server admin
can also change both live from Admin, Sign-up (`POST /api/bench/admin/signup`);
that override is stored in the database, wins over the config, and every
replica picks it up within 30 seconds. "Use the server config" drops it.

## Email verification

A password signup starts unverified. The server makes a one-time link
(`/verify-email?token=...`, valid 24 hours, only its sha256 stored, a newer
link replaces the pending one) and sends it through the `auth.MailSender`
callback (`func(ctx, to, subject, text, html string) error`, set with
`Deps.SendMail` or `Service.SetMailSender`). With no sender configured, or
when sending fails, the link is written to the server log instead
(`docker compose logs heimdall | grep "verification link"`) and the UI tells
the user to check with their admin. The page posts the token back on a
button press, so a mail scanner fetching the link verifies nothing.

The rule: an unverified account can sign in, accept invitations and use its
organizations with its role (run evaluations, read results). Until verified
it can not create API tokens, invite anyone, create another organization, or
be a server admin. GitHub-linked accounts count as verified (GitHub vouches
for the primary address), and so do accounts made by `bifrost-bench user
create`. An operator can mark one by hand:

```sh
bifrost-bench user verify -email someone@example.com
```

A verification counts only while the account's email is the one verified.
Resends are paced at 3 per user, then 1 every 10 minutes.

## Server admins

A server admin sees every tenant, disables users, edits quotas and flips the
kill switch, so the role never comes from an email match alone. A signed-in
user is a server admin when any of these holds:

- their user id is in `HEIMDALL_SERVER_ADMIN_IDS` (comma-separated);
- an operator granted it on the server: `bifrost-bench admin grant -email
  you@example.com` (by user id; `admin revoke` and `admin list` too);
- their email is in `HEIMDALL_SERVER_ADMINS` and verified. The first request
  after that binds the address to the user id; the binding applies only while
  the address stays listed.

API tokens, runners and anonymous callers are never server admins. An
existing admin whose password account predates verification keeps working
once they open a verification link (Resend in the banner; with no mail sender
the link is in the server log) or once the operator runs `admin grant`.

On an empty install (no users) under `invite` or `open`, the first signup
creates the owner account and its organization (`GET /auth/config` reports
`bootstrap: true`). Until that signup happens, anyone who reaches the server
can claim it. For a server on the public internet, either sign up right
after the first start or run with `signup: closed` and create the owner from
the shell:

```sh
read -rs PW && echo "$PW" | bifrost-bench user create -email you@example.com -org "Your org" -role owner
read -rs PW && echo "$PW" | bifrost-bench user set-password -email you@example.com   # recovery; ends every session
```

The password is read from standard input, never from a flag (flags show in
process lists and shell history). `-org` is an org id or slug, or the name of
a new organization.

## Rate limits and enumeration

In-memory token buckets, per process:

| Bucket | Burst | Refill |
|---|---|---|
| login per IP | 30 | 1 every 2 s |
| login per email (also password change) | 10 | 1 every 30 s |
| signup per IP | 5 | 1 a minute |
| signup per email | 3 | 1 every 5 minutes |
| invitation lookup and accept, GitHub start and callback, per IP | 30 | 1 every 2 s |

A refused attempt is `429` with `Retry-After`. A successful login resets its
email bucket. The limiter keeps at most 100,000 keys per bucket and drops
full buckets first; if it is full of active keys it refuses new keys (fails
closed). With several server replicas each has its own buckets, so the
effective limit multiplies by the replica count.

Every failed login answers the same `401 invalid email or password` after
the same argon2id work (a dummy hash is verified when the account is
missing, disabled or password-less), so neither the message nor the timing
says whether an account exists. Every bad bearer token answers the same
`401 invalid or expired token`. An invitation that is unknown, expired or
used answers the same 404. Switching to an org the user is not a member of
answers the same 404 as an org that does not exist.

## Audit

Every auth event writes an `audit_log` row through the database area's
audit API: `auth.login`, `auth.login_failed`, `auth.login_rate_limited`,
`auth.logout`, `auth.signup`, `auth.signup_failed`, `auth.switch_org`,
`auth.password_change`, `auth.password_change_failed`, `auth.token_create`,
`auth.token_revoke`, `auth.invite_create`, `auth.invite_revoke`,
`auth.invite_accept`, `auth.github_link`, `auth.verify_email_sent`,
`auth.email_verified`, `auth.verify_email_failed`, `auth.server_admin_bound`,
`auth.signup_settings`, and from the CLI `auth.user_create`,
`auth.password_set`, `auth.email_verified`, `auth.server_admin_grant` and
`auth.server_admin_revoke` (actor `system`). Share link reads write
`shares.view` (anonymous, with the address and user agent) into the link's
organization, at most once per link, address and route an hour. Failed logins
(including rate-limited ones and GitHub callbacks refused before the code
exchange), refused signups (validation, rate limit, closed policy) and failed
password changes are recorded with actor `anonymous` or the user and the
reason. An audit write that fails is logged, not shown to the caller; the
CLI commands exit non-zero when their audit row could not be written.

## Routes

All under `/api/bench/auth/`; bodies are a single JSON value with unknown
fields refused (trailing data after it is a 400).

| Route | Who | Answer |
|---|---|---|
| `GET config` | anyone | auth mode, signup policy, GitHub on, bootstrap |
| `GET me` | signed in (401 otherwise) | user, orgs with roles, current org and role, CSRF value |
| `POST signup` | anyone, under the policy | 201, session cookie, `me` |
| `POST login` | anyone | session cookie, `me` |
| `POST logout` | anyone | 204, cookie cleared |
| `POST switch-org` `{org}` | session | the org (id or slug) with the user's role |
| `POST password` `{current_password, new_password}` | session | 204 |
| `GET github/start?return_to=` | anyone | 302 to GitHub |
| `GET github/callback` | GitHub's redirect | 302 to the return path |
| `GET invitations/{token}` | token holder | org, email, role, expiry, whether the account exists |
| `POST invitations/{token}` | signed-in invitee | the joined org with the role |
| `GET tokens` | admin of the current org | tokens without secrets |
| `POST tokens` `{name, role, expires_in_days}` | session, admin | 201, the token shown once |
| `POST tokens/{id}/revoke` | admin of the current org | 204 |
| `POST verify-email` `{token}` | link holder | email, verified; 404 when unknown, expired, used or replaced |
| `POST verify-email/resend` | session | where the new link went (`email` or `log`); never the link |

`GET` and `POST /api/bench/admin/signup` (server admin) read and set the
signup override.

Invitations are created, listed and revoked under
`/api/bench/orgs/{org}/invitations` (tenancy area), backed by
`Service.CreateInvitation`, `ListInvitations` and `RevokeInvitation`.

## Threat model

Protected:

- Stolen database or backup: passwords are argon2id; session ids, API
  tokens, invitation tokens and OAuth states are sha256 of 256-bit random
  values, useless to replay. (Backups still hold emails and audit rows; keep
  them private.)
- Cross-site request forgery on cookie sessions (CSRF value plus origin),
  login CSRF (origin check on anonymous writes), OAuth login CSRF (state
  bound to the browser by cookie, single use, ten minutes).
- Session fixation (server-made ids, rotated at every sign-in), stale
  sessions (absolute and idle expiry), stolen cookies after logout or a
  password change (rows deleted).
- Password guessing (per-IP and per-email limits, argon2id cost) and account
  enumeration through login answers or timing.
- Privilege escalation through tokens (role capped at creation and by the
  creator's current role at every use; tokens can not mint tokens) and
  through invitations (role capped by the inviter's).
- Open redirects through `return_to` (same-site paths only).

Not protected, or not yet:

- No SSO or SAML, no two-factor authentication, no WebAuthn.
- No email verification for password signups and no password reset by
  email: the server sends no mail. Recovery is `bifrost-bench user
  set-password` from the shell.
- No account lockout beyond the rate limits; limits are per process and in
  memory (lost on restart, multiplied by replicas).
- Signup can not hide whether an email already has an account (it answers a
  generic "could not create an account" that also covers other failures);
  it is rate limited per IP and per email.
- The bootstrap window: until the first account exists under `invite` or
  `open`, whoever reaches the server first becomes owner. Use `closed` and
  the CLI on a public server.
- Cross-site scripting would defeat CSRF protection (a script on the origin
  can read `/auth/me`); the strict Content-Security-Policy and inert
  rendering of trace content are the defence there.
- Cookies are `Secure` only when `public_url` is https; plain-http hosting
  exposes them on the network. TLS is terminated by the reverse proxy.
- Sessions are not bound to IP or user agent (they are recorded only).
- API tokens and sessions are not revoked when a user's email changes.
  (Deleting a user deletes their sessions and tokens; disabling one stops
  them.)
- Runner token verification is the runners area's; this area only enforces
  that the verifier returns a runner principal with an org.
