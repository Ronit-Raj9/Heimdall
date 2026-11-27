import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { ESTIMATE_FIXTURE } from "./improve.fixtures";
import {
	DEFAULT_IMPROVER_TASK,
	type ImprovementForm,
	NewImprovementForm,
	createBlock,
	improvementFormProblems,
	improvementSpec,
	newForm,
} from "./newImprovementDialog";

const filled: ImprovementForm = { ...newForm(), name: "notes-loop", agent: "acme-agent", suite: "hd-harbor-bfcl@1.0.0" };
const props = {
	onChange: () => undefined,
	onSubmit: () => undefined,
	onCancel: () => undefined,
	agents: ["acme-agent"],
	suites: ["hd-harbor-bfcl@1.0.0"],
	model: "gpt-5-mini",
};
const submitDisabled =
	/<button[^>]*disabled=""[^>]*data-testid="new-improvement-submit"|<button[^>]*data-testid="new-improvement-submit"[^>]*disabled=""/;

describe("NewImprovementForm (wave 4)", () => {
	it("offers standing instructions first and ticked, and never the old no-files dead end", () => {
		const html = render(<NewImprovementForm {...props} form={filled} agentFiles={[]} estimate={ESTIMATE_FIXTURE} />);
		expect(html).toContain("Standing instructions");
		expect(html).toMatch(/<input(?=[^>]*checked="")[^>]*data-testid="new-improvement-instructions"/);
		expect(html).not.toContain("This agent has no files Heimdall can change");
		expect(html).toContain("Kept fixed: model, tools, settings, budgets.");
		expect(html).not.toMatch(submitDisabled);
		expect(improvementFormProblems(filled)).toEqual([]);
	});

	it("lists the agent's files after the instructions, folders with their note", () => {
		const html = render(
			<NewImprovementForm
				{...props}
				form={filled}
				agentFiles={["/home/agent/NOTES.md", "/home/agent/skills/"]}
				estimate={ESTIMATE_FIXTURE}
			/>,
		);
		expect(html.indexOf("Standing instructions")).toBeLessThan(html.indexOf("/home/agent/NOTES.md"));
		expect(html).toContain("Improver may add skills here.");
	});

	it("disables Create with the estimate's refusal as its reason", () => {
		const refused = {
			...ESTIMATE_FIXTURE,
			ok: false,
			refusals: [
				{
					code: "over_limit",
					message: "The estimate ($10.00 to $20.00) is above your spending limit of $15.00. Raise the limit or pick a smaller preset.",
				},
			],
		};
		const html = render(<NewImprovementForm {...props} form={filled} agentFiles={[]} estimate={refused} />);
		expect(html).toMatch(submitDisabled);
		expect(html).toContain('data-testid="new-improvement-reason"');
		expect(createBlock(filled, refused, false)).toBe(refused.refusals[0].message);
	});

	it("disables Create while the estimate loads and when the form has a problem, first reason shown", () => {
		expect(createBlock(filled, undefined, true)).toBe("Waiting for the estimate.");
		expect(createBlock({ ...filled, name: "" }, ESTIMATE_FIXTURE, false)).toBe("Give the run a name.");
		expect(createBlock({ ...filled, instructions: false }, ESTIMATE_FIXTURE, false)).toBe(
			"Tick at least one thing the improver may change.",
		);
		expect(createBlock(filled, ESTIMATE_FIXTURE, false)).toBeUndefined();
	});

	it("sends the preset, the improver task, the editable instructions and the spending limit", () => {
		const body = improvementSpec({ ...filled, files: ["/home/agent/NOTES.md", "/home/agent/skills/"] });
		expect(body).toEqual({
			kind: "improvement",
			name: "notes-loop",
			agent: "agents/acme-agent.yaml",
			suite: "hd-harbor-bfcl@1.0.0",
			preset: "small",
			editable: { instructions: true, files: ["/home/agent/NOTES.md"], dirs: ["/home/agent/skills/"] },
			improver: { task: DEFAULT_IMPROVER_TASK },
			budget: { total_usd: 15 },
		});
		expect(DEFAULT_IMPROVER_TASK).toBe("heimdall-improver@1.1.0:reflect-edit");
	});

	it("sends rounds and ideas per round only for a custom size", () => {
		const body = improvementSpec({ ...filled, preset: "custom", rounds: "4", ideas: "3", limit: "25" });
		expect(body.preset).toBe("custom");
		expect(body.rounds).toBe(4);
		expect(body.improver).toEqual({ task: DEFAULT_IMPROVER_TASK, candidates: 3, keep: 3 });
		expect(body.budget).toEqual({ total_usd: 25 });
		expect(improvementFormProblems({ ...filled, preset: "custom", rounds: "0" })).toContain("rounds");
		expect(improvementFormProblems({ ...filled, preset: "custom", ideas: "9" })).toContain("ideas");
		expect(improvementFormProblems({ ...filled, limit: "0" })).toContain("limit");
	});
});