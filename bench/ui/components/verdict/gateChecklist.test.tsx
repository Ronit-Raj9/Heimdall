import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { GateChecklist } from "./gateChecklist";
import { verdictFixture } from "./testFixtures";

describe("GateChecklist", () => {
	it("renders one row per gate with effect and fired ratio", () => {
		const html = renderToStaticMarkup(<GateChecklist gates={verdictFixture.gates} />);
		for (const gate of verdictFixture.gates) expect(html).toContain(`data-testid="verdict-gate-row-${gate.gate}"`);
		expect(html).toContain(">0/48 runs<");
		expect(html).toContain(">6/48 runs<");
		expect(html).toContain(">invalidate<");
		expect(html).toContain(">report<");
		expect(html).toMatch(/data-testid="verdict-gate-row-false-completion" data-fired="true"/);
		expect(html).toMatch(/data-testid="verdict-gate-row-egress" data-fired="false"/);
	});

	it("shows unknown runs apart", () => {
		const html = renderToStaticMarkup(
			<GateChecklist
				gates={[{ gate: "egress", version: "1.0.0", effect: "invalidate", baseline: true, fired: { num: 1, den: 40, unknown: 8 } }]}
			/>,
		);
		expect(html).toContain(">1/40 runs<");
		expect(html).toContain("+8 unknown");
		expect(html).toContain("text-chart-error-ink");
	});

	it("says so when no gate ran", () => {
		expect(renderToStaticMarkup(<GateChecklist gates={[]} />)).toContain('data-testid="verdict-gates-empty"');
	});
});