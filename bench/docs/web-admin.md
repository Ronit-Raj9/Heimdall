# Server admin on the website

**Operate > Admin** (page title **Server admin**) is shown only to server admins: emails listed in
`HEIMDALL_SERVER_ADMINS` on the server. Tabs:

- **Overview**: users, organizations, runs in progress, and the **Kill
  switch**. **Stop new runs** makes runners lease nothing new; runs in
  progress finish. **Resume runs** turns it off.
- **Users**: filter by email or name; **Disable** signs a user out and
  blocks sign-in until **Enable**.
- **Organizations**: members, public flag, quota. **Edit quota**: runs at
  once, runs per day, spend per day (USD), largest image (GiB), allow custom
  images, **Use the shared runner pool**, container runtime (runc, or runsc
  for the gVisor sandbox). 0 means none allowed. **Use defaults** resets.
- **Custom images**: requests from orgs for digest-pinned images.
  **Approve**, or **Reject** with a reason the org sees.
- **Usage**: platform spend and runs per organization for a date range.
- **Running containers**: runners, pools, hosts, slots in use, connected or
  not. **Refresh**.

## Org admin (not server admin)

**Settings** of an organization (admins and owners):

- **Members**: change roles (viewer, member, admin, owner); **Invite
  someone** creates a one-time link.
- **API tokens**, **Secrets**, **Runners**, **GitHub**.
- **Audit log**: every write, filterable by action (for example `member.role`).
- **Organization**: rename, public read, leave, delete (danger zone).

## Health and evidence

**Operate > Operations**:

- **Health**: **Run doctor** (Docker, images, gateway, fault proxy, suite lock),
  **Kubernetes status**, **GPU check** (local only).
- **Evidence**: **Verify**, **Mirror now**, **Archive idle**, **Back up**
  (database plus a sweep to the mirror), **Restore experiment**.
- **Suite checks**: voice check, visual compare and capture.
- **Simulator spread**.

Things that stay on the server's shell: starting the server (`serve`),
database migrations (`db migrate`), the first deploy. See
[hosting.md](hosting.md).
