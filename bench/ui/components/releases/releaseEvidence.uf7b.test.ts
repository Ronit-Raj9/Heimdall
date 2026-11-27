import i18n from "@/lib/i18n";
import type { ReleaseSummary } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { evidenceHeads } from "./releaseEvidence";

const side = (version: number, runs: number) => ({ label: "A", agent: "codex", version, score: 0.5, ci_low: 0.4, ci_high: 0.6, runs });

describe("decision table heads (M7-78)", () => {
	it("tells the two columns apart when both are the same version", () => {
		const t = i18n.getFixedT("en", "uf7b") as never;
		const same: ReleaseSummary = { candidate: side(1, 6), baseline: side(1, 4) };
		expect(evidenceHeads(same, t)).toEqual({ before: "Earlier run (4 runs)", after: "This evaluation (6 runs)" });
		const diff: ReleaseSummary = { candidate: side(2, 6), baseline: side(1, 4) };
		expect(evidenceHeads(diff, t).before).not.toBe(evidenceHeads(diff, t).after);
	});
});
