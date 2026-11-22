import "@/lib/i18n";

import { LOCAL_SESSION } from "@/lib/session/sessionContext";
import { mutation, render } from "@/lib/testing/render";
import type { SuiteSummary } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { BuildTab, formatOfFile, graderChoice, judgeCalls, RowPreview, sourcesFor } from "./buildTab";
import i18n from "i18next";
import {
	attachmentProblem,
	attachmentsBody,
	columnLabel,
	dataMode,
	hasConversations,
	ImportProgress,
	MappingStep,
	mappingBody,
} from "./rowMapper";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
const mockSplit = vi.hoisted(() => ({ state: {} as Record<string, unknown> }));
vi.mock("@/lib/store/apis/suiteBuildApi", () => ({
	useImportSuiteRowsMutation: () => mutation(),
	useGetRowImportQuery: () => ({ data: undefined }),
	useSetSuiteGraderMutation: () => mutation(),
	useSetTaskSplitMutation: () => mutation(mockSplit.state),
}));
vi.mock("@/lib/store/apis/suiteOpsApi", () => ({ useQuarantineTaskMutation: () => mutation(), useRestoreTaskMutation: () => mutation() }));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => mutation() }));
vi.mock("@/lib/store/hooks", () => ({ useAppDispatch: () => vi.fn() }));

const qa = {
	id: "support-faq",
	grader: "answer_match",
	output: "answer",
	task_count: 2,
	admitted_count: 1,
	tasks: [
		{ id: "refund-window", title: "Refund window", admitted: true },
		{ id: "sso-plan", split: "held_out", redacted: true, admitted: false },
	],
} as unknown as SuiteSummary;

const code = { ...qa, id: "repo", grader: "hidden_tests", output: undefined, tasks: [] } as unknown as SuiteSummary;

describe("BuildTab", () => {
	it("walks a member through grader, rows, private tasks and readiness for a Q&A benchmark", () => {
		const html = render(<BuildTab suite={qa} />);
		expect(html).toContain('data-testid="suite-build-grader"');
		expect(html).toMatch(/data-testid="suite-build-grader-exact"[\s\S]*?checked=""/);
		expect(html).toContain('data-testid="suite-build-source-rows"');
		expect(html).toContain('data-testid="suite-build-source-tickets"');
		expect(html).toContain('data-testid="suite-build-source-traces"');
		expect(html).not.toContain('data-testid="suite-build-source-commit"');
		expect(html).toContain('data-testid="suite-build-rows-data"');
		expect(html).toContain('data-testid="suite-build-split-toggle-sso-plan"');
		expect(html).toContain("Make public");
		expect(html).toContain("1 of 2 tasks held out.");
		expect(html).toContain('data-testid="job-run-suite.admit"');
		expect(html).toContain("1 of 2 tasks ready.");
		expect(html).toContain('data-testid="suite-build-verbatim"');
		expect(html).toContain('data-testid="suite-build-attachments-file"');
		expect(html.replace(/<[^>]+>/g, " ")).not.toMatch(/oracle|noop|probe|canary|sealed|suite |\u2014|\u2013/i);
	});
	it("keeps the tests grader of a code benchmark and offers commits", () => {
		const html = render(<BuildTab suite={code} />);
		expect(html).toMatch(/data-testid="suite-build-grader-tests"[\s\S]*?checked=""/);
		expect(html).not.toContain('data-testid="suite-build-grader-save"');
		expect(html).toContain('data-testid="suite-build-no-rows"');
		expect(html).toContain('data-testid="suite-ops-from-commit"');
	});
	it("asks a hosted member to agree to the judge calls of a judged benchmark's readiness checks", () => {
		const judged = {
			...qa,
			grader: "judge",
			judge: { provider: "openai", model: "gpt-x", votes: 1, readiness_calls: 12 },
		} as unknown as SuiteSummary;
		const html = render(<BuildTab suite={judged} />, { ...LOCAL_SESSION, authMode: "hosted", role: "member" });
		expect(html).toContain('data-testid="suite-build-readiness-judge-consent"');
		expect(html).toContain("openai/gpt-x, about 12 calls");
		expect(html).toMatch(/<button[^>]*disabled=""[^>]*data-testid="job-run-suite.admit"|data-testid="job-run-suite.admit"[^>]*disabled=""/);
		const plain = render(<BuildTab suite={qa} />, { ...LOCAL_SESSION, authMode: "hosted", role: "member" });
		expect(plain).not.toContain("suite-build-readiness-judge-consent");
	});
	it("shows a viewer the state but no actions", () => {
		const html = render(<BuildTab suite={qa} />, { ...LOCAL_SESSION, authMode: "hosted", role: "viewer" });
		expect(html).toContain('data-testid="suite-build-viewer"');
		expect(html).not.toContain("suite-build-split-toggle");
		expect(html).not.toContain('data-testid="suite-build-rows-data"');
	});
});

