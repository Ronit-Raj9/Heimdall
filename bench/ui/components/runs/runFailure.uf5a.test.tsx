import "@/lib/i18n";

import type { Grade, RunDetail, ToolCall } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ClaimVsResult, jsonAnswer } from "./claimVsResult";
import { failedToolCall, runReason } from "./runFailure.utils";
import { RunFailureCard } from "./runFailureCard";

const grade = (over: Partial<Grade> = {}): Grade => ({
	passed: false,
	invalid_evaluator: false,
	exit_code: 0,
	tests_ran: 0,
	failures: 0,
	errors: 0,
	skipped: 0,
	suite_version: "1",
	grader_version: "1",
	diff_sha256: "d",
	...over,
});

const call = (over: Partial<ToolCall>): ToolCall =>
	({ seq: 1, server: "agentdojo", tool: "GET /x", protocol: "http", args_sha256: "", result_sha256: "", is_error: false, started: "", latency_ms: 1, ...over }) as ToolCall;

const detail = (over: Partial<RunDetail> = {}, outcome = "fail") =>
	({ sealed: true, run: { run_id: "r_1", outcome }, grade: grade(), tool_calls: [], ...over }) as unknown as RunDetail;

describe("runReason (M5-1)", () => {
	it("names the tool server 5xx before anything else", () => {
		const d = detail({ tool_calls: [call({ seq: 3, is_error: true, status: 405 }), call({ seq: 7, tool: "DELETE /mcp", is_error: true, status: 502 })] });
		expect(failedToolCall(d.tool_calls)?.seq).toBe(7);
		expect(runReason(d)).toEqual({ kind: "tool", params: { server: "agentdojo", status: 502, tool: "DELETE /mcp", seq: 7 } });
	});

	it("falls back through grader, mode, checks and no checks; nothing for a pass", () => {
		expect(runReason(detail({ grade: grade({ invalid_evaluator: true, reason: "x" }) }))?.kind).toBe("invalidEvaluator");
		expect(runReason(detail({ grade: grade({ mode: "snapshot" }) }))).toEqual({ kind: "mode", params: { what: "files" } });
		expect(runReason(detail({ grade: grade({ tests_ran: 5, failures: 2 }) }))).toEqual({ kind: "checks", params: { failures: 2, ran: 5 } });
		expect(runReason(detail())?.kind).toBe("noChecks");
		expect(runReason(detail({}, "pass"))).toBeUndefined();
		expect(runReason(detail({ sealed: false }))).toBeUndefined();
	});

	it("the card says why in one sentence, in the warning tone for a tool error", () => {
		const d = detail({ tool_calls: [call({ seq: 7, tool: "DELETE /mcp", is_error: true, status: 502 })] });
		const html = renderToStaticMarkup(<RunFailureCard run={d.run} detail={d} />);
		expect(html).toContain("The tool server agentdojo answered 502 to DELETE /mcp at step 7. This is likely infrastructure, not the agent.");
		expect(html).toContain("border-chart-warning");
		expect(html).not.toContain("hidden checks ran");
	});
});

describe("claim vs result (M5-1, N5-1, X-39)", () => {
	const run = { run_id: "r_1", outcome: "fail" } as unknown as RunDetail["run"];
	it("never prints 0 hidden checks ran, says the mode compare and the grader reason", () => {
		const html = renderToStaticMarkup(
			<ClaimVsResult rows={[{ arm: "A", run, grade: grade({ mode: "answer", reason: "answer differs" }), gradeState: "ready" }]} />,
		);
		expect(html).not.toContain("hidden checks ran");
		expect(html).toContain("Compared the final answer with the expected one: did not match");
		expect(html).toContain("Grader&#x27;s reason: answer differs");
	});

	it("pretty prints a JSON answer and leaves text alone", () => {
		expect(jsonAnswer('{"a":1}')).toBe('{\n  "a": 1\n}');
		expect(jsonAnswer("all done")).toBeNull();
		expect(jsonAnswer("{not json")).toBeNull();
	});
});
