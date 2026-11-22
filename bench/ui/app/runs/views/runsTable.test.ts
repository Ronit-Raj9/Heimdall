import { describe, expect, it } from "vitest";
import type { RunListRow } from "@/lib/types/bench";
import {
	agentOptions,
	agentVersionLabel,
	ariaSortOf,
	formatDuration,
	nextRunSort,
	orderRuns,
	runAgent,
	runDuration,
	runsRequest,
} from "./runsTable";

const row = (id: string, sealed: boolean, task = "t", arm = "a", config_id = ""): RunListRow =>
	({ run_id: id, sealed, task, arm, config_id, suite: "s@1", outcome: "pass" }) as RunListRow;

describe("runsTable utils", () => {
	it("puts running runs first and keeps order", () => {
		const out = orderRuns([row("1", true), row("2", false), row("3", true), row("4", false)], null);
		expect(out.map((r) => r.run_id)).toEqual(["2", "4", "1", "3"]);
	});
	it("searches task and agent", () => {
		const out = orderRuns([row("1", true, "fix-bug", "codex"), row("2", true, "other", "claude")], "CLAUDE");
		expect(out.map((r) => r.run_id)).toEqual(["2"]);
	});
	it("filters by agent name, never arm id", () => {
		const rows = [row("1", true, "t", "A", "codex"), row("2", true, "t", "B", "claude-code")];
		expect(orderRuns(rows, null, "claude-code").map((r) => r.run_id)).toEqual(["2"]);
		expect(runAgent(rows[0])).toBe("codex");
		expect(runAgent(row("3", true, "t", "A"))).toBe("A");
		expect(agentOptions(["opencode", "codex"], rows)).toEqual(["claude-code", "codex", "opencode"]);
		expect(agentOptions([], [row("4", true, "t", "A")])).toEqual([]);
	});
	it("builds the request from filters and page", () => {
		expect(runsRequest({ outcome: "pass", suite: null, arm: "x" }, 3)).toEqual({ limit: 50, outcome: "pass", arm: "x", offset: 100 });
	});
	it("sends the agent and fault filters to the server", () => {
		expect(runsRequest({ agent: "codex", fault: "model-down" }, 2)).toEqual({ limit: 50, agent: "codex", fault: "model-down", offset: 50 });
	});
	it("offers agents the server saw on other pages", () => {
		expect(agentOptions([], [], ["claude-code", "codex"])).toEqual(["claude-code", "codex"]);
	});
	it("labels the agent version", () => {
		expect(agentVersionLabel({ version: 2, agent_version: "1.4.0" })).toBe("v2");
		expect(agentVersionLabel({ agent_version: "1.4.0" })).toBe("1.4.0");
		expect(agentVersionLabel({})).toBe("");
	});
	it("formats durations", () => {
		expect(runDuration({ started: "2026-01-01T00:00:00Z", sealed_at: "2026-01-01T00:01:05Z" })).toBe(65);
		expect(formatDuration(65)).toBe("1m 5s");
		expect(formatDuration(null)).toBe("-");
	});
	it("a header click sets the sort in the URL state and the request", () => {
		// First click on Cost: largest first; second click flips it.
		const first = nextRunSort({}, "cost");
		expect(first).toEqual({ sort: "cost", dir: "desc" });
		const second = nextRunSort(first, "cost");
		expect(second).toEqual({ sort: "cost", dir: "asc" });
		// A text column starts A to Z.
		expect(nextRunSort(second, "task")).toEqual({ sort: "task", dir: "asc" });
		// Back to the default order (newest first) clears the URL.
		expect(nextRunSort({ sort: "started", dir: "asc" }, "started")).toEqual({ sort: null, dir: null });
		expect(runsRequest(second, 2)).toEqual({ limit: 50, sort: "cost", dir: "asc", offset: 50 });
		expect(runsRequest({ sort: "price", dir: "up" }, 1)).toEqual({ limit: 50 });
		expect(ariaSortOf(second, "cost")).toBe("ascending");
		expect(ariaSortOf(second, "task")).toBe("none");
		expect(ariaSortOf({}, "started")).toBe("descending");
	});
	it("keeps the server's order when a sort is chosen", () => {
		const out = orderRuns([row("1", true), row("2", false)], null, null, true);
		expect(out.map((r) => r.run_id)).toEqual(["1", "2"]);
	});
});