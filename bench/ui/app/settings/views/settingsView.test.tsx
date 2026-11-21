import "@/lib/i18n";

import { AuditTab } from "@/components/settings/auditTab";
import { ApiTokensTab } from "@/components/settings/apiTokensTab";
import { MembersTab, invitationLink } from "@/components/settings/membersTab";
import { OrgTab } from "@/components/settings/orgTab";
import { PasswordTab } from "@/components/settings/passwordTab";
import {
  RunnersTab,
  saveTokenCommand,
  tokenFileOf,
} from "@/components/settings/runnersTab";
import { grantableRoles, mayManage } from "@/components/settings/settingsBits";
import {
  API_TOKENS,
  API_TOKEN_CREATED,
  AUDIT,
  INVITATIONS,
  LOCAL_SESSION,
  MEMBERS,
  ORG,
  RUNNERS,
  RUNNER_TOKENS,
  demoSession,
  hostedSession,
} from "@/lib/testing/platformFixtures";
import {
  errorQuery,
  loadingQuery,
  mutation,
  okQuery,
  render,
  tagOf,
} from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SettingsView, { settingsTab } from "./settingsView";

const api = vi.hoisted(() => ({
  q: {} as Record<string, unknown>,
  m: {} as Record<string, Record<string, unknown>>,
  calls: [] as string[],
}));
vi.mock(
  "@tanstack/react-router",
  async () => (await import("@/lib/testing/routerMock")).routerMock,
);
vi.mock("@/lib/store/apis/accountSafetyApi", async () => {
  const { mutation: m } = await import("@/lib/testing/render");
  return { useCreateResetLinkMutation: () => m({}) }; // L22 reset links
});
vi.mock("@/lib/store/apis/secretsApi", async () => {
  const { mutation: m } = await import("@/lib/testing/render");
  return {
    useListSecretsQuery: () => ({ data: { secrets: [] }, isLoading: false }),
    useCreateSecretMutation: () => m({}),
    useRotateSecretMutation: () => m({}),
    useDeleteSecretMutation: () => m({}),
  }; // G15: local settings shows the secrets tab
});
vi.mock("@/lib/store/apis/workerPlatformsApi", async (importOriginal) => {
  const real =
    await importOriginal<
      typeof import("@/lib/store/apis/workerPlatformsApi")
    >();
  const { mutation: m } = await import("@/lib/testing/render");
  return {
    ...real,
    useListWorkerPlatformsQuery: () => ({
      data: { platforms: [], default: "" },
      isLoading: false,
    }),
    useConnectWorkerPlatformMutation: () => m({}),
    useTestWorkerPlatformMutation: () => m({}),
    useSetDefaultWorkerMutation: () => m({}),
    useDisconnectWorkerPlatformMutation: () => m({}),
  }; // X19a: Workers tab, connect a platform
});
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => {
  const real =
    await importOriginal<typeof import("@/lib/store/apis/platformApi")>();
  const { mutation: m } = await import("@/lib/testing/render");
  const q = (name: string) => () => api.q[name];
  const w = (name: string) => () => m(api.m[name] ?? {});
  return {
    ...real,
    useListMembersQuery: q("members"),
    useListInvitationsQuery: q("invitations"),
    useListApiTokensQuery: q("tokens"),
    useListRunnersQuery: q("runners"),
    useListRunnerTokensQuery: q("runnerTokens"),
    useGetAuditLogQuery: q("audit"),
    useGetOrgQuery: q("org"),
    useUpdateMemberMutation: w("updateMember"),
    useRemoveMemberMutation: (...a: unknown[]) => {
      api.calls.push("removeMember");
      return w("removeMember")(...(a as []));
    },
    useLeaveOrgMutation: (...a: unknown[]) => {
      api.calls.push("leaveOrg");
      return w("leaveOrg")(...(a as []));
    },
    useCreateInvitationMutation: w("createInvitation"),
    useRevokeInvitationMutation: w("revokeInvitation"),
    useCreateApiTokenMutation: w("createToken"),
    useRevokeApiTokenMutation: w("revokeToken"),
    useCreateRunnerTokenMutation: w("createRunnerToken"),
    useRevokeRunnerTokenMutation: w("revokeRunnerToken"),
    useRemoveRunnerMutation: w("removeRunner"),
    useRenameOrgMutation: w("rename"),
    useSetPublicReadMutation: w("publicRead"),
    useSwitchOrgMutation: w("switchOrg"),
    useChangePasswordMutation: w("changePassword"),
  };
});

