import "@/lib/i18n";

import { ESTIMATE_FIXTURE, IMPROVEMENT_FIXTURE } from "@/components/improvements/improve.fixtures";
import {
	NewImprovementForm,
	improvementFormProblems,
	improvementSpec,
	newForm,
	type ImprovementForm,
} from "@/components/improvements/newImprovementDialog";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { okQuery, render } from "@/lib/testing/render";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import ImprovementsView, { ImprovementsTable } from "./improvementsView";

vi.mock("@tanstack/react-router", async () => ({ ...(await import("@/lib/testing/routerMock")).routerMock, useBlocker: () => undefined }));
vi.mock("@/lib/store/apis/improveApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/improveApi")>()),
	useGetImprovementsQuery: () => okQuery({ improvements: [] }),
}));

const filled: ImprovementForm = {
	...newForm(),
	name: "notes-loop",
	agent: "acme-agent",
	suite: "hd-harbor-bfcl@1.0.0",
	files: ["/home/agent/NOTES.md", "/home/agent/skills/"],
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
		expect(html).toContain("Each round costs model calls.");
		expect(html).toContain("Use the command line instead");
		expect(html).toContain("heimdall improve create");
		expect(html).toContain('data-testid="improvements-cli-copy"');
		expect(html).not.toContain("bifrost-bench");
	});

	it("puts New improvement run in the empty state for an admin only", () => {
		const admin = renderToStaticMarkup(<ImprovementsTable rows={[]} admin onCreate={() => undefined} />);
		expect(admin).toContain("New improvement run");
		const member = renderToStaticMarkup(<ImprovementsTable rows={[]} onCreate={() => undefined} />);
		expect(member).not.toContain("New improvement run");
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
		expect(html).toContain("Only organization admins can start this. It spends model keys.");
	});
});

describe("NewImprovementForm", () => {
	const props = {
		onChange: () => undefined,
		onSubmit: () => undefined,
		onCancel: () => undefined,
		agents: ["acme-agent"],
		suites: ["hd-harbor-bfcl@1.0.0"],
	};

	it("offers standing instructions when the agent has no files, so Create is not blocked by them", () => {
		const html = render(<NewImprovementForm {...props} form={filled} agentFiles={[]} estimate={ESTIMATE_FIXTURE} />);
		expect(html).not.toContain("This agent has no files Heimdall can change (its settings have no files section).");
		expect(html).toContain("Standing instructions");
		expect(html).not.toMatch(
			/<button[^>]*disabled=""[^>]*data-testid="new-improvement-submit"|<button[^>]*data-testid="new-improvement-submit"[^>]*disabled=""/,
		);
		expect(improvementFormProblems(filled)).toEqual([]);
	});

	it("lists the agent's files and enables Create when the form is complete", () => {
		const html = render(
			<NewImprovementForm
				{...props}
				form={filled}
				agentFiles={["/home/agent/NOTES.md", "/home/agent/skills/"]}
				estimate={ESTIMATE_FIXTURE}
			/>,
		);
		expect(html).toContain("/home/agent/NOTES.md");
		expect(html).not.toMatch(/<button[^>]*disabled=""[^>]*data-testid="new-improvement-submit"/);
		expect(improvementFormProblems(filled)).toEqual([]);
	});

	it("checks the name, size and spending limit rules", () => {
		expect(improvementFormProblems({ ...filled, name: "Bad Name" })).toContain("name");
		expect(improvementFormProblems({ ...filled, preset: "custom", rounds: "0" })).toContain("rounds");
		expect(improvementFormProblems({ ...filled, preset: "custom", rounds: "21" })).toContain("rounds");
		expect(improvementFormProblems({ ...filled, limit: "0" })).toContain("limit");
		expect(improvementFormProblems({ ...filled, limit: "" })).toContain("limit");
	});

	it("posts the spec body: agent path, suite with version, files and folders apart, the preset and the limit", () => {
		expect(improvementSpec(filled)).toEqual({
			kind: "improvement",
			name: "notes-loop",
			agent: "agents/acme-agent.yaml",
			suite: "hd-harbor-bfcl@1.0.0",
			preset: "small",
			editable: { instructions: true, files: ["/home/agent/NOTES.md"], dirs: ["/home/agent/skills/"] },
			improver: { task: "heimdall-improver@1.1.0:reflect-edit" },
			budget: { total_usd: 15 },
		});
	});
});