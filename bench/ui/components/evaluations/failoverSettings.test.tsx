import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { FailoverSection, failoverProblem } from "./failoverSettings";

describe("failoverProblem", () => {
	const arm = { label: "A", agent: "codex", model: "openai/gpt-4o" };
	it("accepts a same-provider fallback", () => {
		expect(failoverProblem(arm, { enabled: true, fallbacks: [{ provider: "openai", model: "gpt-4o-mini" }] })).toBe("");
		expect(failoverProblem(arm, undefined)).toBe("");
	});
	it("refuses a missing, other-provider or same model", () => {
		expect(failoverProblem(arm, { enabled: true, fallbacks: [] })).toMatch(/Pick a fallback/);
		expect(failoverProblem(arm, { enabled: true, fallbacks: [{ provider: "anthropic", model: "claude" }] })).toMatch(/same provider/);
		expect(failoverProblem(arm, { enabled: true, fallbacks: [{ provider: "openai", model: "gpt-4o" }] })).toMatch(/different model/);
		expect(failoverProblem({ ...arm, model: "" }, { enabled: true, fallbacks: [] })).toMatch(/model first/);
	});
});

describe("FailoverSection", () => {
	it("shows worked, latency, cost and success with vs without", () => {
		const s = { calls: 2, errors: 0, fallback_served: 2, mean_latency_ms: 1000, cost_usd: 0.01, cost_known: true, task_success: true };
		const html = render(
			<FailoverSection
				report={{
					clean: s,
					failover: s,
					no_failover: { ...s, task_success: false },
					failover_worked: true,
					extra_latency_ms: 1500,
					cost_change_usd: -0.002,
					success_with_failover: true,
					success_without_failover: false,
					summary: "Failover worked.",
					per_run: true,
				}}
			/>,
		);
		expect(html).toContain("Provider failover");
		expect(html).toMatch(/data-testid="failover-worked">Yes</);
		expect(html).toMatch(/passed(<!-- -->)? \/ (<!-- -->)?failed/);
		expect(html).toContain("+1.5 s");
		expect(html).toContain("-$0.0020");
	});
});
