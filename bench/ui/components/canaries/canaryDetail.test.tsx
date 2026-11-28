import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { CANARY_DRIFTED_FIXTURE, CANARY_STEADY_FIXTURE } from "./canary.fixtures";
import { CanaryDetailBody } from "./canaryDetail";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("CanaryDetailBody", () => {
	const html = renderToStaticMarkup(<CanaryDetailBody detail={CANARY_DRIFTED_FIXTURE} />);

	it("states the drift and why", () => {
		expect(html).toMatch(/data-testid="canary-detail-state"[^>]*>Got worse</);
		expect(html).toContain("drift confirmed by two consecutive looks");
		expect(html).toContain("Baseline e_canary_1");
	});

	it("draws the history with the trends chart, one line per target", () => {
		expect(html).toContain('data-testid="trends-chart"');
		expect(html).toContain('data-testid="trends-series-nightly|A"');
		expect(html).toContain('data-testid="trends-series-nightly|B"');
	});

	it("shows what changed: the served model under the same alias, latency, new clusters, flips", () => {
		expect(html).toContain('data-testid="canary-moved"');
		expect(html).toContain("served model changed under the same alias");
		expect(html).toMatch(/data-testid="canary-served-A" data-changed="true"/);
		expect(html).toContain("claude-sonnet-4-5-20261101");
		expect(html).toContain('data-testid="canary-new-clusters"');
		expect(html).toContain("lost success");
	});

	it("lists the tests, the provider equivalence and every look", () => {
		expect(html).toMatch(/data-testid="canary-test-A\/clean\/verified_success"/);
		expect(html).toMatch(/data-status="worse"/);
		expect(html).toContain('data-testid="canary-equivalence-A-B"');
		expect(html).toMatch(/data-verdict="differs"/);
		expect((html.match(/data-testid="canary-look-/g) ?? []).length).toBe(CANARY_DRIFTED_FIXTURE.records.length);
	});

	it("says when nothing moved on a steady canary without a provider pair", () => {
		const steady = renderToStaticMarkup(<CanaryDetailBody detail={CANARY_STEADY_FIXTURE} />);
		expect(steady).toMatch(/data-testid="canary-detail-state"[^>]*>(Too few runs to tell\.|No check yet)</); // lane F2: a steady look on under 10 runs, or none, is not a clean "No change"
		expect(steady).not.toContain('data-testid="canary-equivalence"');
		expect(steady).toContain('data-testid="canary-tests-table"');
	});
});