import "@/lib/i18n";

import { RECEIPT_STEPS, type ReceiptStep } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ReceiptFunnel } from "./receiptFunnel";
import type { ArmRatios } from "./receipts.utils";

// The stale-notes row of experiments.diagnostics.json: a fixture-layer probe.
const FIXTURE_RATIOS: ArmRatios = {
	A: {
		assigned: { num: 12, den: 12, unknown: 0 },
		armed: { num: 12, den: 12, unknown: 0 },
		visible: { num: 12, den: 12, unknown: 0 },
		acknowledged: { num: 2, den: 12, unknown: 1 },
		effect: { num: 4, den: 12, unknown: 0 },
	},
	B: {
		assigned: { num: 12, den: 12, unknown: 0 },
		armed: { num: 12, den: 12, unknown: 0 },
		visible: { num: 12, den: 12, unknown: 0 },
		acknowledged: { num: 9, den: 12, unknown: 0 },
		effect: { num: 9, den: 12, unknown: 0 },
	},
};

const stepState = (html: string, step: ReceiptStep) =>
	html.match(new RegExp(`data-testid="receipt-step-${step}" data-state="([a-z_]+)"`))?.[1];

describe("ReceiptFunnel", () => {
	it("greys the steps the fixture layer does not apply, and shows them, never blank", () => {
		const html = renderToStaticMarkup(<ReceiptFunnel layer="fixture" arms={["A", "B"]} ratios={FIXTURE_RATIOS} />);
		for (const step of ["fired", "delivered", "absorbed_by"] as const) {
			expect(stepState(html, step), step).toBe("not_applicable");
			expect(html).toContain(`data-testid="receipt-step-${step}-A" data-state="not_applicable"`);
			expect(html).toContain(`data-testid="receipt-step-${step}-B" data-state="not_applicable"`);
		}
		expect(html).toContain('aria-label="Not applicable to the fixture layer"');
		expect(html.match(/>does not apply</g)).toHaveLength(6);
		for (const step of ["assigned", "armed", "visible", "acknowledged", "effect"] as const) {
			expect(stepState(html, step), step).toBe("applicable");
		}
	});

	it("lists all eight steps R0 to R7 in order", () => {
		const html = renderToStaticMarkup(<ReceiptFunnel layer="fixture" arms={["A", "B"]} ratios={FIXTURE_RATIOS} />);
		const positions = RECEIPT_STEPS.map((step) => html.indexOf(`data-testid="receipt-step-${step}"`));
		expect(positions.every((p) => p >= 0)).toBe(true);
		expect([...positions].sort((a, b) => a - b)).toEqual(positions);
		expect(html).toContain(">R0<");
		expect(html).toContain(">R7<");
	});

	it("shows counts per arm with unknowns kept apart", () => {
		const html = renderToStaticMarkup(<ReceiptFunnel layer="fixture" arms={["A", "B"]} ratios={FIXTURE_RATIOS} />);
		expect(html).toContain(">2 of 12<");
		expect(html).toContain(">9 of 12<");
		expect(html).toContain(">1 unknown<");
	});

	it("applies every step on the gateway layer, with no data where an arm has none", () => {
		const html = renderToStaticMarkup(
			<ReceiptFunnel layer="gateway" arms={["A"]} ratios={{ A: { fired: { num: 3, den: 3, unknown: 0 } } }} />,
		);
		for (const step of RECEIPT_STEPS) expect(stepState(html, step), step).toBe("applicable");
		expect(html).toContain('data-testid="receipt-step-delivered-A" data-state="no_data"');
	});

	it("without a layer, treats steps no arm recorded as not applicable", () => {
		const html = renderToStaticMarkup(<ReceiptFunnel layer={null} arms={["A", "B"]} ratios={FIXTURE_RATIOS} />);
		expect(stepState(html, "fired")).toBe("not_applicable");
		expect(stepState(html, "visible")).toBe("applicable");
		expect(html).toContain("No setup recorded this step");
	});
});