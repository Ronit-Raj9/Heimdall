import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { GateSettings, sizeHint } from "./gateSettings";

describe("GateSettings", () => {
	it("is off with no gate", () => {
		const html = render(<GateSettings onChange={() => undefined} />);
		expect(html).toContain('data-testid="gate-enable"');
		expect(html).not.toContain('data-testid="gate-max-regression"');
	});

	it("shows the regression limit in points, the baseline version and minimum scores", () => {
		const html = render(<GateSettings gate={{ max_regression: 0.05, baseline_version: 3, min_score: { terminal: 0.4 } }} onChange={() => undefined} />);
		expect(html).toMatch(/data-testid="gate-max-regression"[^>]*value="5"|value="5"[^>]*data-testid="gate-max-regression"/);
		expect(html).toMatch(/value="3"/);
		expect(html).toContain('data-testid="gate-min-0"');
		expect(html).toMatch(/value="40"/);
		expect(html).toContain("the live version");
	});

	it("says how many tasks the drop limit needs and offers the judge and inconclusive rules", () => {
		const html = render(<GateSettings gate={{ max_regression: 0.05 }} onChange={() => undefined} />);
		expect(html).toContain('data-testid="gate-size-hint"');
		expect(html).toContain('data-testid="gate-allow-uncalibrated"');
		expect(html).toContain('data-testid="gate-fail-inconclusive"');
		expect(sizeHint(0.05)).toMatch(/needs about 2\d\d tasks/);
		expect(sizeHint(0.35)).toMatch(/about [6-9] tasks/);
		expect(sizeHint(undefined)).toBe("");
	});
});
