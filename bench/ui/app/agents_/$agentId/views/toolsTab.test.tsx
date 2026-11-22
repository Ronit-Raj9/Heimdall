import "@/lib/i18n";

import type { AgentSkillsResponse, SkillScanResult } from "@/lib/types/agentops";
import i18n from "i18next";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { initialSkillForm, skillFormReducer, skillScanMessage, skillUploadMessage, SkillsSection, ToolsTab } from "./toolsTab";

const state = vi.hoisted(() => ({ skills: undefined as AgentSkillsResponse | undefined }));

vi.mock("@/lib/store/apis/agentopsApi", () => ({
	useGetAgentSkillsQuery: () => ({ data: state.skills, isLoading: false }),
	useGetAgentMCPQuery: () => ({ data: undefined, isLoading: false }),
	useUploadAgentSkillMutation: () => [vi.fn(), { isLoading: false }],
	useScanAgentSkillMutation: () => [vi.fn(), { isLoading: false }],
	useDeleteAgentSkillMutation: () => [vi.fn(), { isLoading: false }],
	usePutAgentMCPMutation: () => [vi.fn(), { isLoading: false }],
}));
vi.mock("@/lib/store/apis/platformApi", () => ({ useListJobsQuery: () => ({ data: { jobs: [] } }) }));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => [vi.fn(), { isLoading: false }] }));
vi.mock("@/components/platform/useJobLog", () => ({ useJobLog: () => ({ lines: [] }) }));
vi.mock("@tanstack/react-router", () => ({ useParams: () => ({}) }));
vi.mock("@/lib/session/sessionContext", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/session/sessionContext")>()),
	useSession: () => ({ authMode: "local" }),
}));

const t = i18n.getFixedT("en", "agentops") as unknown as Parameters<typeof skillUploadMessage>[0];
const file = (name: string) => ({ name, size: 10 }) as File;

const blockedFinding = {
	rule: "injection",
	severity: "block",
	file: "SKILL.md",
	line: 7,
	detail: "asks the agent to ignore its instructions",
};

describe("tools tab skills (fix wave 2, N22 to N24)", () => {
	it("shows a status badge per skill and the findings of a blocked one", () => {
		state.skills = {
			agent: "codex",
			config_path: "agents/codex.yaml",
			dir: "agents/codex/skills",
			installed: true,
			skills: [
				{
					name: "bad",
					path: "agents/codex/skills-pending/bad",
					files: 1,
					bytes: 100,
					valid: true,
					status: "blocked",
					scan: { decision: "blocked", block: 1, warn: 0, scanned_at: "2026-11-23T10:00:00Z", findings: [blockedFinding] },
				},
				{ name: "old", path: "agents/codex/skills/old", files: 1, bytes: 100, valid: true, status: "not_scanned" },
				{
					name: "review",
					path: "agents/codex/skills/review",
					files: 2,
					bytes: 2048,
					valid: true,
					status: "installed",
					scan: { decision: "admitted", block: 0, warn: 0, scanned_at: "2026-11-23T10:00:00Z", findings: [] },
				},
			],
		};
		const html = renderToStaticMarkup(<ToolsTab agentId="codex" />);
		expect(html).toContain('data-testid="agent-skill-status-review"');
		expect(html).toContain("Installed");
		expect(html).toContain("Blocked by the scan");
		expect(html).toContain("Not scanned: runs refuse it until it passes a scan.");
		expect(html).toContain('data-testid="agent-skill-findings-bad"');
		expect(html).toContain("SKILL.md:7");
		expect(html).toContain("asks the agent to ignore its instructions");
		expect(html).toContain('data-testid="agent-skill-scan-review"');
		expect(html).not.toContain('data-testid="job-run-scan.skill"');
	});

	it("says a blocked upload was not installed, and a clean one was", () => {
		const blocked: SkillScanResult = {
			skill: { name: "bad", path: "agents/codex/skills-pending/bad", files: 1, bytes: 1, valid: true, status: "blocked" },
			installed: false,
			record: { decision: "blocked", counts: { block: 2, warn: 1, info: 0 }, findings: [] },
		};
		expect(skillUploadMessage(t, blocked)).toEqual({
			ok: false,
			text: "Skill bad was not installed: the scan found 2 problems that block it.",
		});
		const clean: SkillScanResult = {
			...blocked,
			skill: { ...blocked.skill, name: "review", status: "installed" },
			installed: true,
			record: { decision: "admitted", counts: { block: 0, warn: 1, info: 0 }, findings: [] },
		};
		expect(skillUploadMessage(t, clean).text).toBe(
			"Skill review passed the scan and is installed. Every run of this agent now installs it. 1 warnings, see below.",
		);
		expect(skillScanMessage(t, blocked).text).toBe("Blocked by the scan: 2 problems that block, 1 warnings.");
	});

	it("name follows each picked file until typed in, and the form resets after upload", () => {
		let s = skillFormReducer(initialSkillForm, { type: "pick", file: file("a.zip") });
		s = skillFormReducer(s, { type: "pick", file: file("b.zip") });
		expect(s.name).toBe("b");
		s = skillFormReducer(s, { type: "name", name: "mine" });
		s = skillFormReducer(s, { type: "pick", file: file("c.zip") });
		expect(s.name).toBe("mine");
		const key = s.inputKey;
		s = skillFormReducer(s, { type: "uploaded" });
		expect(s).toEqual({ ...initialSkillForm, inputKey: key + 1 });
		s = skillFormReducer(s, { type: "pick", file: file("c.zip") });
		expect(s.name).toBe("c");
	});

	it("shows where a picked skill will be saved", () => {
		state.skills = { agent: "codex", config_path: "agents/codex.yaml", dir: "agents/codex/skills", installed: false, skills: [] };
		const html = renderToStaticMarkup(
			<SkillsSection id="codex" initial={skillFormReducer(initialSkillForm, { type: "pick", file: file("review.zip") })} />,
		);
		expect(html).toContain("Will be saved as skill review in agents/codex/skills/review/.");
	});
});