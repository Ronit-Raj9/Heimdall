import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { GateSettings } from "./gateSettings";

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
});
