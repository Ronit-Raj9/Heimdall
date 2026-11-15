import "@/lib/i18n";

import type { ArtifactSpec, SuiteList } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { SuiteFacts } from "./suiteFacts";
import { TaskScenario } from "./suiteScenario";
import { TaskDetailBody } from "./taskDetailPanel";
import { DeclaredArtifacts } from "./taskArtifacts";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, children, ...rest }: { to: string; search: unknown; children: React.ReactNode }) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
}));

const list = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/suites.json"), "utf8")) as SuiteList;
const core = list.suites.find((s) => s.id === "coding-core")!;
const plainTask = core.tasks.find((t) => t.id === "t01")!;

const artifacts: ArtifactSpec[] = [
	{ id: "report", path: "out/report.csv", required: true, max_bytes: 1_048_576 },
	{ id: "charts", path: "out/*.svg" },
];

describe("DeclaredArtifacts", () => {
	it("lists id, path, required and the size cap of each declared artifact", () => {
		const html = renderToStaticMarkup(<DeclaredArtifacts artifacts={artifacts} />);
		expect(html).toContain("Expected artifacts");
		expect(html).toMatch(
			/data-testid="suites-task-artifacts-report" data-required="true"[\s\S]*?out\/report.csv[\s\S]*?required[\s\S]*?up to 1 MB per file/,
		);
		expect(html).toMatch(
			/data-testid="suites-task-artifacts-charts" data-required="false"[\s\S]*?out\/\*.svg[\s\S]*?optional[\s\S]*?up to 32 MiB per file \(default\)/,
		);
		expect(html).not.toContain("<a ");
	});

	it("renders nothing for an empty list", () => {
		expect(renderToStaticMarkup(<DeclaredArtifacts artifacts={[]} />)).toBe("");
	});
});

describe("TaskScenario with artifact output", () => {
	it("shows the artifact kind and the declared artifacts in the output facts", () => {
		const html = renderToStaticMarkup(<TaskScenario task={{ ...plainTask, output: { kind: "artifact", artifacts } }} suiteOutput="diff" />);
		expect(html).toMatch(/data-testid="suites-task-output-kind"[^>]*>Artifacts</);
		expect(html).toMatch(/data-testid="suites-task-output"[\s\S]*data-testid="suites-task-artifacts"/);
		expect(html).toContain('data-testid="suites-task-artifacts-report"');
	});

	it("shows the suite's artifact kind when the task sets no output", () => {
		const html = renderToStaticMarkup(<TaskScenario task={{ ...plainTask, skills: ["x"] }} suiteOutput="artifact" />);
		expect(html).toMatch(/data-testid="suites-task-output".*?Artifacts.*?from the suite/);
		expect(html).not.toContain("suites-task-artifacts");
	});
});

describe("suite artifacts", () => {
	it("shows the suite's declared artifacts in its facts, and nothing for a suite without", () => {
		const html = renderToStaticMarkup(<SuiteFacts suite={{ ...core, output: "artifact", artifacts }} />);
		expect(html).toMatch(/data-testid="suites-fact-output"[\s\S]*?>Artifacts</);
		expect(html).toMatch(/data-testid="suites-artifacts" data-from-suite="false"/);
		expect(html).toContain('data-testid="suites-artifacts-report"');
		expect(html).toContain('data-testid="suites-artifacts-charts"');
		expect(renderToStaticMarkup(<SuiteFacts suite={core} />)).not.toContain("suites-artifacts");
	});

	it("shows the suite's list, labelled as the suite's, for a task that declares none", () => {
		const html = renderToStaticMarkup(<TaskScenario task={plainTask} suiteOutput="artifact" suiteArtifacts={artifacts} />);
		expect(html).toMatch(/data-testid="suites-task-output".*?Artifacts.*?from the suite/);
		expect(html).toMatch(/data-testid="suites-task-artifacts" data-from-suite="true"[\s\S]*?Expected artifacts[\s\S]*?from the suite/);
		expect(html).toContain('data-testid="suites-task-artifacts-report"');
	});

	it("uses the suite's list for an artifact task with no list of its own, and the task's own list otherwise", () => {
		const kindOnly = renderToStaticMarkup(
			<TaskScenario task={{ ...plainTask, output: { kind: "artifact" } }} suiteOutput="artifact" suiteArtifacts={artifacts} />,
		);
		expect(kindOnly).toMatch(/data-from-suite="true"/);
		const own = renderToStaticMarkup(
			<TaskScenario
				task={{ ...plainTask, output: { kind: "artifact", artifacts: [{ id: "mesh", path: "out/part.stl" }] } }}
				suiteOutput="artifact"
				suiteArtifacts={artifacts}
			/>,
		);
		expect(own).toContain('data-testid="suites-task-artifacts-mesh"');
		expect(own).not.toContain('data-from-suite="true"');
		expect(own).not.toContain("suites-task-artifacts-report");
	});

	it("shows no suite list for a task of another output kind", () => {
		expect(renderToStaticMarkup(<TaskScenario task={plainTask} suiteOutput="diff" suiteArtifacts={artifacts} />)).toBe("");
		const answer = renderToStaticMarkup(
			<TaskScenario task={{ ...plainTask, output: { kind: "answer" } }} suiteOutput="artifact" suiteArtifacts={artifacts} />,
		);
		expect(answer).not.toContain("suites-task-artifacts");
	});

	it("passes the suite's list through the task detail", () => {
		const detail = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/suites.task.t01.json"), "utf8"));
		const html = renderToStaticMarkup(
			<TaskDetailBody
				detail={{ ...detail, task: plainTask }}
				suiteVersion={core.version}
				suiteOutput="artifact"
				suiteArtifacts={artifacts}
			/>,
		);
		expect(html).toMatch(/data-testid="suites-task-artifacts" data-from-suite="true"/);
	});
});