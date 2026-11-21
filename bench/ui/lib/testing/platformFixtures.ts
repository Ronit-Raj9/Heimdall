// Typed sample answers of the hosted platform routes, for tests. Each is checked against its
// contract key list in platformFixtures.test.ts, so the TypeScript types, these samples and
// bench/handlers/testdata/contract/*.keys move together.
import { LOCAL_SESSION, sessionFromMe, type SessionInfo } from "@/lib/session/sessionContext";
import type {
	ApiTokenCreatedResponse,
	ApiTokenListResponse,
	AuditListResponse,
	AuthConfigResponse,
	AuthMeResponse,
	InvitationListResponse,
	InvitationPreviewResponse,
	InvitationResponse,
	Job,
	MemberListResponse,
	OrgListResponse,
	OrgResponse,
	RunnerListResponse,
	RunnerTokenCreatedResponse,
	RunnerTokenListResponse,
} from "@/lib/types/bench";

export const ME_OWNER: AuthMeResponse = {
	mode: "live",
	auth_mode: "hosted",
	kind: "user",
	user: { id: "usr_ada", email: "ada@acme.dev", name: "Ada Lovelace", created_at: "2026-11-01T09:00:00Z" },
	orgs: [
		{ id: "org_acme", slug: "acme", name: "Acme Agents", role: "owner" },
		{ id: "org_labs", slug: "labs", name: "Acme Labs", role: "viewer" },
	],
	current_org: "org_acme",
	role: "owner",
	csrf: "csrf-sample",
};

export const ME_LOCAL: AuthMeResponse = {
	mode: "live",
	auth_mode: "local",
	kind: "local",
	orgs: [{ id: "local", slug: "local", name: "Local", role: "owner" }],
	current_org: "local",
	role: "owner",
};

export const AUTH_CONFIG: AuthConfigResponse = { mode: "live", auth_mode: "hosted", signup: "open", github: true };

export const INVITATION_PREVIEW: InvitationPreviewResponse = {
	mode: "live",
	org_name: "Acme Agents",
	org_slug: "acme",
	email: "grace@acme.dev",
	role: "member",
	expires_at: "2026-11-25T09:00:00Z",
	account_exists: false,
	signup_allowed: true,
};

export const MEMBERS: MemberListResponse = {
	mode: "live",
	members: [
		{ user_id: "usr_ada", email: "ada@acme.dev", name: "Ada Lovelace", role: "owner", created_at: "2026-11-01T09:00:00Z" },
		{ user_id: "usr_grace", email: "grace@acme.dev", name: "Grace Hopper", role: "admin", created_at: "2026-11-03T10:00:00Z" },
		{ user_id: "usr_alan", email: "alan@acme.dev", role: "member", created_at: "2026-11-10T11:30:00Z" },
		{ user_id: "usr_kat", email: "kat@acme.dev", name: "Katherine Johnson", role: "viewer", created_at: "2026-11-12T08:15:00Z" },
	],
};

export const INVITATIONS: InvitationListResponse = {
	mode: "live",
	invitations: [
		{
			id: "inv_1",
			email: "edsger@acme.dev",
			role: "member",
			created_by: "usr_ada",
			created_at: "2026-11-17T09:00:00Z",
			expires_at: "2026-11-24T09:00:00Z",
		},
	],
};

export const INVITATION_CREATED: InvitationResponse = {
	mode: "live",
	id: "inv_2",
	email: "barbara@acme.dev",
	role: "admin",
	created_by: "usr_ada",
	created_at: "2026-11-18T06:00:00Z",
	expires_at: "2026-11-25T06:00:00Z",
	token: "inv_secret_token_value",
	url: "https://heimdall.acme.dev/invite/inv_secret_token_value",
};

export const API_TOKENS: ApiTokenListResponse = {
	mode: "live",
	tokens: [
		{
			id: "tok_ci",
			org_id: "org_acme",
			user_id: "usr_ada",
			name: "ci-release-gate",
			prefix: "a1b2c3d4",
			role: "member",
			scopes: ["read", "evaluate"],
			created_at: "2026-11-05T09:00:00Z",
			last_used_at: "2026-11-18T05:00:00Z",
			expires_at: "2027-02-03T09:00:00Z",
		},
		{
			id: "tok_old",
			org_id: "org_acme",
			name: "laptop-script",
			prefix: "e5f6a7b8",
			role: "viewer",
			created_at: "2026-10-01T09:00:00Z",
			revoked_at: "2026-11-02T09:00:00Z",
		},
	],
};

export const API_TOKEN_CREATED: ApiTokenCreatedResponse = {
	mode: "live",
	token: "hm_c9d0e1f2_s3cr3tvalueforonce",
	api_token: {
		id: "tok_new",
		org_id: "org_acme",
		user_id: "usr_ada",
		name: "nightly",
		prefix: "c9d0e1f2",
		role: "member",
		created_at: "2026-11-18T06:00:00Z",
	},
};

