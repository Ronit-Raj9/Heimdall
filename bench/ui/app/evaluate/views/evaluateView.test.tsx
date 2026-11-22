import "@/lib/i18n";

import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { applyPatch, armsFor, draftToSpec, EMPTY_DRAFT, learnStepReady, type EvaluateDraft } from "./evaluateDraft";
import { agentCapabilities, splitAgents, type AgentWithCaps } from "./evaluate.utils";
import EvaluateView, { LinkNotice, maxReachable, planLink, stepReady, stepWriteBack } from "./evaluateView";
import { EvaluateDraftProvider } from "./evaluateDraft";
import { Step1Agent } from "./step1Agent";

const est = vi.hoisted(() => ({ current: { isLoading: true, isFetching: true, data: undefined } as Record<string, unknown> }));
const agent = (id: string, extra: Partial<AgentWithCaps> = {}) =>
	({ id, adapter: "command", model: "gpt-x", scripted_kind: "", ...extra }) as AgentWithCaps;
const agents = [
	agent("codex"),
	agent("my-bot", { adapter: "remote_http" }),
	agent("oracle", { scripted_kind: "oracle" }),
	agent("opencode"),
];

vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...(await orig<object>()),
	useGetAgentsQuery: () => ({ isLoading: false, isError: false, data: { agents }, refetch: () => undefined }),
	useGetSuitesQuery: () => ({
		isLoading: false,
		data: { suites: [{ id: "hd-harbor-tb2", description: "", admitted_count: 80, builtin: true }] },
	}),
}));
vi.mock("@/lib/store/apis/evaluationsApi", async (orig) => ({
	...(await orig<object>()),
	BASELINE_AGENTS: ["codex", "claude-code", "mini-swe-agent", "opencode", "gemini-cli"],
	useGetEvaluationPacksQuery: () => ({ isLoading: false, data: [] }),
	useGetAgentCompatibilityQuery: () => ({ isLoading: false, data: undefined }),
	useGetAgentVersionsQuery: () => ({
		isLoading: false,
		data: {
			agent: "codex",
			versions: [
				{ version: 1, runs: 3 },
				{ version: 2, runs: 0 },
			],
		},
	}),
	useEstimateEvaluationQuery: () => est.current,
	useGetBenchFaultsQuery: () => ({ isLoading: true, data: undefined }),
}));
vi.mock("@/lib/store/apis/modelsApi", async (orig) => ({
	...(await orig<object>()),
	useGetModelsQuery: () => ({ isLoading: false, data: { providers: [], keys: [], keys_ready: true } }),
}));
vi.mock("@/lib/store/apis/referenceApi", async (orig) => ({
	...(await orig<object>()),
	useGetReferenceBoardQuery: () => ({ isLoading: false, data: undefined }), // Y09: no published reference scores
}));
vi.mock("@/components/redteam/redTeamPanel", () => ({
	RedTeamPanel: () => null, // Z04 panel needs the store; covered by its own test
}));
vi.mock("./ownTasks", () => ({
	OwnTasksCard: () => null, // Y13 own-tasks card needs the store; covered by its own test
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
		d = applyPatch(d, {
			arms: [
				{ ...d.arms[0], model: "m1" },
				{ ...d.arms[1], model: "m2" },
			],
		});
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
		expect(spec.tasks_per_benchmark).toBe(10);
		expect(spec.trials).toBe(1);
		expect(spec.name).toBe("codex failure test on hd-harbor-tb2");
	});

	it("sends bifrost_cache in score mode only", () => {
		expect(draftToSpec({ ...withAgent(), bifrostCache: true }).bifrost_cache).toBe(true);
		expect(draftToSpec({ ...withAgent(), bifrostCache: true, mode: "models" }).bifrost_cache).toBeUndefined();
		expect(draftToSpec(withAgent()).bifrost_cache).toBeUndefined();
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
		expect(agentCapabilities(agent("codex"))).toEqual(["shell", "tools", "mcp", "sessions"]);
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

	it("renders step 3 (benchmarks) from its lane file", () => {
		const html = render("?step=3", { agent: "codex" });
		expect(html).toContain('data-testid="step3-benchmarks"');
	});

	it("keeps Next off on step 4 when nothing can run, and says why", () => {
		const ready = { agent: "codex", arms: [{ label: "A", agent: "codex", model: "gpt-x" }], benchmarks: ["hd-harbor-bfcl"] };
		const reason = "hd-harbor-bfcl: this benchmark runs inside each task's own environment";
		expect(stepReady(4, applyPatch(withAgent(), ready))).toBe(true);
		expect(stepReady(4, applyPatch(withAgent(), ready), new Set(), reason)).toBe(false);
		est.current = {
			isLoading: false,
			isFetching: false,
			isError: false,
			data: {
				runs: 0,
				est_cost_usd: { low: 0, high: 0, known: true },
				est_minutes: { low: 0, high: 0 },
				per_benchmark: [{ benchmark: "hd-harbor-bfcl", runs: 0, skipped: "this benchmark runs inside each task's own environment" }],
			},
		};
		const html = render("?step=4", ready);
		expect(html).toContain('data-testid="step4-setup"');
		expect(html).toMatch(/data-testid="evaluate-next"[^>]*disabled=""/);
		expect(html).toContain("Nothing can run with this setup: hd-harbor-bfcl: this benchmark runs inside each task&#x27;s own environment");
		est.current = {
			...est.current,
			data: { runs: 4, est_cost_usd: { low: 0, high: 0, known: true }, est_minutes: { low: 1, high: 2 }, per_benchmark: [] },
		};
		const ok = render("?step=4", ready);
		expect(ok).not.toMatch(/data-testid="evaluate-next"[^>]*disabled=""/);
		expect(ok).not.toContain("Nothing can run");
	});
});
describe("deep links (N154, N112, N86, N85)", () => {
	const listed = [
		agent("zz-test-loop", { model: "gpt-5.4-mini" }),
		agent("claude-code", { model: "claude-sonnet-4-5" }),
		agent("codex", { model: "gpt-5.6-terra" }),
	];
	const stored = applyPatch(EMPTY_DRAFT, {
		agent: "zz-test-loop",
		arms: [{ label: "A", agent: "zz-test-loop", model: "gpt-5.4-mini" }],
		benchmarks: ["hd-harbor-skills"],
	});
	const link = (p: Partial<Parameters<typeof planLink>[0]>) => ({ agent: null, mode: null, suite: null, opponent: null, keep: null, ...p });

	it("starts a new draft in versions mode and puts the stored one aside", () => {
		const plan = planLink(link({ agent: "zz-test-loop", mode: "versions" }), stored, listed, null);
		if (plan.kind !== "replace") throw new Error(`plan ${plan.kind}`);
		expect(plan.draft.mode).toBe("versions");
		expect(plan.draft.benchmarks).toEqual([]);
		expect(plan.putAside).toBe(stored); // Go back to my draft restores exactly this
		expect(plan.step).toBe(2);
		expect(plan.badMode).toBeUndefined();
	});

	it("gives a linked agent its own model and only the linked benchmark", () => {
		const plan = planLink(link({ agent: "claude-code", suite: "zz-test-mcp" }), stored, listed, null);
		if (plan.kind !== "replace") throw new Error(`plan ${plan.kind}`);
		expect(plan.draft.arms).toEqual([{ label: "A", agent: "claude-code", model: "claude-sonnet-4-5" }]);
		expect(plan.draft.benchmarks).toEqual(["zz-test-mcp"]);
		expect(plan.step).toBe(3);
	});

	it("keep=1 (back from Register your agent) patches only the agent", () => {
		const plan = planLink(link({ agent: "codex", keep: "1" }), stored, listed, null);
		if (plan.kind !== "patch") throw new Error(`plan ${plan.kind}`);
		const d = applyPatch(stored, plan.patch);
		expect(d.agent).toBe("codex");
		expect(d.benchmarks).toEqual(["hd-harbor-skills"]);
		expect(d.arms[0].model).toBe("gpt-5.6-terra");
	});

	it("waits for the agents list, then reports an unknown agent and leaves the draft alone", () => {
		expect(planLink(link({ agent: "zz-no-such-agent" }), stored, undefined, null).kind).toBe("wait");
		expect(planLink(link({ agent: "zz-no-such-agent" }), stored, listed, null)).toEqual({ kind: "missing", id: "zz-no-such-agent" });
		expect(planLink(link({}), stored, listed, null).kind).toBe("none");
	});

	it("says which agent is missing on step 1 and keeps Next off", () => {
		const html = renderToStaticMarkup(
			<EvaluateDraftProvider initial={{ ...EMPTY_DRAFT }}>
				<Step1Agent missingAgent="zz-no-such-agent" />
			</EvaluateDraftProvider>,
		);
		expect(html).toContain('data-testid="evaluate-agent-missing"');
		expect(html).toContain("No agent is called zz-no-such-agent. Pick one below, or register it.");
		const d = applyPatch(EMPTY_DRAFT, { agent: "codex", arms: [{ label: "A", agent: "codex", model: "gpt-x" }] });
		expect(stepReady(1, d, new Set(), null, { missingAgent: "zz-no-such-agent" })).toBe(false);
		expect(stepReady(1, applyPatch(EMPTY_DRAFT, { agent: "zz-no-such-agent" }), new Set(), null, { known: new Set(["codex"]) })).toBe(
			false,
		);
		expect(render("", { agent: "zz-no-such-agent" })).toMatch(/data-testid="evaluate-next"[^>]*disabled=""/);
	});

	it("shows the put-aside note with Go back to my draft and the unknown mode", () => {
		const html = renderToStaticMarkup(<LinkNotice putAside badMode="duel" onBack={() => undefined} />);
		expect(html).toContain("Started a new evaluation from the link. Your earlier draft was put aside.");
		expect(html).toContain('data-testid="evaluate-link-back"');
		expect(html).toContain("Go back to my draft");
		expect(html).toContain("The link asked for an unknown comparison (duel), so this starts with Its score.");
	});

	it("writes the shown step back to ?step= once settled (N85)", () => {
		expect(stepWriteBack(5, 1, true)).toBeNull();
		expect(stepWriteBack(5, 3, true)).toBe(3);
		expect(stepWriteBack(5, 1, false)).toBeUndefined();
		expect(stepWriteBack(4, 4, true)).toBeUndefined();
		expect(stepWriteBack(null, 1, true)).toBeUndefined();
		// After the write-back the URL has no step: a ready draft no longer jumps ahead by itself.
		expect(render("", { agent: "codex", arms: [{ label: "A", agent: "codex", model: "gpt-x" }] })).toContain(
			'data-testid="evaluate-step-1"',
		);
	});

	it("keeps ?agent=codex&suite=hd-harbor-bfcl&step=4 on step 4", () => {
		const plan = planLink(link({ agent: "codex", suite: "hd-harbor-bfcl" }), EMPTY_DRAFT, listed, 4);
		if (plan.kind !== "replace") throw new Error(`plan ${plan.kind}`);
		expect(plan.step).toBeNull(); // the explicit step stays
		expect(plan.putAside).toBeNull();
		const reach = maxReachable(plan.draft);
		expect(reach).toBeGreaterThanOrEqual(4);
		expect(stepWriteBack(4, Math.min(4, reach) as 4, true)).toBeUndefined();
	});
});

describe("step gates (N89, N95)", () => {
	it("blocks step 4 while failover is on without a fallback and says why", () => {
		const ready = {
			agent: "codex",
			arms: [{ label: "A", agent: "codex", model: "gpt-x", provider: "openai" }],
			benchmarks: ["hd-harbor-bfcl"],
			failover: { enabled: true, fallbacks: [] },
		};
		expect(stepReady(4, applyPatch(withAgent(), ready))).toBe(false);
		est.current = {
			isLoading: false,
			isFetching: false,
			isError: false,
			data: { runs: 4, est_cost_usd: { low: 0, high: 0, known: true }, est_minutes: { low: 1, high: 2 }, per_benchmark: [] },
		};
		const html = render("?step=4", ready as Partial<EvaluateDraft>);
		expect(html).toMatch(/data-testid="evaluate-next"[^>]*disabled=""/);
		expect(html).toMatch(/data-testid="evaluate-next-hint"[^>]*>Pick a fallback model\./);
	});

	it("blocks Latest against the latest version in versions mode", () => {
		const d = applyPatch(withAgent(), { mode: "versions", compareVersion: 2 });
		expect(stepReady(2, d, new Set(), null, { latest: 2 })).toBe(false);
		expect(stepReady(2, applyPatch(d, { compareVersion: 1 }), new Set(), null, { latest: 2 })).toBe(true);
		const html = render("?step=2", { agent: "codex", mode: "versions", compareVersion: 2, arms: d.arms });
		expect(html).toContain('data-testid="evaluate-step-2"');
		expect(html).toMatch(/data-testid="evaluate-next"[^>]*disabled=""/);
		expect(html).toContain("Pick a different version than the one in step 1.");
		expect(html).toContain("v2 (0 runs) (Setup A)");
		expect(html).toContain("codex latest (v2)");
	});
});