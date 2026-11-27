import "@/lib/i18n";

import { experimentRoutingPath, runRoutingPath } from "@/lib/store/apis/routingApi";
import type { RoutingSummary, RunRouting } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { formatShare, GatewayPolicyCard, RunGatewayPolicyCard } from "./gatewayPolicyCard";

// Shaped after the $0 proof of experiment (a) (docs/routing-experiments.md): arm A fixed sonnet,
// arm B a sonnet to haiku fallback chain, one model-outage run each.
const summary: RoutingSummary = {
	exp_id: "e_hd_w9_proof_routing_outage_gated_e5tn",
	routed: true,
	differences: ["strategy: A fixed; B fallback_chain", "fallbacks: A none; B claude-haiku-4-5"],
	arms: [
		{
			arm: "A",
			label: "fixed claude-sonnet-4-5",
			model: "claude-sonnet-4-5",
			policy: { strategy: "fixed", targets: [{ model: "claude-sonnet-4-5" }] },
			policy_hash: "4029040a2f97091218f05d82",
			summary: "fixed: anthropic/claude-sonnet-4-5",
			conditions: [
				{
					condition: "model-outage",
					runs: 1,
					fallback_runs: 0,
					fallback_known: 1,
					switches: 0,
					switches_known: 1,
					targets: [{ target: "anthropic/claude-sonnet-4-5", turn_share: 1, cost_usd: 0.004485, latency_ms: 31 }],
				},
			],
		},
		{
			arm: "B",
			label: "fallback chain sonnet then haiku",
			model: "claude-sonnet-4-5",
			policy: { strategy: "fallback_chain", targets: [{ model: "claude-sonnet-4-5" }], fallbacks: [{ model: "claude-haiku-4-5" }] },
			policy_hash: "8d1cbe65c7f506b7ecdf38f4",
			summary: "fallback_chain: anthropic/claude-sonnet-4-5; fallbacks anthropic/claude-haiku-4-5",
			conditions: [
				{
					condition: "model-outage",
					runs: 1,
					fallback_runs: 1,
					fallback_known: 1,
					switches: 1,
					switches_known: 1,
					targets: [
						{ target: "anthropic/claude-sonnet-4-5", turn_share: 2 / 3, cost_usd: 0.004485, latency_ms: 10.6 },
						{ target: "anthropic/claude-haiku-4-5", turn_share: 1 / 3 },
					],
				},
			],
		},
	],
};

const run: RunRouting = {
	run_id: "r_6zi5wmxr3y",
	arm: "B",
	condition: "model-outage",
	model: "claude-sonnet-4-5",
	policy: summary.arms[1].policy,
	summary: summary.arms[1].summary,
	fallback_used: true,
	switches: 1,
	targets: summary.arms[1].conditions[0].targets,
	rows: [
		{
			id: "p1",
			provider: "anthropic",
			model_served: "claude-sonnet-4-5",
			fallback_index: 0,
			status: "error",
			error_status: 503,
			latency_ms: 5,
			routing_rule_name: "heimdall-route-r_6zi5wmxr3y",
		},
		{
			id: "f1",
			parent_id: "p1",
			provider: "anthropic",
			model_served: "claude-haiku-4-5",
			fallback_index: 1,
			status: "success",
			latency_ms: 7,
			routing_rule_name: "heimdall-route-r_6zi5wmxr3y",
		},
	],
	fidelity: {
		gate: "model_fidelity",
		version: "1.0.0",
		effect: "invalidate",
		fired: false,
		detail: "routing policy fallback_chain: 1 request(s) served by another policy model than claude-sonnet-4-5 (expected switch)",
	},
};

describe("GatewayPolicyCard", () => {
	it("describes the arms by their policy difference and shows fallbacks, switches and targets", () => {
		const html = renderToStaticMarkup(<GatewayPolicyCard summary={summary} />);
		expect(html).toContain('data-testid="gateway-policy-card"');
		expect(html).toContain("strategy: A fixed; B fallback_chain");
		expect(html).toContain("fallback answered in 1 of 1 runs");
		expect(html).toContain("1 model switches");
		expect(html).toContain("anthropic/claude-haiku-4-5");
		expect(html).toContain("67%");
		expect(html).toContain("$0.0045");
		expect(html).toContain("policy 8d1cbe65c7f5");
	});

	it("says unknown for a cost or latency it did not observe, never 0", () => {
		const html = renderToStaticMarkup(<GatewayPolicyCard summary={summary} />);
		const haikuRow = html.slice(html.lastIndexOf("anthropic/claude-haiku-4-5"));
		expect(haikuRow).toContain("unknown");
		expect(haikuRow).not.toContain("$0.0000");
	});

	it("says so when the arms run the same policy", () => {
		const html = renderToStaticMarkup(<GatewayPolicyCard summary={{ ...summary, differences: [] }} />);
		expect(html).toContain("Same gateway policy.");
	});
});

describe("RunGatewayPolicyCard", () => {
	it("shows the fallback row, the rule and the expected switch the gate reported", () => {
		const html = renderToStaticMarkup(<RunGatewayPolicyCard routing={run} />);
		expect(html).toContain('data-testid="run-gateway-policy-B"');
		expect(html).toContain("fallback answered");
		expect(html).toContain("anthropic/claude-sonnet-4-5 error 503");
		expect(html).toContain("fallback 1");
		expect(html).toContain("heimdall-route-r_6zi5wmxr3y");
		expect(html).toContain("model fidelity: routing policy fallback_chain");
	});

	it("marks a fired gate", () => {
		const html = renderToStaticMarkup(
			<RunGatewayPolicyCard
				routing={{
					...run,
					fidelity: { ...run.fidelity!, fired: true, detail: "1 request(s): 1 served by a model outside the routing policy" },
				}}
			/>,
		);
		expect(html).toContain("model fidelity fired: 1 request(s)");
	});
});

describe("routing paths", () => {
	it("escapes ids as one path segment", () => {
		expect(experimentRoutingPath("e/1")).toBe("/experiments/e%2F1/routing");
		expect(runRoutingPath("r_1")).toBe("/runs/r_1/routing");
		expect(formatShare(1 / 3)).toBe("33%");
	});
});