// The platform samples against the contract key lists the Go side reads
// (bench/handlers/testdata/contract), as fixtures.test.ts does for the bench fixtures.
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import * as fx from "./platformFixtures";

const CONTRACT_DIR = path.resolve(__dirname, "../../../handlers/testdata/contract");

function keys(type: string): { required: string[]; optional: string[] } {
	const lines = fs
		.readFileSync(path.join(CONTRACT_DIR, `${type}.keys`), "utf8")
		.split("\n")
		.map((line) => line.trim())
		.filter((line) => line && !line.startsWith("#"));
	return { required: lines.filter((l) => !l.endsWith("?")), optional: lines.filter((l) => l.endsWith("?")).map((l) => l.slice(0, -1)) };
}

function problems(where: string, obj: Record<string, unknown>, type: string, topLevel: boolean): string[] {
	const { required, optional } = keys(type);
	const need = topLevel ? [...required, "mode"] : required;
	const allowed = new Set([...need, ...optional]);
	return [
		...need.filter((k) => !(k in obj)).map((k) => `${where}: missing ${k}`),
		...Object.keys(obj)
			.filter((k) => !allowed.has(k))
			.map((k) => `${where}: ${k} not in ${type}`),
	];
}

type Obj = Record<string, unknown>;
const each = (list: unknown) => (Array.isArray(list) ? (list as Obj[]) : []);

describe("platform samples", () => {
	it("match the contract key lists", () => {
		const found: string[] = [];
		const top = (name: string, obj: unknown, type: string) => found.push(...problems(name, obj as Obj, type, true));
		const nested = (name: string, obj: unknown, type: string) => found.push(...problems(name, obj as Obj, type, false));
		top("ME_OWNER", fx.ME_OWNER, "AuthMe");
		nested("ME_OWNER.user", fx.ME_OWNER.user, "PlatformUser");
		each(fx.ME_OWNER.orgs).forEach((o, i) => nested(`ME_OWNER.orgs[${i}]`, o, "MeOrg"));
		top("ME_LOCAL", fx.ME_LOCAL, "AuthMe");
		top("AUTH_CONFIG", fx.AUTH_CONFIG, "AuthConfig");
		top("INVITATION_PREVIEW", fx.INVITATION_PREVIEW, "InvitationPreview");
		top("MEMBERS", fx.MEMBERS, "MemberList");
		each(fx.MEMBERS.members).forEach((m, i) => nested(`MEMBERS[${i}]`, m, "Member"));
		top("INVITATIONS", fx.INVITATIONS, "InvitationList");
		each(fx.INVITATIONS.invitations).forEach((m, i) => nested(`INVITATIONS[${i}]`, m, "Invitation"));
		top("INVITATION_CREATED", fx.INVITATION_CREATED, "Invitation");
		top("API_TOKENS", fx.API_TOKENS, "ApiTokenList");
		each(fx.API_TOKENS.tokens).forEach((m, i) => nested(`API_TOKENS[${i}]`, m, "ApiToken"));
		top("API_TOKEN_CREATED", fx.API_TOKEN_CREATED, "ApiTokenCreated");
		nested("API_TOKEN_CREATED.api_token", fx.API_TOKEN_CREATED.api_token, "ApiToken");
		top("RUNNERS", fx.RUNNERS, "RunnerList");
		each(fx.RUNNERS.runners).forEach((m, i) => nested(`RUNNERS[${i}]`, m, "Runner"));
		top("RUNNER_TOKENS", fx.RUNNER_TOKENS, "RunnerTokenList");
		each(fx.RUNNER_TOKENS.tokens).forEach((m, i) => nested(`RUNNER_TOKENS[${i}]`, m, "RunnerToken"));
		top("RUNNER_TOKEN_CREATED", fx.RUNNER_TOKEN_CREATED, "RunnerTokenCreated");
		nested("RUNNER_TOKEN_CREATED.info", fx.RUNNER_TOKEN_CREATED.info, "RunnerToken");
		top("AUDIT", fx.AUDIT, "AuditList");
		each(fx.AUDIT.entries).forEach((m, i) => nested(`AUDIT[${i}]`, m, "AuditEntry"));
		top("ORG", fx.ORG, "OrgInfo");
		top("ORGS", fx.ORGS, "OrgList");
		each(fx.ORGS.orgs).forEach((m, i) => nested(`ORGS[${i}]`, m, "OrgInfo"));
		for (const job of [fx.RUNNING_JOB, fx.FAILED_JOB, fx.REGRADE_JOB]) nested(job.id, job, "Job");
		expect(found).toEqual([]);
	});
});