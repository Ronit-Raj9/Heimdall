import "@/lib/i18n";

import type { ReferenceBoard, ReferenceEntry } from "@/lib/store/apis/referenceApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ReferenceBoardView } from "./referenceBoard";
import { agentName, costPerSolved, entriesByBenchmark, groupByBenchmark, referenceHint, shortHash } from "./reference.utils";

function entry(over: Partial<ReferenceEntry>): ReferenceEntry {
	return {
		id: "ev_a:hd-swe-mini:A",
		evaluation_id: "ev_a",
		experiment_id: "e_a",
		benchmark: "hd-swe-mini",
		capability: "coding",
		agent: "codex",
		model: "gpt-5.4",
		score: 0.42,
		ci_low: 0.3,
		ci_high: 0.55,
		runs: 20,
		tasks: 10,
		solved: 8,
		cost_usd: 4,
		cost_per_solved_usd: 0.5,
		wall_s: 100,
		ran_at: "2026-11-20T10:00:00Z",
		seal_hash: "sha256:0123456789abcdef0123",
		sealed_runs: 20,
		report_path: "/s/org_ref.shr_1.sig",
		published_at: "2026-11-21T10:00:00Z",
		...over,
	};
}

const board = (entries: ReferenceEntry[]): ReferenceBoard => ({ org: "Heimdall reference", benchmark: "", benchmarks: [], entries });

describe("reference board", () => {
	it("says plainly when nothing is published, with no numbers", () => {
		const html = renderToStaticMarkup(<ReferenceBoardView board={board([])} />);
		expect(html).toContain('data-testid="reference-empty"');
		expect(html).toContain("No reference scores yet.");
		expect(html).not.toMatch(/\d+%/);
		const one = renderToStaticMarkup(<ReferenceBoardView board={board([])} benchmark="tau2" />);
		expect(one).toContain("for this benchmark");
	});

	it("shows score, range, cost per solved task, runs, fingerprint and the report card link", () => {
		const html = renderToStaticMarkup(
			<ReferenceBoardView
				board={board([entry({}), entry({ id: "ev_b:tau2:A", benchmark: "tau2", cost_usd: 1, cost_per_solved_usd: null, solved: 0, score: 0, report_path: undefined })])}
			/>,
		);
		expect(html).toContain("42%");
		expect(html).toContain("30% to 55%");
		expect(html).toContain("$0.50");
		expect(html).toContain("0123456789ab");
		expect(html).toContain('href="/s/org_ref.shr_1.sig"');
		expect(html).toContain("None solved");
		expect(html).toContain('data-testid="reference-benchmark-tau2"');
		expect(html).not.toMatch(/sealed|suite|runner/i);
	});

	it("names the cost unknown instead of $0", () => {
		const html = renderToStaticMarkup(<ReferenceBoardView board={board([entry({ cost_usd: null, cost_per_solved_usd: null })])} />);
		expect(html).toContain("Not known");
		expect(html).not.toContain("$0");
	});
});

describe("reference utils", () => {
	it("groups and keys benchmarks without the version pin", () => {
		const es = [entry({}), entry({ id: "x", benchmark: "HD-SWE-MINI@1.0.0", score: 0.6 }), entry({ id: "y", benchmark: "tau2" })];
		expect(groupByBenchmark(es).map((g) => g.entries.length)).toEqual([2, 1]);
		expect(entriesByBenchmark(es).get("hd-swe-mini")?.length).toBe(2);
	});

	it("hints only from published entries", () => {
		expect(referenceHint(undefined)).toBeNull();
		expect(referenceHint([])).toBeNull();
		expect(referenceHint([entry({})])).toMatchObject({ kind: "one", agent: "codex", score: "42%" });
		expect(referenceHint([entry({}), entry({ agent: "claude-code", score: 0.61 })])).toMatchObject({ kind: "many", agent: "claude-code", low: "42%", high: "61%" });
	});

	it("formats names, costs and hashes", () => {
		expect(agentName({ agent: "codex", version: 2, agent_build: "0.50.0" })).toBe("codex v2 (0.50.0)");
		expect(agentName({ agent: "codex" })).toBe("codex");
		expect(costPerSolved(0.0123)).toBe("$0.0123");
		expect(costPerSolved(1.5)).toBe("$1.50");
		expect(shortHash("sha256:abcdef0123456789")).toBe("abcdef012345");
	});
});
