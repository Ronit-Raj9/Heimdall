import "@/lib/i18n";

import type { MonitorCertification, MonitorCertificationSummary } from "@/lib/types/bench";
import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { MAX_ITEMS, MonitorCertDetail, MonitorCertList, RateBar } from "./monitorCertification";

// A plain anchor stands in for the router's Link, so the target URL is visible in the markup.
vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, search, children, ...rest }: { to: string; search?: Record<string, string>; children: ReactNode }) => (
		<a href={`${to}?${new URLSearchParams(search ?? {}).toString()}`} {...rest}>
			{children}
		</a>
	),
}));

const withRouter = (node: ReactNode) => renderToStaticMarkup(node);

const cert: MonitorCertification = {
	cert_id: "mc_1",
	created_at: "2026-11-18T06:00:00Z",
	monitor: { name: "keyword", kind: "builtin", mode: "transcript", config_hash: "sha256:0123456789abcdef0123", network: "none" },
	exp_ids: ["e_x"],
	labels: {
		honest: 120,
		attack: 14,
		excluded: 6,
		rule: "honest: no veto fired; attack: safety probe veto fired",
		excluded_reasons: { "veto under clean": 4, "unverified seal": 2 },
	},
	integrity: { state: "passed", tested: 10, flipped: 0, detail: "0 of 10 caught attacks flipped" },
	operating_points: [
		{
			target_fpr: 0.01,
			supported: true,
			threshold: 0.7,
			false_positives: 1,
			honest: 120,
			achieved_fpr: 0.0083,
			fpr_interval: [0.0002, 0.0456],
			caught: 11,
			attacks: 14,
			catch_rate: 0.786,
			catch_interval: [0.492, 0.953],
		},
		{
			target_fpr: 0.001,
			supported: false,
			threshold: 0.9,
			false_positives: 0,
			honest: 120,
			achieved_fpr: 0,
			caught: 5,
			attacks: 14,
			catch_rate: 0.357,
			note: "needs 1000 honest runs",
		},
	],
	families: [
		{
			family: "prompt-injection",
			gates: ["g12_injection_followed"],
			attacks: 6,
			points: [{ target_fpr: 0.01, caught: 5, attacks: 6, catch_rate: 0.833, catch_interval: [0.359, 0.996] }],
		},
	],
	warnings: ["attacks fewer than 30"],
	items: Array.from({ length: MAX_ITEMS + 3 }, (_, i) => ({
		run_id: `r_${i}`,
		exp_id: "e_x",
		task: "t01",
		arm: "A",
		condition: "clean",
		label: "honest" as const,
		score: 0.1,
		decision: "allow",
		seal_hash: "abc",
	})),
	errors: 2,
};

describe("MonitorCertDetail", () => {
	it("shows the headline, every operating point, families, warnings and the item cap", () => {
		const html = withRouter(<MonitorCertDetail cert={cert} />);
		expect(html).toContain('data-testid="monitor-cert-headline"');
		expect(html).toContain("78.6%");
		expect(html).toContain("[49.2%, 95.3%]");
		expect(html).toContain('data-testid="monitor-point-0.001"');
		expect(html).toContain("needs 1000 honest runs");
		expect(html).toContain("prompt-injection");
		expect(html).toContain("2 monitor errors");
		expect(html).toContain("3 more runs in the JSON certificate.");
		expect(html).toContain("/monitors/certifications/mc_1/report.md");
		expect(html).toContain("veto under clean (4); unverified seal (2)");
		expect(html).toContain('data-state="passed"');
	});
});

describe("MonitorCertList", () => {
	it("lists the headline catch rate and flags a small sample", () => {
		const certs: MonitorCertificationSummary[] = [
			{
				cert_id: "mc_1",
				created_at: "2026-11-18T06:00:00Z",
				monitor_name: "keyword",
				monitor_kind: "builtin",
				mode: "transcript",
				exp_ids: ["e_x"],
				honest: 40,
				attacks: 3,
				headline: { target_fpr: 0.01, catch_rate: 0.667, catch_interval: [0.094, 0.992], supported: false },
			},
		];
		const html = withRouter(<MonitorCertList certs={certs} />);
		expect(html).toContain('data-testid="monitor-cert-mc_1"');
		expect(html).toContain("66.7%");
		expect(html).toContain("sample too small");
		expect(html).toContain("cert=mc_1");
	});
});

describe("MonitorCertList without attacks", () => {
	it("says there was nothing to score instead of a 0-100% interval", () => {
		const html = withRouter(
			<MonitorCertList
				certs={[
					{
						cert_id: "mc_2",
						created_at: "2026-11-18T06:00:00Z",
						monitor_name: "k",
						monitor_kind: "builtin",
						mode: "transcript",
						exp_ids: ["e_x"],
						honest: 289,
						attacks: 0,
						headline: { target_fpr: 0.01, catch_rate: null, catch_interval: [0, 1], supported: false },
					},
				]}
			/>,
		);
		expect(html).toContain("no attack runs to score");
		expect(html).not.toContain("[0%, 100%]");
	});
});

describe("RateBar", () => {
	it("draws an empty bar when there is nothing to rate", () => {
		expect(renderToStaticMarkup(<RateBar rate={null} supported={false} />)).toContain("width:0%");
	});
	it("draws the rate and its interval", () => {
		const html = renderToStaticMarkup(<RateBar rate={0.5} interval={[0.2, 0.8]} supported testId="bar" />);
		expect(html).toContain("width:50%");
		expect(html).toContain("left:20%");
	});
});