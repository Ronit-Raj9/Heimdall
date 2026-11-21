import { describe, expect, it } from "vitest";
import type { RunListRow } from "@/lib/types/bench";
import { formatDuration, orderRuns, runDuration, runsRequest } from "./runsTable";

const row = (id: string, sealed: boolean, task = "t", arm = "a"): RunListRow =>
	({ run_id: id, sealed, task, arm, suite: "s@1", outcome: "pass" }) as RunListRow;

describe("runsTable utils", () => {
	it("puts running runs first and keeps order", () => {
		const out = orderRuns([row("1", true), row("2", false), row("3", true), row("4", false)], null);
		expect(out.map((r) => r.run_id)).toEqual(["2", "4", "1", "3"]);
	});
	it("searches task and agent", () => {
		const out = orderRuns([row("1", true, "fix-bug", "codex"), row("2", true, "other", "claude")], "CLAUDE");
		expect(out.map((r) => r.run_id)).toEqual(["2"]);
	});
	it("builds the request from chips and page", () => {
		expect(runsRequest({ outcome: "pass", suite: null, arm: "x" }, 3)).toEqual({ limit: 50, outcome: "pass", arm: "x", offset: 100 });
	});
	it("formats durations", () => {
		expect(runDuration({ started: "2026-01-01T00:00:00Z", sealed_at: "2026-01-01T00:01:05Z" })).toBe(65);
		expect(formatDuration(65)).toBe("1m 5s");
		expect(formatDuration(null)).toBe("-");
	});
});
