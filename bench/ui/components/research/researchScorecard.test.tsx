import "@/lib/i18n";

import { experimentResearchPath } from "@/lib/store/apis/researchApi";
import type { ResearchArm, ResearchSummary } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { formatMean, formatRate, ResearchScorecard } from "./researchScorecard";

// Shaped after the $0 smoke of examples/acme-research (docs/research-tasks.md): arm A runs each
// task's reference script through the websearch tool, arm B does nothing.
const summary: ResearchSummary = {
	exp_id: "e_research_smoke",
	suite: "acme-research@1.0.0",
	condition: "clean",
	research: true,
	replay: true,
	synthetic: true,
	arms: [
		{
			arm: "A",
			label: "oracle (reference script)",
			runs: 8,
			verified: 1,
			passed: 8,
			decided: 8,
			searches: 1.5,
			sources_cited: 1.4,
			citations_grounded: 1,
			citation_precision: 0.94,
			external_unmatched: 0,
		},
		{
			arm: "B",
			label: "noop",
			runs: 8,
			verified: 0,
			passed: 0,
			decided: 8,
			searches: 0,
			sources_cited: 0,
			citations_grounded: null,
			citation_precision: null,
			external_unmatched: 0,
		},
	],
};

describe("ResearchScorecard", () => {
	it("shows the verified share, grounding, precision and searches per arm", () => {
		const html = renderToStaticMarkup(<ResearchScorecard summary={summary} />);
		expect(html).toContain('data-testid="research-scorecard"');
		expect(html).toContain("Citations grounded");
		expect(html).toContain("Citation precision");
		expect(html).toContain("8 of 8 answers verified");
		expect(html).toContain("replayed searches");
		expect(html).toContain("smoke test, not findings");
		expect(html).toContain('data-testid="research-A-grounded"');
		expect(html).toContain('data-testid="research-B-precision"');
		expect(html).not.toContain('data-testid="research-A-unmatched"');
	});

	it("warns about live search results and unrecorded queries", () => {
		const live: ResearchSummary = { ...summary, replay: false, synthetic: false, arms: [{ ...summary.arms[0], external_unmatched: 2 }] };
		const html = renderToStaticMarkup(<ResearchScorecard summary={live} />);
		expect(html).toContain("live search results, not repeatable");
		expect(html).toContain('data-testid="research-A-unmatched"');
		expect(html).not.toContain("smoke test");
	});

	it("formats unknowns as unknown", () => {
		expect(formatRate(null)).toBe("unknown");
		expect(formatRate(0.94)).toBe("94%");
		expect(formatMean(null)).toBe("unknown");
		expect(formatMean(1.46)).toBe("1.5");
	});

	it("escapes the experiment id in the request path", () => {
		expect(experimentResearchPath("e/x")).toBe("/experiments/e%2Fx/research");
	});
});

// The Go side's key lists (bench/handlers/testdata/contract/<Type>.keys): the fixture above carries
// exactly the listed keys, required and optional.
const CONTRACT_DIR = path.resolve(__dirname, "../../../handlers/testdata/contract");

function keyList(typ: string): { required: string[]; optional: string[] } {
	const lines = fs
		.readFileSync(path.join(CONTRACT_DIR, `${typ}.keys`), "utf8")
		.split("\n")
		.map((l) => l.trim())
		.filter((l) => l && !l.startsWith("#"));
	return { required: lines.filter((l) => !l.endsWith("?")), optional: lines.filter((l) => l.endsWith("?")).map((l) => l.slice(0, -1)) };
}

function checkObject(obj: object, typ: string) {
	const { required, optional } = keyList(typ);
	const keys = Object.keys(obj);
	for (const k of required) expect(keys, `${typ} needs ${k}`).toContain(k);
	for (const k of keys) expect([...required, ...optional], `${typ} has no key ${k}`).toContain(k);
}

describe("research contract", () => {
	it("matches the Go key lists", () => {
		checkObject(summary, "ResearchSummary");
		summary.arms.forEach((a: ResearchArm) => checkObject(a, "ResearchArm"));
	});
});