const org = hostedSession().org!;

beforeEach(() => {
  api.q = {
    members: okQuery(MEMBERS),
    invitations: okQuery(INVITATIONS),
    tokens: okQuery(API_TOKENS),
    runners: okQuery(RUNNERS),
    runnerTokens: okQuery(RUNNER_TOKENS),
    audit: okQuery(AUDIT),
    org: okQuery(ORG),
  };
  api.m = {};
  api.calls = [];
});

describe("SettingsView", () => {
  it("explains local mode instead of showing team settings", () => {
    const html = render(
      <SettingsView tab="members" onTabChange={() => undefined} />,
      LOCAL_SESSION,
    );
    expect(html).toContain('data-testid="settings-local"');
    expect(html).toContain("local mode");
    expect(html).not.toContain("settings-tab-members");
    expect(html).toContain('data-testid="settings-local-links"');
  });
  it("keeps browsers from autofilling secret names and values", () => {
    const html = render(
      <SettingsView tab="members" onTabChange={() => undefined} />,
      LOCAL_SESSION,
    );
    const value = tagOf(html, "secret-value") ?? "";
    expect(value).toContain('autoComplete="new-password"');
    expect(value).toContain('data-lpignore="true"');
    expect(value).toContain('data-1p-ignore="true"');
    const name = tagOf(html, "secret-name") ?? "";
    expect(name).toContain('autoComplete="off"');
    expect(name).toContain('name="heimdall-secret-name"');
  });
  it("asks a visitor who is not signed in to sign in", () => {
    expect(
      render(
        <SettingsView tab="members" onTabChange={() => undefined} />,
        demoSession(),
      ),
    ).toContain("settings-signed-out");
  });
  it("shows the six tabs and the organization in hosted mode", () => {
    const html = render(
      <SettingsView tab="members" onTabChange={() => undefined} />,
      hostedSession(),
    );
    for (const tab of [
      "members",
      "tokens",
      "runners",
      "audit",
      "org",
      "account",
    ])
      expect(html).toContain(`data-testid="settings-tab-${tab}"`);
    expect(html).toContain(
      "Who can use Acme Agents, access keys for scripts, and the machines that run your agents.",
    );
    expect(html).toContain('data-testid="settings-members"');
  });
  it("reads the tab from the URL, members by default", () => {
    expect(settingsTab("audit")).toBe("audit");
    expect(settingsTab("account")).toBe("account");
    expect(settingsTab("nope")).toBe("members");
    expect(settingsTab(null)).toBe("members");
  });
});

describe("MembersTab", () => {
  it("lets an owner change roles and remove others, never itself", () => {
    const html = render(<MembersTab org={org} />, hostedSession("owner"));
    expect(html).toContain('data-testid="member-role-usr_grace"');
    expect(html).toContain('data-testid="member-remove-usr_grace"');
    expect(html).not.toContain("member-role-usr_ada");
    expect(html).not.toContain("member-remove-usr_ada");
    expect(html).toContain('data-testid="invite-form"');
    expect(html).toContain('data-testid="invitation-row-inv_1"');
  });
  it("lets an admin manage members and viewers but not owners", () => {
    const html = render(<MembersTab org={org} />, hostedSession("admin"));
    expect(html).toContain("member-role-usr_alan");
    expect(html).toContain("member-role-usr_kat");
    expect(html).not.toContain("member-role-usr_ada");
    expect(grantableRoles("admin")).toEqual(["viewer", "member", "admin"]);
    expect(mayManage("admin", "owner")).toBe(false);
    expect(mayManage("admin", "member", "owner")).toBe(false);
    expect(mayManage("owner", "admin", "owner")).toBe(true);
  });
  it("shows a member read-only roles and no invite form", () => {
    const html = render(<MembersTab org={org} />, hostedSession("member"));
    expect(html).not.toContain("member-role-usr_grace");
    expect(html).not.toContain("invite-form");
    expect(html).toContain("Needs admin or above. Your role: member.");
  });
  it("shows the role refusal when the server answers 403", () => {
    api.q.members = errorQuery(403, "members need the admin role");
    const html = render(<MembersTab org={org} />, hostedSession("viewer"));
    expect(html).toContain("Your role cannot do this");
    expect(html).toContain("members need the admin role");
  });
  it("builds the invitation link on this site", () => {
    expect(invitationLink("https://h.dev", "a/b")).toBe(
      "https://h.dev/invite/a%2Fb",
    );
  });
});

