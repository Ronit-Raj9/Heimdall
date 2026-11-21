import "@/lib/i18n";

import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { applyPatch, armsFor, draftToSpec, EMPTY_DRAFT, learnStepReady, type EvaluateDraft } from "./evaluateDraft";
import { agentCapabilities, splitAgents, type AgentWithCaps } from "./evaluate.utils";
import EvaluateView, { maxReachable, stepReady } from "./evaluateView";

const agent = (id: string, extra: Partial<AgentWithCaps> = {}) => ({ id, adapter: "command", model: "gpt-x", scripted_kind: "", ...extra }) as AgentWithCaps;
const agents = [agent("codex"), agent("my-bot", { adapter: "remote_http" }), agent("oracle", { scripted_kind: "oracle" }), agent("opencode")];

vi.mock("@/lib/store/apis/benchApi", () => ({
	useGetAgentsQuery: () => ({ isLoading: false, isError: false, data: { agents }, refetch: () => undefined }),
}));
vi.mock("@/lib/store/apis/evaluationsApi", () => ({
	BASELINE_AGENTS: ["codex", "claude-code", "mini-swe-agent", "opencode", "gemini-cli"],
	useGetAgentVersionsQuery: () => ({ isLoading: false, data: { agent: "codex", versions: [{ version: 1, runs: 3 }, { version: 2, runs: 0 }] } }),
}));
vi.mock("@tanstack/react-router", () => ({
	Link: ({ children, ...p }: { children: React.ReactNode; "data-testid"?: string }) => <a data-testid={p["data-testid"]}>{children}</a>,
}));

const render = (search = "", initial?: Partial<EvaluateDraft>) =>
	renderToStaticMarkup(
		<NuqsTestingAdapter searchParams={search}>
			<EvaluateView initial={initial} />
		</NuqsTestingAdapter>,
	);

const withAgent = (patch: Partial<EvaluateDraft> = {}) => applyPatch({ ...EMPTY_DRAFT }, { agent: "codex", ...patch });

describe("evaluateDraft", () => {
	it("derives arms per mode", () => {
		expect(armsFor(withAgent(), "gpt-x")).toEqual([{ label: "A", agent: "codex", model: "gpt-x" }]);
		const v = withAgent({ mode: "versions", version: 1, compareVersion: 2 });
		expect(v.arms.map((a) => [a.label, a.agent, a.version])).toEqual([
			["A", "codex", 1],
			["B", "codex", 2],
		]);
		const o = withAgent({ mode: "opponent", opponent: "opencode" });
		expect(o.arms[1].agent).toBe("opencode");
		const m = withAgent({ mode: "models" });
		expect(m.arms).toHaveLength(2);
		expect(m.arms[1].model).toBe("");
	});

	it("keeps chosen models when the mode changes", () => {
		let d = withAgent({ mode: "models" });
		d = applyPatch(d, { arms: [{ ...d.arms[0], model: "m1" }, { ...d.arms[1], model: "m2" }] });
		d = applyPatch(d, { mode: "score" });
		d = applyPatch(d, { mode: "models" });
		expect(d.arms.map((a) => a.model)).toEqual(["m1", "m2"]);
	});

	it("checks step 2 readiness", () => {
		expect(learnStepReady(withAgent())).toBe(true);
		expect(learnStepReady(withAgent({ mode: "versions", version: 1, compareVersion: 1 }))).toBe(false);
		expect(learnStepReady(withAgent({ mode: "versions", version: 1, compareVersion: 2 }))).toBe(true);
		expect(learnStepReady(withAgent({ mode: "opponent" }))).toBe(false);
	});

	it("builds the spec with clean always on", () => {
		const spec = draftToSpec({ ...withAgent(), faults: ["broken-tool"], benchmarks: ["hd-harbor-tb2"] });
		expect(spec.faults).toEqual(["clean", "broken-tool"]);
		expect(spec.mode).toBe("score");
		expect(spec.tasks_per_benchmark).toBe(5);
		expect(spec.trials).toBe(2);
		expect(spec.name).toBe("codex score");
	});

	it("gates steps", () => {
		expect(maxReachable(EMPTY_DRAFT)).toBe(1);
		const d = applyPatch(withAgent(), { benchmarks: ["x"] });
		expect(stepReady(3, d)).toBe(true);
		expect(stepReady(4, d)).toBe(false);
		expect(maxReachable(d)).toBe(4);
	});
});

describe("evaluate utils", () => {
	it("defaults capabilities by adapter", () => {
		expect(agentCapabilities(agent("codex"))).toEqual(["shell", "tools", "mcp"]);
		expect(agentCapabilities(agent("x", { adapter: "remote_http" }))).toEqual(["tools", "sessions"]);
		expect(agentCapabilities(agent("x", { capabilities: ["voice"] }))).toEqual(["voice"]);
		expect(agentCapabilities(agent("x"))).toEqual(["shell", "tools"]);
	});
	it("splits own and built-in, drops test agents", () => {
		const { own, builtin } = splitAgents(agents);
		expect(own.map((a) => a.id)).toEqual(["my-bot"]);
		expect(builtin.map((a) => a.id)).toEqual(["codex", "opencode"]);
	});
});

describe("EvaluateView", () => {
	it("renders the stepper and step 1 agent cards with capabilities", () => {
		const html = render();
		expect(html).toContain('data-testid="evaluate-stepper"');
		expect(html).toContain("Your agent");
		expect(html).toContain('data-testid="evaluate-agent-codex"');
		expect(html).toContain('data-testid="evaluate-agent-my-bot"');
		expect(html).not.toContain('data-testid="evaluate-agent-oracle"');
		expect(html).toContain('data-testid="cap-mcp"');
		expect(html).toContain("Register your agent");
		expect(html).toContain("Pick an agent to continue.");
	});

	it("shows the version picker once an agent is picked", () => {
		const html = render("", { agent: "codex" });
		expect(html).toContain('data-testid="evaluate-version"');
		expect(html).toContain("v1 (3 runs)");
	});

	it("step 2 shows the four modes with one sentence each", () => {
		const html = render("?step=2", { agent: "codex", arms: [{ label: "A", agent: "codex", model: "gpt-x" }] });
		expect(html).toContain('data-testid="evaluate-step-2"');
		for (const m of ["score", "versions", "models", "opponent"]) expect(html).toContain(`data-testid="evaluate-mode-${m}"`);
		expect(html).toContain("Run your agent with two different models");
	});

	it("never jumps past an unready step", () => {
		const html = render("?step=4");
		expect(html).toContain('data-testid="evaluate-step-1"');
	});

	it("renders the step 3 placeholder from its lane file", () => {
		const html = render("?step=3", { agent: "codex" });
		expect(html).toContain('data-testid="evaluate-step-3');
	});
});
