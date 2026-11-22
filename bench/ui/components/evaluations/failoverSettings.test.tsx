import { okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { FailoverSection, FailoverSettings, failoverProblem } from "./failoverSettings";

const q = vi.hoisted(() => ({ models: {} as Record<string, unknown> }));
vi.mock("@/lib/store/apis/modelsApi", () => ({ useGetModelsQuery: () => q.models }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

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

describe("FailoverSettings fallback list (N89)", () => {
	const provider = (id: string, name: string, model: string) => ({ id, name, needs_base_url: false, key_hint: "", models: [{ id: model, purposes: ["agent"], rank: 1 }] });
	q.models = okQuery({
		providers: [provider("openai", "OpenAI", "gpt-4o-mini"), provider("anthropic", "Anthropic", "claude-haiku"), provider("groq", "Groq", "llama-3-8b")],
		keys: [],
	});
	it("lists only the provider of the agent's model", () => {
		const html = render(
			<FailoverSettings arm={{ label: "A", agent: "codex", provider: "openai", model: "gpt-4o" }} value={{ enabled: true, fallbacks: [] }} onChange={() => undefined} />,
		);
		expect(html).toContain("gpt-4o-mini");
		expect(html).not.toContain("claude-haiku");
		expect(html).not.toContain("Groq");
		expect(html).toContain("Pick a fallback model.");
	});
	it("waits for the agent's model before listing anything", () => {
		const html = render(<FailoverSettings arm={{ label: "A", agent: "codex", model: "" }} value={{ enabled: true, fallbacks: [] }} onChange={() => undefined} />);
		expect(html).not.toContain("gpt-4o-mini");
		expect(html).toContain("Pick the agent&#x27;s model first");
	});
});