describe("ApiTokensTab", () => {
  it("lists tokens by prefix only, never a secret, with revoke on active ones", () => {
    const html = render(<ApiTokensTab />, hostedSession("admin"));
    expect(html).toContain("hm_a1b2c3d4…");
    expect(html).not.toContain(API_TOKEN_CREATED.token);
    expect(tagOf(html, "token-row-tok_old")).toContain('data-state="revoked"');
    expect(html).toContain('data-testid="token-revoke-tok_ci"');
    expect(html).not.toContain("token-revoke-tok_old");
    expect(html).toContain('data-testid="token-form"');
  });
  it("shows the create form, not a secret, on a fresh render: a secret is shown once", () => {
    const html = render(<ApiTokensTab />, hostedSession("owner"));
    expect(html).toContain('data-testid="token-form"');
    expect(html).not.toContain("token-created");
  });
  it("hides create and revoke from members", () => {
    const html = render(<ApiTokensTab />, hostedSession("member"));
    expect(html).not.toContain("token-form");
    expect(html).not.toContain("token-revoke-");
  });
  it("shows empty and loading states", () => {
    api.q.tokens = okQuery({ mode: "live", tokens: [] });
    expect(render(<ApiTokensTab />, hostedSession())).toContain("tokens-empty");
    api.q.tokens = loadingQuery();
    expect(render(<ApiTokensTab />, hostedSession())).toContain(
      "tokens-loading",
    );
  });
});

describe("RunnersTab", () => {
  it("lists runners with state, pool, version and last seen, and the runner tokens", () => {
    const html = render(<RunnersTab org={org} />, hostedSession("admin"));
    expect(html).toContain('data-testid="runner-row-rnr_1"');
    expect(html).toContain('data-testid="runner-state-running"');
    expect(html).toContain('data-testid="runner-state-lost"');
    expect(html).toContain("2 of 4 busy");
    expect(html).toContain("0.9.2");
    expect(html).toContain('data-testid="runner-token-row-rtk_1"');
    expect(html).toContain("hmr_f00dcafe…");
    expect(html).toContain('data-testid="runner-token-form"');
    expect(html).toContain('data-testid="runner-remove-rnr_2"');
  });
  it("explains the shared worker with its slots and links to your own machine", () => {
    const shared = {
      ...RUNNERS.runners[0],
      id: "rnr_s",
      name: "shared-runner-org_abc",
      pool: "shared",
      slots: 3,
    };
    api.q.runners = okQuery({ mode: "live", runners: [shared] });
    const html = render(<RunnersTab org={org} />, hostedSession("admin"));
    expect(html).toContain('data-testid="runner-row-rnr_s-shared-note"');
    expect(html).toContain("provided by the owner of this Heimdall server");
    expect(html).toContain("Up to 3 runs at once.");
    expect(html).toContain('href="#runner-token-setup"');
    expect(html).toContain('id="runner-token-setup"');
    expect(html).toContain("Use your own machine instead");
  });
  it("says how to get a first runner", () => {
    api.q.runners = okQuery({ mode: "live", runners: [] });
    expect(render(<RunnersTab org={org} />, hostedSession())).toContain(
      "Create a worker token below, then start the worker with it.",
    );
  });
  it("shows the role refusal to members (runners are admin)", () => {
    api.q.runners = errorQuery(
      403,
      "workers and worker tokens need the admin role",
    );
    api.q.runnerTokens = errorQuery(
      403,
      "workers and worker tokens need the admin role",
    );
    expect(render(<RunnersTab org={org} />, hostedSession("member"))).toContain(
      "Your role cannot do this",
    );
  });
  it("saves the token where the start command reads it", () => {
    const command =
      "bifrost-bench runner start -server https://h.dev -token-file /etc/heimdall/token -pool gpu";
    expect(tokenFileOf(command)).toBe("/etc/heimdall/token");
    expect(tokenFileOf("bifrost-bench runner start")).toBe(
      "~/.heimdall/runner-token",
    );
    expect(saveTokenCommand("hmr_x", "/etc/heimdall/token")).toBe(
      "mkdir -p /etc/heimdall && (umask 077 && printf '%s\\n' 'hmr_x' > /etc/heimdall/token)",
    );
  });
});

