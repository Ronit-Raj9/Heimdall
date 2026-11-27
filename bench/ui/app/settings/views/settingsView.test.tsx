import "@/lib/i18n";

import { AuditTab } from "@/components/settings/auditTab";
import { ApiTokensTab } from "@/components/settings/apiTokensTab";
import { MembersTab, invitationLink } from "@/components/settings/membersTab";
import { OrgTab } from "@/components/settings/orgTab";
import { PasswordTab } from "@/components/settings/passwordTab";
import { RunnersTab, saveTokenCommand, tokenFileOf } from "@/components/settings/runnersTab";
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
import { errorQuery, loadingQuery, mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SettingsView, { settingsTab, settingsTabsFor } from "./settingsView";

const api = vi.hoisted(() => ({
	q: {} as Record<string, unknown>,
	m: {} as Record<string, Record<string, unknown>>,
	calls: [] as string[],
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
// X19a: Workers tab, connect a platform.
vi.mock("@/lib/store/apis/workerPlatformsApi", async (importOriginal) => {
	const real = await importOriginal<typeof import("@/lib/store/apis/workerPlatformsApi")>();
	const { mutation: m } = await import("@/lib/testing/render");
	return {
		...real,
		useListWorkerPlatformsQuery: () => ({ data: { platforms: [], default: "" }, isLoading: false }),
		useConnectWorkerPlatformMutation: () => m({}),
		useTestWorkerPlatformMutation: () => m({}),
		useDisconnectWorkerPlatformMutation: () => m({}),
	};
});
// Prompt logs of the organization (X03), on the Organization tab.
vi.mock("@/lib/store/apis/promptLogsApi", async (importOriginal) => {
	const real = await importOriginal<typeof import("@/lib/store/apis/promptLogsApi")>();
	const { mutation: m } = await import("@/lib/testing/render");
	return {
		...real,
		useGetPromptLoggingQuery: () => ({ isLoading: false }),
		useListPromptLogsQuery: () => ({ isLoading: false }),
		useSetPromptLoggingMutation: () => m({}),
		useDeletePromptLogsMutation: () => m({}),
	};
});
// Where runs go and your own workers (one Workers settings page).
vi.mock("@/lib/store/apis/workerTargetApi", async (importOriginal) => {
	const real = await importOriginal<typeof import("@/lib/store/apis/workerTargetApi")>();
	const { mutation: m } = await import("@/lib/testing/render");
	return {
		...real,
		useGetWorkerTargetQuery: () => ({
			data: {
				target: "shared",
				choices: [{ target: "shared", kind: "shared", name: "shared", title: "Shared worker", current: true, per_evaluation: true }],
			},
			isLoading: false,
		}),
		useSetWorkerTargetMutation: () => m({}),
	};
});
vi.mock("@/lib/store/apis/workerConnectionsApi", async (importOriginal) => {
	const real = await importOriginal<typeof import("@/lib/store/apis/workerConnectionsApi")>();
	const { mutation: m } = await import("@/lib/testing/render");
	return {
		...real,
		useListWorkerConnectionsQuery: () => ({ data: { connections: [], default_worker: "", platforms: [] }, isLoading: false }),
		useCreateWorkerConnectionMutation: () => m({}),
		useWorkerConnectionActionMutation: () => m({}),
		useWorkerInstallCommandMutation: () => m({}),
	};
});
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
vi.mock("@/lib/store/apis/registryCredentialsApi", async () => {
	const { mutation: m } = await import("@/lib/testing/render");
	return {
		useListRegistryCredentialsQuery: () => ({ data: { credentials: [] }, isLoading: false }),
		useCreateRegistryCredentialMutation: () => m({}),
		useDeleteRegistryCredentialMutation: () => m({}),
	}; // Y10: private registry logins on the secrets tab
});
vi.mock("@/lib/store/apis/dataOpsApi", async (importOriginal) => {
	const real = await importOriginal<typeof import("@/lib/store/apis/dataOpsApi")>();
	const { mutation: m } = await import("@/lib/testing/render");
	return {
		auditExportUrl: real.auditExportUrl,
		useVerifyAuditMutation: () => m(api.m.verify ?? {}),
		useGetRetentionQuery: () => api.q.retention,
		useSetRetentionMutation: () => m(api.m.retention ?? {}),
		useDeleteOrgMutation: () => m(api.m.deleteOrg ?? {}),
	};
});
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => {
	const real = await importOriginal<typeof import("@/lib/store/apis/platformApi")>();
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
		retention: okQuery({ run_days: 0, min_days: 1, max_days: 3650 }),
	};
	api.m = {};
	api.calls = [];
});

describe("SettingsView", () => {
	it("shows the plumbing tabs in local mode, without team tabs", () => {
		const html = render(<SettingsView tab="secrets" onTabChange={() => undefined} />, LOCAL_SESSION);
		expect(html).toContain('data-testid="settings-local"');
		expect(html).toContain("local mode");
		for (const tab of ["keys", "runners", "usage", "files", "jobs", "integrations", "operations", "secrets"])
			expect(html).toContain(`data-testid="settings-tab-${tab}"`);
		for (const tab of ["members", "tokens", "github", "audit", "org", "account", "admin", "billing"])
			expect(html).not.toContain(`data-testid="settings-tab-${tab}"`);
	});
	it("lists Billing in hosted mode only (Y16)", () => {
		expect(settingsTabsFor(hostedSession("viewer"))).toContain("billing");
		expect(settingsTabsFor(LOCAL_SESSION)).not.toContain("billing");
		expect(settingsTab("billing")).toBe("billing");
	});
	it("lists Server admin only for server admins", () => {
		const tabs = (session: Parameters<typeof settingsTabsFor>[0]) => settingsTabsFor(session);
		expect(tabs(hostedSession())).not.toContain("admin");
		expect(tabs({ ...hostedSession(), serverAdmin: true })).toContain("admin");
		expect(tabs(hostedSession())[0]).toBe("keys");
	});
	it("keeps browsers from autofilling secret names and values", () => {
		const html = render(<SettingsView tab="secrets" onTabChange={() => undefined} />, LOCAL_SESSION);
		const value = tagOf(html, "secret-value") ?? "";
		expect(value).toContain('autoComplete="new-password"');
		expect(value).toContain('data-lpignore="true"');
		expect(value).toContain('data-1p-ignore="true"');
		const name = tagOf(html, "secret-name") ?? "";
		expect(name).toContain('autoComplete="off"');
		expect(name).toContain('name="heimdall-secret-name"');
	});
	it("asks a visitor who is not signed in to sign in", () => {
		expect(render(<SettingsView tab="members" onTabChange={() => undefined} />, demoSession())).toContain("settings-signed-out");
	});
	it("shows every tab and the organization in hosted mode", () => {
		const html = render(<SettingsView tab="members" onTabChange={() => undefined} />, hostedSession());
		for (const tab of [
			"keys",
			"runners",
			"usage",
			"files",
			"jobs",
			"integrations",
			"operations",
			"members",
			"tokens",
			"secrets",
			"github",
			"audit",
			"org",
			"account",
		])
			expect(html).toContain(`data-testid="settings-tab-${tab}"`);
		expect(html).toContain("Model keys, workers and files for Acme Agents, and who can use it.");
		expect(html).toContain('data-testid="settings-members"');
	});
	it("reads the tab from the URL, API keys by default, workers as the Workers tab", () => {
		expect(settingsTab("audit")).toBe("audit");
		expect(settingsTab("account")).toBe("account");
		expect(settingsTab("workers")).toBe("runners");
		expect(settingsTab("nope")).toBe("keys");
		expect(settingsTab(null)).toBe("keys");
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
		expect(invitationLink("https://h.dev", "a/b")).toBe("https://h.dev/invite/a%2Fb");
	});
});

describe("ApiTokensTab", () => {
	it("lists tokens by prefix only, never a secret, with revoke on active ones", () => {
		const html = render(<ApiTokensTab />, hostedSession("admin"));
		expect(html).toContain("hm_a1b2c3d4…");
		expect(html).not.toContain(API_TOKEN_CREATED.token);
		expect(tagOf(html, "token-row-tok_old")).toContain('data-state="revoked"');
		expect(html).toContain('data-testid="token-revoke-tok_ci"');
		expect(tagOf(html, "token-scope-tok_ci")).toBeTruthy();
		expect(html).toContain("Run evaluations");
		expect(html).toContain('data-testid="token-scope"');
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
		expect(render(<ApiTokensTab />, hostedSession())).toContain("tokens-loading");
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
		const shared = { ...RUNNERS.runners[0], id: "rnr_s", name: "shared-runner-org_abc", pool: "shared", slots: 3 };
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
		expect(render(<RunnersTab org={org} />, hostedSession())).toContain("Create a worker token below, then start the worker with it.");
	});
	it("shows the role refusal to members (runners are admin)", () => {
		api.q.runners = errorQuery(403, "workers and worker tokens need the admin role");
		api.q.runnerTokens = errorQuery(403, "workers and worker tokens need the admin role");
		expect(render(<RunnersTab org={org} />, hostedSession("member"))).toContain("Your role cannot do this");
	});
	it("saves the token where the start command reads it", () => {
		const command = "bifrost-bench runner start -server https://h.dev -token-file /etc/heimdall/token -pool gpu";
		expect(tokenFileOf(command)).toBe("/etc/heimdall/token");
		expect(tokenFileOf("bifrost-bench runner start")).toBe("~/.heimdall-runner-token");
		expect(saveTokenCommand("hmr_x", "/etc/heimdall/token")).toBe(
			"mkdir -p /etc/heimdall && (umask 077 && printf '%s\\n' 'hmr_x' > /etc/heimdall/token)",
		);
	});
});

describe("AuditTab", () => {
	it("shows who did what to which target, when and from where, with paging", () => {
		const html = render(<AuditTab org={org} />, hostedSession("admin"));
		expect(html).toContain('data-testid="audit-row-aud_3"');
		// Wave 4 N33: actor ids read as member names, never addresses.
		expect(html).toContain("Ada Lovelace");
		expect(html).not.toContain("ada@acme.dev");
		expect(html).toContain("member.role");
		expect(html).toContain("user:usr_grace");
		expect(html).toContain("10.0.0.7");
		expect(html).toContain("from=member  to=admin");
		expect(tagOf(html, "audit-newer")).toContain('disabled=""');
		expect(tagOf(html, "audit-older")).toContain('disabled=""'); // a short page is the last
	});
	it("says when nothing matches", () => {
		api.q.audit = okQuery({ mode: "live", entries: [] });
		expect(render(<AuditTab org={org} />, hostedSession())).toContain("audit-empty");
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
		expect(leaveOrgRequest("org 1")).toEqual({ url: "/orgs/org%201/leave", method: "POST" });
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
		expect(tagOf(html, "password-current")).toContain('autoComplete="current-password"');
		expect(tagOf(html, "password-new")).toContain('autoComplete="new-password"');
		expect(html).toContain('data-testid="password-repeat"');
		expect(html).toContain("Your other signed-in sessions end; this one stays signed in.");
	});
	it("shows the server's reason when the change fails", () => {
		api.m.changePassword = { error: { status: 403, data: { error: { message: "current password is wrong" } } } };
		const html = render(<PasswordTab />, hostedSession("member"));
		expect(html).toContain('data-testid="password-error"');
		expect(html).toContain("current password is wrong");
	});
});

describe("audit export and integrity (X16)", () => {
	it("links JSONL and CSV downloads and offers the integrity check", () => {
		const html = render(<AuditTab org={org} />, hostedSession("admin"));
		expect(html).toContain(`href="/api/bench/orgs/${org.id}/audit/export?format=jsonl"`);
		expect(html).toContain(`href="/api/bench/orgs/${org.id}/audit/export?format=csv"`);
		expect(html).toContain('data-testid="audit-verify"');
		expect(html).toContain("Reads of traces, evidence and exports are logged here too");
	});
	it("shows an intact chain with its head and the older unchained entries", () => {
		api.m.verify = {
			data: { ok: true, entries: 120, chained: 118, legacy: 2, head_seq: 118, head_hash: "abcdef0123456789", broken_at: 0, reason: "" },
		};
		const html = render(<AuditTab org={org} />, hostedSession("admin"));
		expect(html).toContain("Audit log intact: 120 entries, chain head abcdef012345");
		expect(html).toContain("2 older entries were written before the chain existed");
	});
	it("shows where a tampered chain breaks", () => {
		api.m.verify = {
			data: { ok: false, entries: 120, chained: 118, legacy: 0, head_seq: 118, head_hash: "ff", broken_at: 57, reason: "hash mismatch" },
		};
		const html = render(<AuditTab org={org} />, hostedSession("admin"));
		expect(html).toContain('data-testid="audit-verify-broken"');
		expect(html).toContain("the chain breaks at entry 57");
		expect(html).toContain("Reason: hash mismatch");
		api.m.verify = {};
	});
});

describe("data retention and organization delete (X16)", () => {
	it("lets an admin pick how long comparisons are kept, with the last clean-up", async () => {
		const { retentionChoice, retentionDays } = await import("@/components/settings/dataControls");
		api.q.retention = okQuery({ run_days: 90, min_days: 1, max_days: 3650, last_sweep_at: "2026-11-20T00:00:00Z", last_deleted: 3 });
		const html = render(<OrgTab org={org} />, hostedSession("admin"));
		expect(html).toContain('data-testid="retention-form"');
		expect(html).toContain("Comparisons older than 90 days are deleted.");
		expect(html).toContain("3 comparisons deleted.");
		expect(html).toContain("The audit log is kept.");
		expect(retentionChoice(0)).toBe("0");
		expect(retentionChoice(45)).toBe("custom");
		expect(retentionDays("custom", "45", { min_days: 1, max_days: 3650 })).toBe(45);
		expect(retentionDays("custom", "0", { min_days: 1, max_days: 3650 })).toBeNull();
		expect(retentionDays("custom", "4000", { min_days: 1, max_days: 3650 })).toBeNull();
		expect(retentionDays("365", "", { min_days: 1, max_days: 3650 })).toBe(365);
	});
	it("shows retention read-only below admin", () => {
		api.q.retention = okQuery({ run_days: 0, min_days: 1, max_days: 3650 });
		const html = render(<OrgTab org={org} />, hostedSession("member"));
		expect(html).not.toContain('data-testid="retention-form"');
		expect(html).toContain("Everything is kept until you delete it.");
	});
	it("offers Delete organization to owners only", () => {
		api.q.retention = okQuery({ run_days: 0, min_days: 1, max_days: 3650 });
		expect(render(<OrgTab org={org} />, hostedSession("owner"))).toContain('data-testid="org-delete"');
		const admin = render(<OrgTab org={org} />, hostedSession("admin"));
		expect(admin).not.toContain('data-testid="org-delete"');
		expect(admin).toContain("Only owners can delete the organization.");
	});
	it("unlocks the delete dialog only when the short name is typed exactly", async () => {
		const { DeleteOrgDialog } = await import("@/components/settings/dataControls");
		const html = render(<DeleteOrgDialog info={ORG} onClose={() => {}} />, hostedSession("owner"));
		expect(html).toContain(`Type ${ORG.slug} to confirm`);
		expect(html).toContain("Audit log entries are kept.");
		expect(tagOf(html, "org-delete-submit")).toContain('disabled=""');
	});
});