import { describe, expect, it } from "vitest";
import { datasetDeleteRequest, datasetTasksRequest, datasetVersionPath, datasetWriteRequest, harvestRequest, issueCancelRequest, mcpServerReportPath } from "@/lib/store/apis/ufl5Api";
import { redTeamEvaluations } from "@/components/redteam/harvestForm";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import type { IssueRun } from "@/lib/types/issuebench";
import type { McpServerReport } from "@/lib/types/ufl5";
import { canCancel, newestRuns } from "@/components/issuebench/pastIssueRuns";
import { rowProblems, rowRequest } from "./datasetRows";
import { pct, sortedTools } from "./mcpServerReport";

// The same strings as handlers/datasetapi_ufl5_test.go, which posts them to the handler.
const WEB_ADD = `{"tasks":[{"input":"What is 2+2?","id":"q-sum","expected":["4","four"]}],"mode":"add","source":"web"}`;
const WEB_REPLACE = `{"tasks":[{"input":"What is 3+3?","id":"q-sum","expected":["6"]}],"mode":"upsert","source":"web"}`;

describe("dataset rows requests (round 5, L5)", () => {
	it("sends the body the Go handler is tested with", () => {
		expect(JSON.stringify(datasetWriteRequest("qa", rowRequest({ id: " q-sum ", input: " What is 2+2? ", expected: "4\n\n four " }, false)).body)).toBe(WEB_ADD);
		expect(JSON.stringify(rowRequest({ id: "q-sum", input: "What is 3+3?", expected: "6" }, true))).toBe(WEB_REPLACE);
	});
	it("leaves out an empty id and empty answers", () => {
		expect(rowRequest({ id: "", input: "Q", expected: " " }, false)).toEqual({ tasks: [{ input: "Q" }], mode: "add", source: "web" });
	});
	it("uses the routes of handlers/datasetapi.go", () => {
		expect(datasetTasksRequest("my qa", 50, 50)).toEqual({ url: "/suites/my%20qa/dataset/tasks", params: { offset: 50, limit: 50 } });
		expect(datasetWriteRequest("qa", { tasks: [] })).toMatchObject({ url: "/suites/qa/dataset/tasks", method: "POST" });
		expect(datasetDeleteRequest("qa", "a/b")).toEqual({ url: "/suites/qa/dataset/tasks/a%2Fb", method: "DELETE" });
		expect(datasetVersionPath("qa", 3)).toBe("/suites/qa/dataset/versions/3");
	});
	it("turns the server's problems into one line", () => {
		const r = { benchmark: "qa", version: null, added: null, updated: null, unchanged: null, problems: [{ index: 1, message: "id q-sum is taken" }] };
		expect(rowProblems(r)).toBe("id q-sum is taken");
		expect(rowProblems({ ...r, problems: [] })).toBeNull();
	});
});

describe("MCP server report (round 5, L5)", () => {
	it("reads the route of handlers/mcptest.go", () => {
		expect(mcpServerReportPath("acme-mcp")).toBe("/mcp-servers/benchmarks/acme-mcp/report");
	});
	it("orders tools by calls and shows no share without counts", () => {
		const base = { runs: 0, picked: 0, calls: 0, valid: 0, succeeded: 0, passed: 0, pick_rate: 0, valid_rate: 0, success_rate: 0, p50_ms: 0, p95_ms: 0, by_agent: null, advice: null };
		const rep: McpServerReport = {
			benchmark: "b",
			server: "s",
			runs: 2,
			agents: ["codex"],
			calls: 5,
			errors: 0,
			unlisted_tools: null,
			tools: [
				{ ...base, tool: "b", calls: 1 },
				{ ...base, tool: "a", calls: 4 },
			],
		};
		expect(sortedTools(rep).map((x) => x.tool)).toEqual(["a", "b"]);
		expect(pct(0.5, 0)).toBe("-");
		expect(pct(0.666, 3)).toBe("67%");
	});
});

describe("past issue scans (round 5, L5)", () => {
	it("posts the cancel of handlers/issuebench.go with no body", () => {
		expect(issueCancelRequest("ib 1")).toEqual({ url: "/issuebench/runs/ib%201/cancel", method: "POST" });
	});
	it("lists newest first and stops only a running build", () => {
		const r = (id: string, created: string, status: IssueRun["status"]) => ({ id, created, status }) as IssueRun;
		expect(newestRuns([r("a", "2026-01-01", "done"), r("b", "2026-02-01", "running")]).map((x) => x.id)).toEqual(["b", "a"]);
		expect(canCancel({ status: "running" })).toBe(true);
		expect(canCancel({ status: "scanned" })).toBe(false);
	});
});

describe("red-team harvest (round 5, L5)", () => {
	it("posts handlers.RedTeamHarvestRequest, which is decoded strictly", () => {
		expect(harvestRequest("ev-1")).toEqual({ url: "/redteam/regressions/harvest", method: "POST", body: { evaluation: "ev-1" } });
	});
	it("offers finished red-team evaluations only, newest first", () => {
		const e = (id: string, status: string, benchmarks: string[], created_at: string) => ({ id, status, spec: { benchmarks }, created_at }) as unknown as Evaluation;
		const got = redTeamEvaluations([e("a", "done", ["red-team"], "1"), e("b", "running", ["red-team"], "2"), e("c", "done", ["swe"], "3"), e("d", "done", ["red-team-x"], "4")]);
		expect(got.map((x) => x.id)).toEqual(["d", "a"]);
	});
});
