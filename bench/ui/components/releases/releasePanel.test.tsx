import "@/lib/i18n";

import { ReduxProvider } from "@/lib/store";
import { renderToStaticMarkup } from "react-dom/server";
import { okQuery } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { ReleasePanel } from "./releasePanel";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const api = vi.hoisted(() => ({ list: undefined as unknown }));
vi.mock("@/lib/store", async (importOriginal) => {
	const real = await importOriginal<typeof import("@/lib/store")>();
	return {
		...real,
		useListExperimentsQuery: (...args: Parameters<typeof real.useListExperimentsQuery>) =>
			(api.list as ReturnType<typeof real.useListExperimentsQuery> | undefined) ?? real.useListExperimentsQuery(...args),
	};
});

describe("ReleasePanel", () => {
	it("renders the form with the release button disabled before any data arrives", () => {
		// Server rendering runs no effects, so no request is sent: this is the panel's first paint.
		const html = renderToStaticMarkup(
			<ReduxProvider>
				<ReleasePanel />
			</ReduxProvider>,
		);
		expect(html).toContain('data-testid="release-panel"');
		expect(html).toContain('data-testid="release-form"');
		expect(html).toMatch(
			/<button[^>]*disabled=""[^>]*data-testid="release-submit"|<button[^>]*data-testid="release-submit"[^>]*disabled=""/,
		);
		expect(html).not.toContain('data-testid="release-override-checkbox"');
	});
	it("offers quick runs behind a checkbox and lists no readiness checks", () => {
		const exp = (exp_id: string, name: string, suite: string) => ({
			exp_id,
			name,
			state: "done",
			manifest_hash: "h",
			frozen_at: "2026-11-20T10:00:00Z",
			slots: { total: 2, done: 2, running: 0, failed: 0 },
			spent_usd: { state: "observed", v: 0 },
			suite,
			arms: ["A", "B"],
		});
		api.list = okQuery({
			experiments: [
				exp("e_admit_acme_safety_5e8q", "admit-acme-safety", "acme-safety@1.0.0"),
				exp("e_quick", "quick-20261121-101500-ab12", "tb2@1.0.0"),
				exp("e_quick2", "quick-20261121-111500-cd34", "tb2@1.0.0"),
			],
		});
		const html = renderToStaticMarkup(
			<ReduxProvider>
				<ReleasePanel />
			</ReduxProvider>,
		);
		api.list = undefined;
		expect(html).toContain('data-testid="release-show-quick"');
		expect(html).toContain("Show quick runs (2)");
		// Nothing citable without quick runs: the empty note shows, not the check.
		expect(html).toContain('data-testid="release-no-experiments"');
	});
});