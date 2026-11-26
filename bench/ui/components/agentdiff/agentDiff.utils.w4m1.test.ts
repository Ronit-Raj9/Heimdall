import { describe, expect, it } from "vitest";
import type { AgentDiffVersionInfo } from "@/lib/types/bench";
import { suggestBaseline } from "./agentDiff.utils";

const v = (hash: string, at: string, suites: string[]): AgentDiffVersionInfo => ({
	config_hash: hash,
	agent_id: "bot",
	label: hash,
	suites,
	arms: [],
	sealed_runs: 1,
	latest_at: at,
});

describe("suggestBaseline (wave 4, p01-1)", () => {
	it("skips an older version that shares no benchmark and picks the next one that does", () => {
		const b = v("b", "2026-11-03T00:00:00Z", ["s@1"]);
		const noShare = v("x", "2026-11-02T00:00:00Z", ["other@1"]);
		const shares = v("a", "2026-11-01T00:00:00Z", ["s@1"]);
		expect(suggestBaseline([b, noShare, shares], "b")?.config_hash).toBe("a");
	});
	it("gives null when no older version shares a benchmark", () => {
		const b = v("b", "2026-11-03T00:00:00Z", ["s@1"]);
		const noShare = v("x", "2026-11-02T00:00:00Z", ["other@1"]);
		expect(suggestBaseline([b, noShare], "b")).toBeNull();
	});
});