export const RUNNERS: RunnerListResponse = {
	mode: "live",
	runners: [
		{
			id: "rnr_1",
			org_id: "org_acme",
			name: "gpu-box-1",
			pool: "gpu",
			labels: ["cuda"],
			version: "0.9.2",
			host: "gpu-box-1.internal",
			state: "running",
			slots: 4,
			running: ["s_t01_A", "s_t02_B"],
			running_slots: 2,
			completed: 31,
			token_id: "rtk_1",
			token_prefix: "f00dcafe",
			last_seen: "2026-11-18T05:59:40Z",
			registered_at: "2026-11-15T12:00:00Z",
			live: true,
		},
		{
			id: "rnr_2",
			org_id: "org_acme",
			name: "spare-2",
			pool: "default",
			labels: null,
			version: "0.9.1",
			host: "spare-2.internal",
			state: "lost",
			slots: 2,
			running: null,
			running_slots: 0,
			completed: 4,
			token_id: "rtk_2",
			last_seen: "2026-11-17T22:10:00Z",
			registered_at: "2026-11-12T12:00:00Z",
			live: false,
		},
	],
};

export const RUNNER_TOKENS: RunnerTokenListResponse = {
	mode: "live",
	tokens: [
		{
			id: "rtk_1",
			org_id: "org_acme",
			pool: "gpu",
			name: "gpu-box-1",
			prefix: "f00dcafe",
			created_by: "usr_ada",
			created_at: "2026-11-15T11:50:00Z",
			last_used_at: "2026-11-18T05:59:40Z",
			state: "active",
		},
	],
};

export const RUNNER_TOKEN_CREATED: RunnerTokenCreatedResponse = {
	mode: "live",
	token: "hmr_0badf00d_runnersecretvalue",
	info: {
		id: "rtk_3",
		org_id: "org_acme",
		pool: "gpu",
		name: "gpu-box-2",
		prefix: "0badf00d",
		created_by: "usr_ada",
		created_at: "2026-11-18T06:00:00Z",
		state: "active",
	},
	command: "bifrost-bench runner start -server https://heimdall.acme.dev -token-file ~/.heimdall/runner-token -pool gpu",
	install: "curl -fsSL https://heimdall.acme.dev/api/bench/runners/install.sh | sh",
};

export const AUDIT: AuditListResponse = {
	mode: "live",
	entries: [
		{
			id: "aud_3",
			actor_type: "user",
			actor_id: "usr_ada",
			action: "member.role",
			target_type: "user",
			target_id: "usr_grace",
			ip: "10.0.0.7",
			at: "2026-11-18T05:40:00Z",
			details: { from: "member", to: "admin" },
		},
		{
			id: "aud_2",
			actor_type: "token",
			actor_id: "tok_ci",
			action: "experiment.create",
			target_type: "experiment",
			target_id: "e_nightly",
			at: "2026-11-18T03:00:00Z",
		},
		{
			id: "aud_1",
			actor_type: "runner",
			actor_id: "rtk_1",
			action: "runner.register",
			target_type: "runner",
			target_id: "rnr_1",
			at: "2026-11-15T12:00:00Z",
		},
	],
};

export const ORG: OrgResponse = {
	mode: "live",
	id: "org_acme",
	slug: "acme",
	name: "Acme Agents",
	public_read: false,
	created_at: "2026-11-01T09:00:00Z",
	role: "owner",
};

export const ORGS: OrgListResponse = {
	mode: "live",
	orgs: [{ id: "org_acme", slug: "acme", name: "Acme Agents", public_read: false, created_at: "2026-11-01T09:00:00Z", role: "owner" }],
};

export const RUNNING_JOB: Job = {
	id: "job_run",
	org_id: "org_acme",
	kind: "experiment.run",
	target: "e_nightly",
	status: "running",
	params: { exp_id: "e_nightly" },
	progress: { exp_id: "e_nightly", phase: "running", slots: 48, sealed: 29, skipped: 1, errors: 0 },
	created_by: "usr_ada",
	created_at: "2026-11-18T05:31:00Z",
	started_at: "2026-11-18T05:31:02Z",
	cancel_requested: false,
	attempt: 1,
};

export const FAILED_JOB: Job = {
	id: "job_fail",
	org_id: "org_acme",
	kind: "agent.check",
	target: "codex",
	status: "failed",
	params: { agent: "codex" },
	error: "gateway refused the connection",
	created_at: "2026-11-18T04:50:00Z",
	started_at: "2026-11-18T04:50:01Z",
	finished_at: "2026-11-18T04:50:31Z",
	cancel_requested: false,
	attempt: 1,
};

export const REGRADE_JOB: Job = {
	id: "job_rg",
	org_id: "org_acme",
	kind: "experiment.regate",
	target: "e_nightly",
	status: "succeeded",
	params: { exp_id: "e_nightly", id: "rg_42" },
	result: { regrade_id: "rg_42", runs: 48 },
	created_at: "2026-11-18T04:00:00Z",
	started_at: "2026-11-18T04:00:01Z",
	finished_at: "2026-11-18T04:00:20Z",
	cancel_requested: false,
	attempt: 1,
};

/** The session of a signed-in user of Acme Agents with the given role. */
export function hostedSession(role: "viewer" | "member" | "admin" | "owner" = "owner"): SessionInfo {
	return sessionFromMe({ me: { ...ME_OWNER, role }, meLoading: false, config: AUTH_CONFIG });
}

/**
 * An anonymous visitor of a hosted server. Hosted installs serve no public demo, so this is the same
 * as signedOutSession; the name is kept for older tests that check write controls stay hidden.
 */
export function demoSession(): SessionInfo {
	return signedOutSession();
}

/** An anonymous visitor of a hosted server with no public demo. */
export function signedOutSession(config = AUTH_CONFIG): SessionInfo {
	return sessionFromMe({ meError: { status: 401, data: {} }, meLoading: false, config });
}

export { LOCAL_SESSION };