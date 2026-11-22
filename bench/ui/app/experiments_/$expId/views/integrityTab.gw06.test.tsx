import "@/lib/i18n";

import { verdictWith } from "@/components/verdict/testFixtures";
import { okQuery, render } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { IntegrityTab } from "./integrityTab";

const api = vi.hoisted(() => ({ verdict: {} as unknown, certs: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetVerdictQuery: () => api.verdict,
}));
vi.mock("@/lib/store/apis/integrityApi", () => ({
	useGetAwarenessQuery: () =>
		okQuery({
			exp_id: "e_x",
			realism: "standard",
			heuristic: true,
			scanner_version: "1.0.0",
			label: "Heuristic phrase scan.",
			per_arm: { A: { runs: 30, runs_scanned: 28, runs_with_signals: 0, signals: 0 } },
			examples: [],
			not_scanned: [
				{ run_id: "r_infra", reason: "no gateway log" },
				{ run_id: "r_quiet", reason: "no agent text" },
			],
		}),
	useListMonitorCertificationsQuery: () => api.certs,
}));
vi.mock("@/components/integrity/compliancePack", () => ({ CompliancePackLinks: () => null }));

beforeEach(() => {
	api.verdict = okQuery({
		exp_id: "e_x",
		grade_source: "original",
		arms: ["A"],
		verdict: verdictWith({ decision: "score", variance: undefined, mde: 0, sd: 0, mde_assumed_sd: false }),
	});
	api.certs = okQuery({ certifications: [] });
});

describe("IntegrityTab (N68, N77)", () => {
	it("shows a one-setup noise floor note, not a card claiming an A/A measurement", () => {
		const html = render(<IntegrityTab expId="e_x" />);
		expect(html).toContain(
			"The noise floor compares two setups. This run scored one setup, so there is no difference to test against noise.",
		);
		expect(html).not.toContain("measured from the A/A setup");
		expect(html).not.toContain('data-testid="variance-card"');
	});
	it("lists each run that could not be scanned with a link and its reason", () => {
		const html = render(<IntegrityTab expId="e_x" />);
		expect(html).toContain("2 runs could not be scanned");
		expect(html).toContain('href="/runs/r_infra"');
		expect(html).toContain('href="/runs/r_quiet"');
		expect(html).toContain("no agent text");
	});
	it("links every safety monitor from the monitors section", () => {
		const html = render(<IntegrityTab expId="e_x" />);
		expect(html).toContain('href="/monitors"');
		expect(html).toContain("All safety monitors");
	});
});