describe("buildTab helpers", () => {
	it("names graders and sources", () => {
		expect(graderChoice({ grader: "judge", output: "answer" })).toBe("judge");
		expect(graderChoice({ grader: "answer_match", output: "answer" })).toBe("exact");
		expect(graderChoice({ grader: "verifier_command", output: "state" })).toBe("state");
		expect(graderChoice({ grader: "hidden_tests", output: undefined })).toBe("tests");
		expect(sourcesFor({ output: "answer" })).toEqual(["rows", "tickets", "traces"]);
		expect(sourcesFor({ output: "state" })).toEqual(["mock", "traces"]);
		expect(sourcesFor({ output: "state", track: "support" })).toEqual(["scenarios", "mock", "traces"]);
		expect(sourcesFor({ output: "diff" })).toEqual(["commit", "issues", "traces"]);
		expect(formatOfFile("faq.CSV")).toBe("csv");
		expect(formatOfFile("rows.jsonl")).toBe("jsonl");
		expect(formatOfFile("notes.txt")).toBeNull();
	});
	it("counts the judge calls of the readiness checks", () => {
		const s = {
			judge: { provider: "p", model: "m", votes: 1, readiness_calls: 10 },
			tasks: [{ id: "a" }, { id: "b" }],
		} as unknown as SuiteSummary;
		expect(judgeCalls(s, "")).toBe(10);
		expect(judgeCalls(s, "a")).toBe(5);
		expect(judgeCalls({ tasks: [] } as unknown as SuiteSummary, "")).toBe(0);
	});
	it("previews rows and problems", () => {
		const t = i18n.getFixedT("en", "suiteBuild");
		const html = render(
			<RowPreview
				t={t as never}
				result={{
					suite: "s",
					grader: "judge",
					dry_run: true,
					next: "",
					tasks: [
						{
							line: 2,
							id: "charged-twice",
							title: "Charged twice",
							question: "Billed twice",
							check: "Apologise",
							held_out: true,
							wrong_answers: ["We will look into it."],
							generated_wrong: true,
						},
					],
					problems: [{ line: 3, message: "no check" }],
				}}
			/>,
		);
		expect(html).toContain("1 rows can become tasks, 1 cannot.");
		expect(html).toContain("Rubric");
		expect(html).toContain("Row 3: no check");
		expect(html).toContain("held out");
		expect(html).toContain("We will look into it.");
		expect(html).toContain("Written by Heimdall from your answer");
	});
	it("shows turns, attachments, metadata and the first 500 of a large dry run", () => {
		const t = i18n.getFixedT("en", "suiteBuild");
		const html = render(
			<RowPreview
				t={t as never}
				result={{
					suite: "s",
					grader: "judge",
					dry_run: true,
					next: "",
					tasks_total: 1200,
					tasks: [
						{ line: 2, id: "chat-1", title: "", question: "Hi", check: "Be kind", turns: 3, attachments: 2, metadata: { region: "eu" } },
					],
					problems: [],
				}}
			/>,
		);
		expect(html).toContain("1200 rows can become tasks");
		expect(html).toContain("Showing the first 1 of 1200 tasks.");
		expect(html).toContain('data-testid="suite-build-turns-chat-1"');
		expect(html).toContain("3 turns");
		expect(html).toContain("2 files");
		expect(html).toContain("region: eu");
	});
});

