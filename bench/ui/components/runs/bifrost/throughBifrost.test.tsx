import "@/lib/i18n";

import { runBifrostPath, type RunBifrost } from "@/lib/store/apis/runBifrostApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ms, ThroughBifrost, tokenSum, usd } from "./throughBifrost";

const tok = (fresh: number, out: number) => ({ fresh, cache_read: 0, cache_write: 0, out });

const data: RunBifrost = {
	run_id: "r1",
	available: true,
	complete: false,
	source: "both",
	providers: ["anthropic", "openai"],
	models: ["claude-sonnet-4-5", "gpt-5"],
	calls: 2,
	fallbacks: 1,
	cache_hits: 1,
	faults: 1,
	errors: 1,
	tokens: tok(100, 20),
	cost_usd: 0.002,
	cost_known: 1,
	latency_ms: 1520,
	avg_latency_ms: 760,
	call_list: [
		{
			id: "p",
			at: "",
			provider: "anthropic",
			model_requested: "claude-sonnet-4-5",
			model_served: "claude-sonnet-4-5",
			status: "error",
			error_status: 503,
			fallback_index: 0,
			cache_hit: false,
			fault: "inject 503",
			latency_ms: 20,
		},
		{
			id: "f",
			at: "",
			provider: "openai",
			model_requested: "claude-sonnet-4-5",
			model_served: "gpt-5",
			status: "success",
			fallback_index: 1,
			cache_hit: true,
			tokens: tok(100, 20),
			cost_usd: 0.002,
			latency_ms: 1500,
		},
	],
};

describe("ThroughBifrost", () => {
	it("shows the run's Bifrost facts and one row per call", () => {
		const html = renderToStaticMarkup(<ThroughBifrost data={data} arm="A" />);
		expect(html).toContain('data-testid="bifrost-A"');
		expect(html).toContain("From Bifrost&#x27;s request logs and the Heimdall Bifrost plugin");
		expect(html).toContain('data-testid="bifrost-A-incomplete"');
		expect(html).toContain("1 of 2 calls reported a cost");
		expect(html).toContain("$0.0020");
		expect(html).toContain("anthropic, openai");
		expect(html).toContain("failed 503");
		expect(html).toContain("inject 503");
		expect(html).toContain("fallback 1");
		expect(html).toContain("asked for claude-sonnet-4-5");
		expect(html).toContain("1.5 s");
		expect(html).toContain('data-testid="bifrost-call-2"');
	});

	it("says so when the run has no Bifrost records", () => {
		const html = renderToStaticMarkup(<ThroughBifrost data={{ ...data, available: false, call_list: [] }} arm="B" />);
		expect(html).toContain('data-testid="bifrost-B-empty"');
		expect(html).not.toContain("bifrost-B-calls-table");
	});

	it("formats and escapes", () => {
		expect(tokenSum(undefined)).toBeUndefined();
		expect(tokenSum(tok(3, 4))).toBe(7);
		expect(usd(0.5)).toBe("$0.50"); // fix wave 3: the one money format (lib/money.ts)
		expect(usd(0.0005)).toBe("$0.0005");
		expect(ms(12.4)).toBe("12 ms");
		expect(runBifrostPath("a/b")).toBe("/runs/a%2Fb/bifrost");
	});
	it("says which path applied the faults", () => {
		const plugin = renderToStaticMarkup(<ThroughBifrost data={{ ...data, fault_path: "plugin" }} arm="A" />);
		expect(plugin).toContain("1 by the plugin");
		const proxy = renderToStaticMarkup(<ThroughBifrost data={{ ...data, fault_path: "proxy" }} arm="A" />);
		expect(proxy).toContain("1 by the proxy");
		const mixed = renderToStaticMarkup(<ThroughBifrost data={{ ...data, fault_path: "mixed" }} arm="A" />);
		expect(mixed).toContain("1 by plugin and proxy");
		const none = renderToStaticMarkup(<ThroughBifrost data={{ ...data, faults: 0, fault_path: undefined }} arm="A" />);
		expect(none).not.toContain("by the");
	});
});