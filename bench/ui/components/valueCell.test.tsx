import "@/lib/i18n";

import type { Value, ValueKind } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ValueCell } from "./valueCell";

const KINDS: { kind: ValueKind; v: number; text: string; bounds: [number, number]; boundsText: [string, string] }[] = [
	{ kind: "rate", v: 0.333, text: "33.3%", bounds: [0.02, 0.11], boundsText: ["2%", "11%"] },
	{ kind: "count", v: 14, text: "14", bounds: [12, 20], boundsText: ["12", "20"] },
	{ kind: "tokens", v: 40112, text: "40.1K", bounds: [812, 52000], boundsText: ["812", "52K"] },
	{ kind: "usd", v: 0.0031, text: "$0.0031", bounds: [1.5, 2.25], boundsText: ["$1.50", "$2.25"] },
	{ kind: "duration", v: 14.25, text: "14.3s", bounds: [30, 150], boundsText: ["30s", "2.5m"] },
];

function render(value: Value | null | undefined, kind: ValueKind): string {
	return renderToStaticMarkup(<ValueCell value={value} kind={kind} />);
}

describe("ValueCell", () => {
	for (const { kind, v, text, bounds, boundsText } of KINDS) {
		describe(kind, () => {
			it("renders an observed value formatted for its kind", () => {
				const html = render({ state: "observed", v, evidence: "gateway" }, kind);
				expect(html).toContain('data-state="observed"');
				expect(html).toContain(`data-kind="${kind}"`);
				expect(html).toContain('data-evidence="gateway"');
				expect(html).toContain(`>${text}<`);
			});

			it("renders unknown hatched with reason and bounds, never as zero", () => {
				const html = render({ state: "unknown", v: null, evidence: "gateway", bounds, reason: "telemetry_incomplete" }, kind);
				expect(html).toContain('data-state="unknown"');
				expect(html).toContain("bench-hatched");
				expect(html).toContain('data-testid="value-cell-unknown"');
				expect(html).toContain(`aria-label="Unknown: telemetry_incomplete. Between ${boundsText[0]} and ${boundsText[1]}."`);
				expect(html).toContain(">unknown<");
				expect(html).not.toMatch(/>\$?0(\.0+)?%?s?</);
			});

			it("renders unknown without bounds with only the reason", () => {
				const html = render({ state: "unknown", v: null, reason: "telemetry_incomplete" }, kind);
				expect(html).toContain('aria-label="Unknown: telemetry_incomplete."');
			});

			it("renders not_applicable as n/a", () => {
				const html = render({ state: "not_applicable", v: null }, kind);
				expect(html).toContain('data-state="not_applicable"');
				expect(html).toContain('aria-label="Not applicable"');
				expect(html).toContain(">n/a<");
			});
		});
	}

	it("renders a missing value as unknown, not zero", () => {
		const html = render(undefined, "count");
		expect(html).toContain('data-state="unknown"');
		expect(html).toContain("value missing from the response");
	});

	it("renders an observed value without a number as unknown", () => {
		const html = render({ state: "observed", v: null }, "rate");
		expect(html).toContain('data-state="unknown"');
		expect(html).toContain("no reason recorded");
	});

	it("makes an observed value's source reachable by keyboard and touch (QC2-29-08)", () => {
		const html = render({ state: "observed", v: 0.5, evidence: "gateway" }, "rate");
		expect(html).toMatch(/<span[^>]*data-state="observed"[^>]*tabindex="0"|<span[^>]*tabindex="0"[^>]*data-state="observed"/);
		expect(html).toContain("cursor-help");
		const bare = render({ state: "observed", v: 0.5 } as Value, "rate");
		expect(bare).not.toContain("tabindex");
	});
	it("renders an observed zero as zero", () => {
		const html = render({ state: "observed", v: 0 }, "count");
		expect(html).toContain('data-state="observed"');
		expect(html).toContain(">0<");
	});

	it("uses the testId override", () => {
		const html = renderToStaticMarkup(<ValueCell value={{ state: "observed", v: 1 }} kind="count" testId="verdict-cell-steps" />);
		expect(html).toContain('data-testid="verdict-cell-steps"');
	});
});
// 6-B-07: an observed value's evidence level is readable, not only a data attribute.
describe("ValueCell provenance", () => {
	it("names the evidence level in the title and for screen readers", () => {
		const html = render({ state: "observed", v: 1.5, evidence: "gateway" }, "usd");
		expect(html).toContain('title="Evidence: gateway rows and the proxy ledger"');
		// Q-45: provenance is only in the title, never inline text after the number.
		expect(html).not.toContain("(Evidence:");
	});
	it("adds nothing when the value has no evidence level", () => {
		expect(render({ state: "observed", v: 1.5 } as Value, "usd")).not.toContain("sr-only");
	});
});
