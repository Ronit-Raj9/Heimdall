import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { SessionInfo } from "@/lib/session/sessionContext";
import { LOCAL_SESSION } from "@/lib/session/sessionContext";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { beforeEach, describe, expect, it, vi } from "vitest";
import WelcomeView from "./welcomeView";
import { asStep, clampTasks, defaultAgent, defaultSuite } from "./welcome.utils";

const q = vi.hoisted(() => ({
	suites: {} as Record<string, unknown>,
	agents: {} as Record<string, unknown>,
	exp: {} as Record<string, unknown>,
	verdict: {} as Record<string, unknown>,
	health: {} as Record<string, unknown>,
	models: {} as Record<string, unknown>,
}));

const ok = (data: unknown) => ({ data, error: undefined, isLoading: false, isFetching: false, isSuccess: true, refetch: () => undefined });

vi.mock("@/components/models/modelPicker", async () => await import("@/lib/testing/modelPickerStub"));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<object>()),
	useGetSuitesQuery: () => q.suites,
	useGetAgentsQuery: () => q.agents,
	useGetExperimentQuery: () => q.exp,
	useGetVerdictQuery: () => q.verdict,
	useGetHealthQuery: () => q.health,
}));
vi.mock("@/lib/store/apis/modelsApi", () => ({ useGetModelsQuery: () => q.models }));
vi.mock("@/lib/store/apis/welcomeApi", () => ({
	useWelcomeQuickRunMutation: () => [() => Promise.resolve({}), { isLoading: false, error: undefined, reset: () => undefined }],
}));
vi.mock("@/components/verdict/verdictCard", () => ({ VerdictCard: () => <div data-testid="verdict-card" /> }));
vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, children, ...rest }: { to: string; children: React.ReactNode }) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
	useRouterState: () => "/welcome",
}));

const suite = (id: string, task_count = 10) => ({ id, version: "1.0.0", description: `${id} tasks`, task_count });
const agent = (id: string, extra: Record<string, unknown> = {}) => ({ id, adapter: "command", model: "gpt-x", scripted_kind: "", ...extra });

function view(search: string, session: SessionInfo = LOCAL_SESSION) {
	return render(
		<NuqsTestingAdapter searchParams={search}>
			<WelcomeView />
		</NuqsTestingAdapter>,
		session,
	);
}

beforeEach(() => {
	q.suites = ok({ suites: [suite("acme-web"), suite("coding-core")] });
	q.agents = ok({ agents: [agent("toy", { scripted_kind: "noop" }), agent("claude-code")] });
	q.exp = ok(undefined);
	q.verdict = ok(undefined);
	q.health = ok({ launch: true });
	q.models = ok({ providers: [], keys: [] });
});

describe("WelcomeView", () => {
	it("opens on the suite step with the starter suite first and selected", () => {
		const html = view("?suite=coding-core");
		expect(html).toContain('data-testid="welcome-step-suite"');
		expect(html.indexOf("welcome-suite-coding-core")).toBeLessThan(html.indexOf("welcome-suite-acme-web"));
		expect(html).toMatch(/data-testid="welcome-suite-coding-core" data-selected="true"/);
		expect(html).toContain("Recommended");
		expect(html).toContain("Ready");
		expect(html).toContain('data-testid="welcome-catalog-tile"');
	});

	it("shows the run summary with a cost estimate", () => {
		q.models = ok({ providers: [{ id: "openai", name: "OpenAI", models: [] }], keys: [] });
		const html = view("?suite=coding-core&agent=claude-code&step=model");
		expect(html).toContain('data-testid="welcome-provider-openai"');
		expect(html).toContain("No key");
	});

	it("offers the catalog when the org has no suite", () => {
		q.suites = ok({ suites: [] });
		const html = view("");
		expect(html).toContain('data-testid="welcome-suites-empty"');
		expect(html).toContain('href="/catalog"');
	});

	it("offers a template when the org has no agent", () => {
		q.agents = ok({ agents: [] });
		const html = view("?suite=coding-core&step=agent");
		expect(html).toContain('data-testid="welcome-agents-empty"');
		expect(html).toContain('href="/agents/new"');
	});

	it("goes back to the model step when the run step has no model (reload)", () => {
		const html = view("?suite=coding-core&agent=claude-code&step=run");
		expect(html).toContain('data-testid="welcome-step-model"');
		expect(html).toContain("Choose a model to continue.");
	});

	it("shows live progress, then the verdict on the same page", () => {
		q.exp = ok({ exp_id: "e_1", state: "running", slots: { total: 5, done: 1, running: 1, failed: 0 } });
		let html = view("?suite=coding-core&agent=claude-code&exp=e_1");
		expect(html).toContain('data-testid="welcome-step-verdict"');
		expect(html).toContain('data-state="running"');
		expect(html).not.toContain("welcome-verdict-headline");
		expect(html).toContain('data-testid="welcome-live-slots"');
		expect(html).toContain("1 running now, 1 finished");

		q.exp = ok({ exp_id: "e_1", state: "done", slots: { total: 5, done: 5, running: 0, failed: 0 } });
		q.verdict = ok({ exp_id: "e_1", verdict: { decision: "improved" }, naive: { A: { num: 2, den: 5, unknown: 0 }, B: { num: 4, den: 5, unknown: 0 } } });
		html = view("?suite=coding-core&agent=claude-code&exp=e_1");
		expect(html).toContain("Better than the baseline");
		expect(html).toContain("4 of 5 runs passed");
		expect(html).toContain("Compare another model");
		expect(html).toContain('data-testid="welcome-arm-bars"');
		expect(html).toContain("80%");
		expect(html).toContain('href="/experiments/$expId"');
	});
});

describe("welcome utils", () => {
	it("picks sensible defaults", () => {
		expect(defaultSuite([suite("a", 0), suite("b"), suite("coding-core")] as never)).toBe("coding-core");
		expect(defaultSuite([suite("a", 0), suite("b")] as never)).toBe("b");
		expect(defaultAgent([agent("toy", { scripted_kind: "noop" }), agent("zzz"), agent("codex")] as never)).toBe("codex");
		expect(asStep("bogus")).toBe("suite");
		expect(clampTasks(10, suite("x", 3) as never)).toBe(3);
		expect(clampTasks(5, undefined)).toBe(5);
	});
});
