import "@/lib/i18n";

import { render, tagOf } from "@/lib/testing/render";
import type { Run } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { formatDiffValue, knownNumber, rowDiffers, RunDiff } from "./runDiff";

const run = (outcome: Run["outcome"], text?: string) =>
	({
		outcome,
		claim: text ? { made: true, text, source: "native" } : undefined,
	}) as unknown as Run;
const known = (v: number) => ({ state: "observed", v }) as never;

describe("RunDiff", () => {
	it("flags rows that differ and leaves equal rows plain", () => {
		const html = render(
			<RunDiff
				lanes={[
					{
						arm: "A",
						run: run("pass", "done"),
						metrics: {
							steps: known(4),
							tokens: known(1200),
							cost_usd: known(0.012),
						},
					},
					{
						arm: "B",
						run: run("fail", "gave up"),
						metrics: {
							steps: known(4),
							tokens: known(3400),
							cost_usd: known(0.05),
						},
					},
				]}
			/>,
		);
		expect(tagOf(html, "compare-diff-outcome")).toContain('data-differs="true"');
		expect(tagOf(html, "compare-diff-steps")).toContain('data-differs="false"');
		expect(tagOf(html, "compare-diff-tokens")).toContain('data-differs="true"');
		expect(html).toContain("3,400");
		expect(html).toContain("gave up");
	});
	it("formats values and treats unknown as missing", () => {
		expect(knownNumber({ state: "unknown", v: null } as never)).toBeNull();
		expect(formatDiffValue("cost", 0.0123)).toBe("$0.0123");
		expect(formatDiffValue("time", 75)).toBe("1m 15s");
		expect(formatDiffValue("tokens", null)).toBe("-");
		expect(rowDiffers([1, 1, 1])).toBe(false);
		expect(rowDiffers([1, null])).toBe(true);
	});
});