describe("row mapping step", () => {
	const t = i18n.getFixedT("en", "suiteBuild") as never;
	const columns = [
		{ path: "inputs.question", kind: "text" as const, example: "How long is the refund window?", filled: 10 },
		{ path: "messages", kind: "messages" as const, filled: 4 },
		{ path: "meta.region", kind: "text" as const, example: "eu", filled: 10 },
	];
	it("presets each field from the effective mapping and lists columns with kind and example", () => {
		const html = render(
			<MappingStep
				columns={columns}
				mapping={{ question: "inputs.question", messages: "-", metadata: ["meta.region"] }}
				onChange={() => {}}
				saveMapping
				onSaveMapping={() => {}}
				showGradeOn
				gradeOn="transcript"
				onGradeOn={() => {}}
				t={t}
			/>,
		);
		expect(html).toContain("Map columns");
		expect(html).toMatch(
			/data-testid="suite-build-map-question"[\s\S]*?<option value="inputs.question" selected="">inputs.question \(text\): How long is the refund window\?<\/option>/,
		);
		expect(html).toMatch(/data-testid="suite-build-map-messages"[\s\S]*?<option value="-" selected="">Do not read/);
		expect(html).toContain("Detect automatically");
		expect(html).toMatch(/<input type="checkbox" checked=""\/>meta.region/);
		expect(html).toContain('data-testid="suite-build-grade-on"');
		expect(html).toMatch(/data-testid="suite-build-grade-on-transcript"/);
		expect(html).toMatch(
			/<input[^>]*data-testid="suite-build-map-save"[^>]*checked=""|<input[^>]*checked=""[^>]*data-testid="suite-build-map-save"/,
		);
		for (const f of ["expected", "rubric", "reference", "wrong_answers", "user_turns", "attachments", "id", "title", "split"])
			expect(html).toContain(`data-testid="suite-build-map-${f}"`);
	});
	it("hides the grade-on choice without conversations", () => {
		const html = render(
			<MappingStep
				columns={columns}
				mapping={{}}
				onChange={() => {}}
				saveMapping={false}
				onSaveMapping={() => {}}
				showGradeOn={false}
				onGradeOn={() => {}}
				t={t}
			/>,
		);
		expect(html).not.toContain("suite-build-grade-on");
		expect(hasConversations({ tasks: [{ line: 1, id: "a", title: "", question: "", check: "", turns: 1 }] })).toBe(false);
		expect(hasConversations({ tasks: [{ line: 1, id: "a", title: "", question: "", check: "", turns: 2 }] })).toBe(true);
		expect(hasConversations({ tasks: [{ line: 1, id: "a", title: "", question: "", check: "", grade_on: "final" }] })).toBe(true);
	});
	it("builds the request mapping, attachments and size limits", () => {
		expect(mappingBody({ question: "q", expected: "", metadata: [] })).toEqual({ question: "q" });
		expect(mappingBody({ question: "", metadata: ["a.b"] })).toEqual({ metadata: ["a.b"] });
		expect(mappingBody({})).toBeUndefined();
		expect(attachmentsBody([{ name: "a.png", size: 3, media_type: "image/png", content_base64: "AAA" }])).toEqual({
			"a.png": { content_base64: "AAA", media_type: "image/png" },
		});
		expect(attachmentsBody([])).toBeUndefined();
		const MiB = 1024 * 1024;
		expect(attachmentProblem([{ name: "big.pdf", size: 6 * MiB }])).toEqual({ key: "attach.tooBig", name: "big.pdf" });
		expect(attachmentProblem(Array.from({ length: 9 }, (_, i) => ({ name: `f${i}`, size: 5 * MiB })))).toEqual({ key: "attach.tooMuch" });
		expect(attachmentProblem([{ name: "ok", size: MiB }])).toBeNull();
		expect(dataMode(MiB)).toBe("sync");
		expect(dataMode(9 * MiB)).toBe("async");
		expect(dataMode(49 * MiB)).toBe("too_big");
		expect(columnLabel({ path: "x", kind: "object", filled: 1 }, t)).toBe("x (record)");
	});
});

describe("background import progress", () => {
	const t = i18n.getFixedT("en", "suiteBuild") as never;
	const base = {
		id: "imp-1",
		suite: "s",
		source: "rows.jsonl",
		rows: 2000,
		total: 2000,
		problem_count: 0,
		problems: [],
		started_at: "2026-11-22T10:00:00Z",
	};
	it("shows the bar, status and count while running", () => {
		const html = render(<ImportProgress status={{ ...base, status: "running", written: 500 }} t={t} />);
		expect(html).toContain("Importing tasks");
		expect(html).toContain("Running");
		expect(html).toContain("500 of 2000 tasks written");
		expect(html).toContain('aria-valuenow="500"');
		expect(html).toContain("width:25%");
	});
	it("shows problems, error and next when finished", () => {
		const html = render(
			<ImportProgress
				status={{
					...base,
					status: "failed",
					written: 10,
					problem_count: 3,
					problems: [{ line: 4, message: "no question" }],
					error: "disk full",
					next: "Fix the rows and import again.",
				}}
				t={t}
			/>,
		);
		expect(html).toContain("Failed");
		expect(html).toContain("disk full");
		expect(html).toContain("3 rows could not be imported");
		expect(html).toContain("Row 4: no question");
		expect(html).toContain("Fix the rows and import again.");
	});
});

describe("tool-use benchmarks and private tasks (N118, N119)", () => {
	it("names the grader of a tool-use benchmark Tool calls", () => {
		expect(graderChoice({ track: "tool_use", grader: "verifier_command", output: undefined })).toBe("toolCalls");
		expect(graderChoice({ track: "tool_use", grader: "verifier_command", output: "state" })).toBe("state");
		const mcp = { ...code, id: "zz-test-mcp", track: "tool_use", grader: "verifier_command" } as unknown as SuiteSummary;
		const html = render(<BuildTab suite={mcp} />);
		expect(html).toMatch(/data-testid="suite-build-grader-toolCalls"[\s\S]*?checked=""[\s\S]*?Tool calls[\s\S]*?In use/);
		expect(html).not.toContain("Hidden tests run on the agent");
		expect(html).toContain("checks the agent&#x27;s tool calls");
	});
	it("shows Saving... on a task row while its split is saved", () => {
		mockSplit.state = { isLoading: true };
		try {
			const html = render(<BuildTab suite={qa} />);
			expect(html).toMatch(/data-testid="suite-build-split-toggle-sso-plan"[^>]*>[\s\S]*?Saving\.\.\./);
		} finally {
			mockSplit.state = {};
		}
	});
});