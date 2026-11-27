import "@/lib/i18n";

import { errorQuery, okQuery, render } from "@/lib/testing/render";
import type { GwEffect } from "@/lib/types/gateway";
import { describe, expect, it, vi } from "vitest";
import { GatewayEffectCard, GatewayEffectView, detailRows, effectOf } from "./gatewayEffectCard";

const state = vi.hoisted(() => ({ effect: undefined as unknown }));

vi.mock("@/lib/store/apis/gatewayApi", async (orig) => ({
	effectOf: ((await orig()) as { effectOf: unknown }).effectOf,
	useGetBifrostSettingQuery: () => ({ data: undefined }),
	useGetGatewayReportCardQuery: () => ({ data: undefined }),
	useGetGatewayEffectOptionalQuery: () => state.effect,
}));
vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...((await orig()) as object),
	useGetExperimentQuery: () => ({ data: undefined }),
}));
vi.mock("@/app/gateway/views/featureTestDialog", () => ({
	FeatureTestDialog: () => null,
	suiteOption: (s: { id: string; version?: string }) => (s.version ? `${s.id}@${s.version}` : s.id),
}));

const effect: GwEffect = {
	feature: "semantic_cache",
	exp_id: "e1",
	control: "A",
	variant: "B",
	state: "measured",
	headline: "The cache saved 31% of cost with no loss in solve rate.",
	result: "saved",
	tasks: 40,
	arms: [
		{ arm: "A", label: "cache off", runs: 80, tasks: 40, solve_rate: 0.6, cost_per_run: 0.05, latency_ms: 900 },
		{ arm: "B", label: "cache on at 0.80", runs: 80, tasks: 40, solve_rate: 0.6, cost_per_run: 0.0345 },
	],
	details: { wrong_hit_share: 0.01, best_threshold: "0.90" },
	setting: { kind: "plugin_config", title: "Semantic cache threshold", where: "Bifrost dashboard, Plugins", body: "threshold: 0.90" },
	source: { kind: "runs", runs: 160, rows: 900, raw_rows: 900, text: "160 runs of this comparison" },
	fingerprint: "f",
	computed_at: "2026-11-20T10:00:00Z",
};

describe("GatewayEffectCard", () => {
	it("renders nothing when the comparison is not a feature test (404)", () => {
		state.effect = errorQuery(404, "this comparison is not a Bifrost feature test");
		expect(render(<GatewayEffectCard expId="e1" />)).toBe("");
		state.effect = { data: undefined, isLoading: true };
		expect(render(<GatewayEffectCard expId="e1" />)).toBe("");
	});

	it("shows state, headline, setups, details and the button", () => {
		state.effect = okQuery(effect);
		const html = render(<GatewayEffectCard expId="e1" />);
		expect(html).toContain("Bifrost feature test");
		expect(html).toContain("Measured");
		expect(html).toContain("Saved");
		expect(html).toContain("The cache saved 31% of cost");
		expect(html).toContain("cache on at 0.80");
		expect(html).toContain("Today");
		expect(html).toContain("60%");
		expect(html).toContain("$0.0345");
		expect(html).toContain("Raw data");
		expect(html).toContain("wrong_hit_share");
		expect(html).toContain("From: 160 runs of this comparison");
		expect(html).toContain("Use this in Bifrost");
	});

	it("explains a test that measured nothing", () => {
		const html = render(
			<GatewayEffectView
				effect={{ ...effect, state: "not_applied", result: undefined, reason: "the agent did not open the skill in any run" }}
			/>,
		);
		expect(html).toContain("The setting was not applied");
		expect(html).toContain("Unknown: the agent did not open the skill in any run");
	});

	it("labels known details, counts lists, keeps the rest under Raw data, and reads {effect: null}", () => {
		const { rows, raw } = detailRows({ recommended: 0.9, thresholds: [1, 2], other: 3, filter: { a: 1 } });
		expect(rows).toEqual([
			{ key: "recommended", value: 0.9 },
			{ key: "thresholds", value: [1, 2] },
		]);
		expect(raw).toBe(true);
		expect(detailRows({ recommended: 0.9 }).raw).toBe(false);
		const html = render(<GatewayEffectView effect={{ ...effect, details: { recommended: 0.9, thresholds: [1, 2] } }} />);
		expect(html).toContain("Recommended threshold");
		expect(html).toContain("2 entries");
		expect(effectOf({ effect: null })).toBeNull();
		expect(effectOf(effect)).toBe(effect);
		state.effect = okQuery(null);
		expect(render(<GatewayEffectCard expId="e1" />)).toBe("");
	});

	it("says what to do when the effect is unknown", () => {
		const html = render(<GatewayEffectView effect={{ ...effect, state: "unknown", result: undefined, reason: "no rows" }} />);
		expect(html).toContain("This test could not show an effect: no rows. Check that the setting is on in Bifrost, then run it again.");
	});

	it("offers to run an unknown test again, and only then", () => {
		const unknown = { ...effect, state: "unknown" as const, result: undefined, reason: "no rows" };
		expect(render(<GatewayEffectView effect={unknown} onRunAgain={() => {}} />)).toContain("Run this test again");
		expect(render(<GatewayEffectView effect={effect} onRunAgain={() => {}} />)).not.toContain("gateway-effect-run-again");
		state.effect = okQuery(unknown);
		expect(render(<GatewayEffectCard expId="e1" />)).toContain('data-testid="gateway-effect-run-again"');
	});
});
