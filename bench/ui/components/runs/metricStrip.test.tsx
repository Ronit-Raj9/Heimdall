import "@/lib/i18n";

import type { Value } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MetricStrip } from "./metricStrip";
import { STRIP_METRICS, stripMetricKinds, visibleStripMetrics } from "./runs.utils";

const obs = (v: number): Value => ({ state: "observed", v, evidence: "gateway" });
const kinds = stripMetricKinds(undefined);

describe("visibleStripMetrics", () => {
	it("hides optional rows no run recorded, so a run sealed before them shows the old strip", () => {
		const names = visibleStripMetrics([{ metrics: { steps: obs(3) } }]).map((m) => m.name);
		expect(names).toEqual(["steps", "tool_calls", "tokens", "cost_usd", "wall_time_s", "verified_success"]);
	});

	it("shows an optional row when any lane recorded it", () => {
		const names = visibleStripMetrics([{ metrics: {} }, { metrics: { model_turns_failed: obs(1), tool_proof_rate: obs(0.5) } }]).map(
			(m) => m.name,
		);
		expect(names).toContain("model_turns_failed");
		expect(names).toContain("tool_proof_rate");
		expect(names).not.toContain("tool_proof_none");
	});

	it("keeps the step family right under steps and the proof classes under tool calls", () => {
		const names = STRIP_METRICS.map((m) => m.name);
		const at = names.indexOf("steps");
		expect(names.slice(at, at + 4)).toEqual(["steps", "model_turns_failed", "model_attempts", "agent_steps"]);
		expect(names[names.indexOf("tool_calls") + 1]).toBe("tool_proof_rate");
	});
});

describe("MetricStrip", () => {
	it("shows turns answered, failed, attempts and agent steps together, each with what it counts", () => {
		const metrics = { steps: obs(2), model_turns_failed: obs(30), model_attempts: obs(32), agent_steps: obs(9), tool_calls: obs(4) };
		const html = renderToStaticMarkup(<MetricStrip lanes={[{ arm: "A", metrics }]} kinds={kinds} />);
		expect(html).toContain("Model replies");
		expect(html).toContain("Model calls that failed");
		expect(html).toContain("Model calls incl. retries");
		expect(html).toContain("Actions Heimdall saw");
		expect(html).toContain('data-testid="run-metric-help-steps"');
		expect(html).toContain("Not counted here: failed turns");
		expect(html.indexOf("Model replies")).toBeLessThan(html.indexOf("Model calls that failed"));
		expect(html.indexOf("Actions Heimdall saw")).toBeLessThan(html.indexOf("Tool uses"));
	});

	it("shows the proof breakdown and opaque tool traffic when recorded", () => {
		const metrics = {
			tool_calls: obs(4),
			tool_proof_rate: obs(0.75),
			tool_proof_proxy: obs(0.5),
			tool_proof_process: obs(0.25),
			tool_proof_result_returned: obs(0.25),
			tool_traffic_unparsed: obs(1),
		};
		const html = renderToStaticMarkup(<MetricStrip lanes={[{ arm: "A", metrics }]} kinds={kinds} />);
		expect(html).toContain("Tool uses Heimdall saw run");
		expect(html).toContain("Seen by the tool relay");
		expect(html).toContain("Only a reply came back");
		expect(html).toContain("does not prove the tool ran");
		expect(html).toContain("Tool traffic Heimdall could not read");
		expect(html).not.toContain("Seen only inside the agent's box");
	});
});