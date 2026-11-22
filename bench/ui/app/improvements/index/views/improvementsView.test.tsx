import "@/lib/i18n";

import { IMPROVEMENT_FIXTURE } from "@/components/improvements/improve.fixtures";
import { NewImprovementForm, improvementFormProblems, improvementSpec, type ImprovementForm } from "@/components/improvements/newImprovementDialog";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { okQuery, render } from "@/lib/testing/render";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import ImprovementsView, { ImprovementsTable } from "./improvementsView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/improveApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/improveApi")>()),
	useGetImprovementsQuery: () => okQuery({ improvements: [] }),
}));

const filled: ImprovementForm = {
	name: "notes-loop",
	agent: "acme-agent",
	suite: "hd-harbor-bfcl@1.0.0",
	files: ["/home/agent/NOTES.md", "/home/agent/skills/"],
	rounds: "3",
	budget: "2.00",
};

describe("ImprovementsTable", () => {
	it("lists runs with status, rounds and held-out gain", () => {
		const html = renderToStaticMarkup(<ImprovementsTable rows={[IMPROVEMENT_FIXTURE]} />);
		expect(html).toContain('data-testid="improvements-table"');
		expect(html).toContain("notes-loop");
		expect(html).toContain("2 of 2");
		expect(html).toContain("+20.0 pts");
	});

	it("explains how to start when there are none, the command line inside a disclosure with Copy", () => {
		const html = renderToStaticMarkup(<ImprovementsTable rows={[]} admin />);
		expect(html).toContain('data-testid="improvements-empty"');
		expect(html).toContain("No improvement runs yet. Start one with New improvement run.");
		expect(html).toContain("Use the command line instead");
		expect(html).toContain("heimdall improve create");
		expect(html).toContain('data-testid="improvements-cli-copy"');
		expect(html).not.toContain("bifrost-bench");
	});
});

describe("ImprovementsView", () => {
	it("offers New improvement run to an admin", () => {
		const html = render(<ImprovementsView />, hostedSession("admin"));
		expect(html).toContain('data-testid="improvements-new"');
		expect(html).toContain("New improvement run");
		expect(html).not.toContain('data-testid="improvements-admin-only"');
	});

	it("tells a member why only admins can start one", () => {
		const html = render(<ImprovementsView />, hostedSession("member"));
		expect(html).not.toContain('data-testid="improvements-new"');
		expect(html).toContain("Only organization admins can start an improvement run, because it spends the organization&#x27;s model keys.");
	});
});

describe("NewImprovementForm", () => {
	const props = { onChange: () => undefined, onSubmit: () => undefined, onCancel: () => undefined, agents: ["acme-agent"], suites: ["hd-harbor-bfcl@1.0.0"] };

	it("disables Create and says why when the agent has no files", () => {
		const html = render(<NewImprovementForm {...props} form={{ ...filled, files: [] }} agentFiles={[]} />);
		expect(html).toContain("This agent has no files Heimdall can change (its settings have no files section).");
		expect(html).toMatch(/<button[^>]*disabled=""[^>]*data-testid="new-improvement-submit"|<button[^>]*data-testid="new-improvement-submit"[^>]*disabled=""/);
		expect(improvementFormProblems({ ...filled, files: [] }, [])).toContain("noFiles");
	});

	it("lists the agent's files and enables Create when the form is complete", () => {
		const html = render(<NewImprovementForm {...props} form={filled} agentFiles={["/home/agent/NOTES.md", "/home/agent/skills/"]} />);
		expect(html).toContain("/home/agent/NOTES.md");
		expect(html).not.toMatch(/<button[^>]*disabled=""[^>]*data-testid="new-improvement-submit"/);
		expect(html).toContain("Each round runs the agent on practice, gate and held-out tasks");
		expect(improvementFormProblems(filled, ["/home/agent/NOTES.md", "/home/agent/skills/"])).toEqual([]);
	});

	it("checks the name, rounds and budget rules", () => {
		const files = ["/home/agent/NOTES.md"];
		expect(improvementFormProblems({ ...filled, name: "Bad Name" }, files)).toContain("name");
		expect(improvementFormProblems({ ...filled, rounds: "0" }, files)).toContain("rounds");
		expect(improvementFormProblems({ ...filled, rounds: "21" }, files)).toContain("rounds");
		expect(improvementFormProblems({ ...filled, budget: "0" }, files)).toContain("budget");
		expect(improvementFormProblems({ ...filled, budget: "" }, files)).toContain("budget");
	});

	it("posts the spec body: agent path, suite with version, files and folders apart, budget per round", () => {
		expect(improvementSpec(filled)).toEqual({
			kind: "improvement",
			name: "notes-loop",
			agent: "agents/acme-agent.yaml",
			suite: "hd-harbor-bfcl@1.0.0",
			rounds: 3,
			editable: { files: ["/home/agent/NOTES.md"], dirs: ["/home/agent/skills/"] },
			improver: { budget_usd: 2 },
		});
	});
});
