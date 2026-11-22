import "@/lib/i18n";

import type { BifrostUse } from "@/lib/types/bifrostUse";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { BifrostFeatureList, BifrostGatewayLine, CacheABTable, bifrostUseStatus } from "./bifrostUse";

const use: BifrostUse = {
	gateway_configured: true,
	gateway_host: "bifrost:8080",
	reachable: true,
	loaded_plugins: ["governance", "heimdall", "logging"],
	features: [
		{ id: "virtual_keys", name: "Virtual key per run", state: "on", detail: "One key per run.", docs: "https://docs.getbifrost.ai/features/governance/virtual-keys", code: "bench/gateways/bifrost/keys.go" },
		{ id: "semantic_cache", name: "Semantic cache", state: "available", detail: "Cache on vs off.", docs: "https://docs.getbifrost.ai/features/semantic-caching" },
	],
};

describe("Bifrost use", () => {
	it("lists features with state, docs link and code path", () => {
		const html = renderToStaticMarkup(<BifrostFeatureList features={use.features} testId="f" />);
		expect(html).toContain("Virtual key per run");
		expect(html).toContain("In use");
		expect(html).toContain("Available");
		expect(html).toContain("https://docs.getbifrost.ai/features/governance/virtual-keys");
		expect(html).toContain("bench/gateways/bifrost/keys.go");
		expect(html).toContain('data-state="available"');
	});
	it("shows the gateway and loaded plugins, or why it could not be asked", () => {
		expect(renderToStaticMarkup(<BifrostGatewayLine use={use} />)).toContain("governance, heimdall, logging");
		const down = renderToStaticMarkup(<BifrostGatewayLine use={{ ...use, reachable: false, check_error: "gateway not reachable" }} />);
		expect(down).toContain("gateway not reachable");
		expect(renderToStaticMarkup(<BifrostGatewayLine use={{ ...use, gateway_configured: false }} />)).toContain("No Bifrost gateway");
	});
	it("counts features in use for the card status", () => {
		expect(bifrostUseStatus(use)).toEqual({ tone: "ok", count: 1 });
		expect(bifrostUseStatus({ ...use, reachable: false })).toEqual({ tone: "warn", count: 1 });
		expect(bifrostUseStatus(undefined)).toEqual({ tone: "idle", count: 0 });
	});
	it("shows the cache on vs off report per benchmark", () => {
		const arm = { arm: "A", runs: 2, requests: 4, cache_hits: 0, hit_rate: 0, mean_latency_ms: 100, cost_usd: 0.04, cost_runs: 2, successes: 2, success_runs: 2, success_rate: 1 };
		const html = renderToStaticMarkup(
			<CacheABTable
				report={{
					evaluation_id: "ev_x",
					enabled: true,
					children: [{ benchmark: "swe", experiment_id: "e1", report: { off: arm, on: { ...arm, arm: "cache", cache_hits: 2, hit_rate: 0.5 }, tasks: 2, latency_delta_ms: -40, cost_saved_usd: 0.01, success_delta: 0, summary: "s" } }],
				}}
			/>,
		);
		expect(html).toContain("Bifrost cache on vs off");
		expect(html).toContain("50%");
		expect(html).toContain("-40 ms");
		expect(html).toContain("$0.0100");
		expect(renderToStaticMarkup(<CacheABTable report={{ evaluation_id: "ev_x", enabled: true, children: [] }} />)).toContain("No benchmark has finished");
	});
});

