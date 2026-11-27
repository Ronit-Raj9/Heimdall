import "@/lib/i18n";

import { loadingQuery, mutation, okQuery, render } from "@/lib/testing/render";
import type { AgentSkill } from "@/lib/types/agentops";
import { describe, expect, it, vi } from "vitest";
import { SkillScanBar, unscannedSkills } from "./skillScanBar";

const state = vi.hoisted(() => ({ skills: undefined as AgentSkill[] | undefined }));

vi.mock("@/lib/store/apis/agentopsApi", () => ({
	useGetAgentSkillsQuery: () =>
		state.skills ? okQuery({ agent: "zz-test-loop", config_path: "", dir: "", installed: true, skills: state.skills }) : loadingQuery(),
	useScanAgentSkillMutation: () => mutation(),
}));

const skill = (name: string, status: AgentSkill["status"]): AgentSkill => ({
	name,
	path: `agents/zz-test-loop/skills/${name}`,
	files: 1,
	bytes: 10,
	valid: true,
	status,
});
const noop = () => undefined;

describe("SkillScanBar (L5)", () => {
	it("shows nothing while loading or when every skill passed its scan", () => {
		state.skills = undefined;
		expect(render(<SkillScanBar agentId="zz-test-loop" onOpenTools={noop} />)).toBe("");
		state.skills = [skill("good", "installed")];
		expect(render(<SkillScanBar agentId="zz-test-loop" onOpenTools={noop} />)).toBe("");
	});

	it("warns about one unscanned skill with Scan now and the tools", () => {
		state.skills = [skill("good", "installed"), skill("evil", "not_scanned")];
		const html = render(<SkillScanBar agentId="zz-test-loop" onOpenTools={noop} />);
		expect(html).toContain('data-testid="skill-scan-bar"');
		expect(html).toContain("1 skill has not passed a scan. Runs are refused until it does.");
		expect(html).toContain("Scan now");
		expect(html).toContain("Open the tools");
	});

	it("counts blocked skills too, and offers Scan now only for unscanned ones", () => {
		state.skills = [skill("evil", "not_scanned"), skill("bad", "blocked")];
		const html = render(<SkillScanBar agentId="zz-test-loop" onOpenTools={noop} />);
		expect(html).toContain("2 skills have not passed a scan. Runs are refused until they do.");
		expect(html).toContain('data-testid="skill-scan-bar-scan"');
		state.skills = [skill("bad", "blocked")];
		const blocked = render(<SkillScanBar agentId="zz-test-loop" onOpenTools={noop} />);
		expect(blocked).toContain("1 skill has not passed a scan");
		expect(blocked).not.toContain('data-testid="skill-scan-bar-scan"');
		expect(blocked).toContain("Open the tools");
		expect(unscannedSkills([skill("a", "installed"), skill("b", "blocked"), skill("c", "not_scanned")]).map((s) => s.name)).toEqual([
			"b",
			"c",
		]);
	});
});