describe("AuditTab", () => {
  it("shows who did what to which target, when and from where, with paging", () => {
    const html = render(<AuditTab org={org} />, hostedSession("admin"));
    expect(html).toContain('data-testid="audit-row-aud_3"');
    expect(html).toContain("ada@acme.dev"); // actor ids read as member emails
    expect(html).toContain("member.role");
    expect(html).toContain("user:usr_grace");
    expect(html).toContain("10.0.0.7");
    expect(html).toContain("from=member  to=admin");
    expect(tagOf(html, "audit-newer")).toContain('disabled=""');
    expect(tagOf(html, "audit-older")).toContain('disabled=""'); // a short page is the last
  });
  it("says when nothing matches", () => {
    api.q.audit = okQuery({ mode: "live", entries: [] });
    expect(render(<AuditTab org={org} />, hostedSession())).toContain(
      "audit-empty",
    );
  });
});

describe("OrgTab", () => {
  it("lets an owner rename and change public read, with the danger zone", () => {
    const html = render(<OrgTab org={org} />, hostedSession("owner"));
    expect(html).toContain('data-testid="org-rename-form"');
    expect(html).toContain('data-testid="org-public-toggle"');
    expect(html).toContain("Make public");
    expect(html).toContain('data-testid="org-leave"');
    expect(html).toContain('data-testid="org-delete-note"');
  });
  it("lets an admin rename but only an owner publish", () => {
    const html = render(<OrgTab org={org} />, hostedSession("admin"));
    expect(html).toContain("org-rename-form");
    expect(html).not.toContain("org-public-toggle");
    expect(html).toContain("Needs owner or above. Your role: admin.");
  });
  it("leaves through the self-service route, not the admin-only remove (6-C-01)", async () => {
    const html = render(<OrgTab org={org} />, hostedSession("viewer"));
    expect(html).toContain('data-testid="org-leave"');
    expect(api.calls).toContain("leaveOrg");
    expect(api.calls).not.toContain("removeMember");
    const { leaveOrgRequest } = await import("@/lib/store/apis/platformApi");
    expect(leaveOrgRequest("org 1")).toEqual({
      url: "/orgs/org%201/leave",
      method: "POST",
    });
  });
  it("shows the public state", () => {
    api.q.org = okQuery({ ...ORG, public_read: true });
    const html = render(<OrgTab org={org} />, hostedSession("owner"));
    expect(tagOf(html, "org-public-state")).toContain('data-public="true"');
    expect(html).toContain("Make private");
  });
});
// 4-A-05: the hosted bootstrap password can be rotated from the browser.
describe("PasswordTab", () => {
  it("asks for the current and new password and says which sessions end", () => {
    const html = render(<PasswordTab />, hostedSession("member"));
    expect(html).toContain('data-testid="password-form"');
    expect(tagOf(html, "password-current")).toContain(
      'autoComplete="current-password"',
    );
    expect(tagOf(html, "password-new")).toContain(
      'autoComplete="new-password"',
    );
    expect(html).toContain('data-testid="password-repeat"');
    expect(html).toContain(
      "Your other signed-in sessions end; this one stays signed in.",
    );
  });
  it("shows the server's reason when the change fails", () => {
    api.m.changePassword = {
      error: {
        status: 403,
        data: { error: { message: "current password is wrong" } },
      },
    };
    const html = render(<PasswordTab />, hostedSession("member"));
    expect(html).toContain('data-testid="password-error"');
    expect(html).toContain("current password is wrong");
  });
});
