import "@/lib/i18n";

import type { EvaluationSpec } from "@/lib/store/apis/evaluationsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { BareModelCard } from "./firstLookLauncher";

const spec = {
	name: "First look: bot vs the bare model",
	mode: "opponent",
	arms: [
		{ label: "A", agent: "bot", model: "gpt-x" },
		{ label: "B", agent: "bare-model", model: "gpt-x" },
	],
	benchmarks: ["fastqa"],
	tasks_per_benchmark: 5,
	trials: 1,
	faults: [],
} as unknown as EvaluationSpec;

describe("BareModelCard", () => {
	it("offers the bare-model comparison as a second card", () => {
		const html = renderToStaticMarkup(<BareModelCard spec={spec} onStart={() => {}} />);
		expect(html).toContain('data-testid="first-look-bare"');
		expect(html).toContain("Compare with the bare model");
		expect(html).toContain("5 tasks on fastqa");
		expect(html).toContain("gpt-x alone, no tools");
		expect(html).toContain('data-testid="first-look-bare-start"');
		expect(html.includes(String.fromCharCode(0x2014))).toBe(false);
	});
	it("says why when there is no comparison, and shows nothing without a reason", () => {
		const html = renderToStaticMarkup(<BareModelCard reason="this agent is the bare model" onStart={() => {}} />);
		expect(html).toContain("No bare-model comparison: this agent is the bare model");
		expect(html).not.toContain("first-look-bare-start");
		expect(renderToStaticMarkup(<BareModelCard onStart={() => {}} />)).toBe("");
	});
});
