import "@/lib/i18n";

import { runRemotePath } from "@/lib/store/apis/runRemoteApi";
import type { RemoteEvidence } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { RemoteCoverage } from "./remoteCoverage";

const remote: RemoteEvidence = {
	channels: { model_traffic: "off", tools: "on", self_report: "on" },
	trusted: ["outcome (verifier)", "tool calls through the published tool proxy (tool ledger)"],
	agent_reported: ["reported_tokens", "version and build (self-declared)"],
	unknown: ["tokens", "cost_usd"],
	reported: { input_tokens: 100, output_tokens: 20, cost_usd: 0.25, version: "<img src=x onerror=alert(1)>", build: "b7", events: 3 },
};

describe("RemoteCoverage", () => {
	it("shows each channel, the three lists and the self-report under its badge", () => {
		const html = renderToStaticMarkup(<RemoteCoverage remote={remote} arm="A" />);
		expect(html).toContain('data-testid="remote-coverage-A"');
		expect(html).toContain("Model traffic through the gateway: off");
		expect(html).toContain("Tools through the bench: on");
		expect(html).toContain("tool ledger");
		expect(html).toContain("agent-reported");
		expect(html).toContain("120 tokens");
		expect(html).toContain("$0.2500");
		expect(html).toContain("3 events");
		expect(html).toContain('data-testid="remote-unknown-A"');
	});

	it("renders agent text inert", () => {
		const html = renderToStaticMarkup(<RemoteCoverage remote={remote} arm="A" />);
		expect(html).not.toContain("<img");
		expect(html).toContain("&lt;img");
	});

	it("leaves out the self-report when there is none and marks a missing channel", () => {
		const html = renderToStaticMarkup(
			<RemoteCoverage remote={{ channels: { model_traffic: "on", tools: "missing", self_report: "off" }, trusted: ["tokens"] }} arm="B" />,
		);
		expect(html).toContain("Tools through the bench: missing");
		expect(html).not.toContain('data-testid="remote-self-report-B"');
		expect(html).not.toContain('data-testid="remote-unknown-B"');
	});

	it("escapes the run id in the request path", () => {
		expect(runRemotePath("r/1")).toBe("/runs/r%2F1/remote");
	});
});
