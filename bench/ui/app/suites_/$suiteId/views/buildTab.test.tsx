import "@/lib/i18n";

import { LOCAL_SESSION } from "@/lib/session/sessionContext";
import { mutation, render } from "@/lib/testing/render";
import type { SuiteSummary } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { BuildTab, formatOfFile, graderChoice, judgeCalls, RowPreview, sourcesFor } from "./buildTab";
import i18n from "i18next";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/suiteBuildApi", () => ({
	useImportSuiteRowsMutation: () => mutation(),
	useSetSuiteGraderMutation: () => mutation(),
	useSetTaskSplitMutation: () => mutation(),
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
		expect(html.replace(/<[^>]+>/g, " ")).not.toMatch(/oracle|noop|probe|canary|sealed|suite /i);
	});
	it("keeps the tests grader of a code benchmark and offers commits", () => {
		const html = render(<BuildTab suite={code} />);
		expect(html).toMatch(/data-testid="suite-build-grader-tests"[\s\S]*?checked=""/);
		expect(html).not.toContain('data-testid="suite-build-grader-save"');
		expect(html).toContain('data-testid="suite-build-no-rows"');
		expect(html).toContain('data-testid="suite-ops-from-commit"');
	});
	it("asks a hosted member to agree to the judge calls of a judged benchmark's readiness checks", () => {
		const judged = { ...qa, grader: "judge", judge: { provider: "openai", model: "gpt-x", votes: 1, readiness_calls: 12 } } as unknown as SuiteSummary;
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
		const s = { judge: { provider: "p", model: "m", votes: 1, readiness_calls: 10 }, tasks: [{ id: "a" }, { id: "b" }] } as unknown as SuiteSummary;
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
					tasks: [{ line: 2, id: "charged-twice", title: "Charged twice", question: "Billed twice", check: "Apologise", held_out: true, wrong_answers: ["We will look into it."], generated_wrong: true }],
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
});
