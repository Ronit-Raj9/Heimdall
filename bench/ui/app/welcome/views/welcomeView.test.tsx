import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { SessionInfo } from "@/lib/session/sessionContext";
import { LOCAL_SESSION } from "@/lib/session/sessionContext";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { beforeEach, describe, expect, it, vi } from "vitest";
import WelcomeView, { RunStep } from "./welcomeView";
import {
  asStep,
  clampTasks,
  defaultAgent,
  defaultModel,
  defaultSuite,
  isTestAgent,
  keyStateFor,
  suiteState,
  wizardRuns,
} from "./welcome.utils";

const q = vi.hoisted(() => ({
  suites: {} as Record<string, unknown>,
  agents: {} as Record<string, unknown>,
  exp: {} as Record<string, unknown>,
  verdict: {} as Record<string, unknown>,
  health: {} as Record<string, unknown>,
  models: {} as Record<string, unknown>,
}));

const ok = (data: unknown) => ({
  data,
  error: undefined,
  isLoading: false,
  isFetching: false,
  isSuccess: true,
  refetch: () => undefined,
});

vi.mock(
  "@/components/models/modelPicker",
  async () => await import("@/lib/testing/modelPickerStub"),
);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  useGetSuitesQuery: () => q.suites,
  useGetAgentsQuery: () => q.agents,
  useGetExperimentQuery: () => q.exp,
  useGetVerdictQuery: () => q.verdict,
  useGetHealthQuery: () => q.health,
}));
vi.mock("@/lib/store/apis/modelsApi", () => ({
  useGetModelsQuery: () => q.models,
}));
vi.mock("@/lib/store/apis/welcomeApi", () => ({
  useWelcomeQuickRunMutation: () => [
    () => Promise.resolve({}),
    { isLoading: false, error: undefined, reset: () => undefined },
  ],
}));
vi.mock("@/components/verdict/verdictCard", () => ({
  VerdictCard: () => <div data-testid="verdict-card" />,
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({
    to,
    children,
    ...rest
  }: {
    to: string;
    children: React.ReactNode;
  }) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
  useRouterState: () => "/welcome",
}));

const suite = (
  id: string,
  task_count = 10,
  extra: Record<string, unknown> = {},
) => ({
  id,
  version: "1.0.0",
  description: `${id} tasks`,
  task_count,
  admitted_count: task_count,
  lock_state: "locked",
  ...extra,
});
const key = (
  id: string,
  provider: string,
  status: string,
  models_available: string[] = [],
) => ({
  id,
  provider,
  status,
  models: [],
  models_available,
  over_budget: false,
});
const agent = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  adapter: "command",
  model: "gpt-x",
  scripted_kind: "",
  ...extra,
});

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
  q.agents = ok({
    agents: [agent("toy", { scripted_kind: "noop" }), agent("claude-code")],
  });
  q.exp = ok(undefined);
  q.verdict = ok(undefined);
  q.health = ok({ launch: true });
  q.models = ok({ providers: [], keys: [] });
});

describe("WelcomeView", () => {
  it("opens on the suite step with the starter suite first and selected", () => {
    const html = view("?suite=coding-core");
    expect(html).toContain('data-testid="welcome-step-suite"');
    expect(html.indexOf("welcome-suite-coding-core")).toBeLessThan(
      html.indexOf("welcome-suite-acme-web"),
    );
    expect(html).toMatch(
      /data-testid="welcome-suite-coding-core" data-selected="true"/,
    );
    expect(html).toContain("Recommended");
    expect(html).toContain("Ready");
    expect(html).toContain("Coding basics");
    expect(html).toContain('data-testid="welcome-catalog-tile"');
  });

  it("uses the Home readiness rule on suite tiles", () => {
    q.suites = ok({
      suites: [
        suite("coding-core"),
        suite("acme-web", 10, { admitted_count: 3 }),
        suite("acme-team", 4, { admitted_count: 0, lock_state: "missing" }),
      ],
    });
    const html = view("?suite=coding-core");
    expect(html).toContain("3 of 10 tasks ready");
    expect(html).toContain("No task is ready yet");
    expect(html).toMatch(
      /disabled=""[^>]*data-testid="welcome-suite-acme-team"/,
    );
    expect(html).not.toMatch(
      /disabled=""[^>]*data-testid="welcome-suite-acme-web"/,
    );
  });

  it("hides test agents behind a link", () => {
    q.agents = ok({
      agents: [
        agent("oracle"),
        agent("live-x"),
        agent("toy", { scripted_kind: "noop" }),
        agent("codex"),
      ],
    });
    const html = view("?suite=coding-core&agent=codex&step=agent");
    expect(html).toContain("welcome-agent-codex");
    expect(html).not.toContain("welcome-agent-oracle");
    expect(html).not.toContain("welcome-agent-live-x");
    expect(html).toContain("Show test agents");
  });

  it("shows the real key status on the model step", () => {
    const step = "?suite=coding-core&agent=claude-code&step=model";
    expect(view(step)).toContain("Add a model key first");
    q.models = ok({ providers: [], keys: [key("k", "openai", "untested")] });
    let html = view(step);
    expect(html).toContain("Key not tested: press Test on Models &amp; keys");
    expect(html).toContain('href="/models"');
    expect(html).not.toContain("Key works");
    q.models = ok({ providers: [], keys: [key("k", "openai", "invalid")] });
    expect(view(step)).toContain("Key not working");
    q.models = ok({ providers: [], keys: [key("k", "openai", "ok")] });
    html = view(step);
    expect(html).toContain("Key works");
    expect(html).not.toContain("welcome-model-quick");
  });

  it("explains the run count and never invents a price", () => {
    const html = render(
      <RunStep
        suiteId="coding-core"
        agentId="codex"
        model={{ provider: "openai", model: "gpt-6.2-sol" }}
        tasks={5}
        onTasks={() => undefined}
        onBack={() => undefined}
        onStarted={() => undefined}
      />,
      LOCAL_SESSION,
    );
    expect(html).toContain("5 tasks x 2 setups (A and B) = 10 runs");
    expect(html).toContain("Cost: not known (this model has no price set)");
    expect(html).not.toContain("$");
    expect(html).toContain("Coding basics");
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
    q.exp = ok({
      exp_id: "e_1",
      state: "running",
      slots: { total: 5, done: 1, running: 1, failed: 0 },
    });
    let html = view("?suite=coding-core&agent=claude-code&exp=e_1");
    expect(html).toContain('data-testid="welcome-step-verdict"');
    expect(html).toContain('data-state="running"');
    expect(html).not.toContain("welcome-verdict-headline");
    expect(html).toContain('data-testid="welcome-live-slots"');
    expect(html).toContain("1 running now, 1 finished");

    q.exp = ok({
      exp_id: "e_1",
      state: "done",
      slots: { total: 5, done: 5, running: 0, failed: 0 },
    });
    q.verdict = ok({
      exp_id: "e_1",
      verdict: { decision: "improved" },
      naive: {
        A: { num: 2, den: 5, unknown: 0 },
        B: { num: 4, den: 5, unknown: 0 },
      },
    });
    html = view("?suite=coding-core&agent=claude-code&exp=e_1");
    expect(html).toContain("B is better");
    expect(html).toContain("4 of 5 runs solved the task");
    expect(html).toContain("Try another model");
    expect(html).toContain('data-testid="welcome-arm-bars"');
    expect(html).toContain("80%");
    expect(html).toContain('href="/experiments/$expId"');
  });
});

describe("welcome utils", () => {
  it("picks sensible defaults", () => {
    expect(
      defaultSuite([suite("a", 0), suite("b"), suite("coding-core")] as never),
    ).toBe("coding-core");
    expect(defaultSuite([suite("a", 0), suite("b")] as never)).toBe("b");
    expect(
      defaultAgent([
        agent("toy", { scripted_kind: "noop" }),
        agent("zzz"),
        agent("codex"),
      ] as never),
    ).toBe("codex");
    expect(wizardRuns(5)).toBe(10);
    expect(asStep("bogus")).toBe("suite");
    expect(clampTasks(10, suite("x", 3) as never)).toBe(3);
    expect(clampTasks(5, undefined)).toBe(5);
  });
});

describe("wizard defaults", () => {
  const claude = agent("claude-code", { model: "claude-sonnet-4-5" });
  const codex = agent("codex", { model: "gpt-5" });

  it("picks an agent whose key works, codex first for OpenAI keys", () => {
    const agents = [claude, codex, agent("oracle")] as never;
    expect(
      defaultAgent(agents, [key("o", "openai-compatible", "ok")] as never),
    ).toBe("codex");
    expect(defaultAgent(agents, [key("a", "anthropic", "ok")] as never)).toBe(
      "claude-code",
    );
    expect(
      defaultAgent(agents, [key("a", "anthropic", "untested")] as never),
    ).toBe("codex");
    expect(defaultAgent(agents, [] as never)).toBe("codex");
    expect(isTestAgent({ id: "live-demo", scripted_kind: "" } as never)).toBe(
      true,
    );
    expect(isTestAgent({ id: "codex", scripted_kind: "" } as never)).toBe(
      false,
    );
  });

  it("picks a model the key reports", () => {
    const keys = [
      key("o", "openai-compatible", "ok", ["gpt-6.1", "gpt-6.2-sol"]),
    ] as never;
    expect(defaultModel(codex as never, keys)).toEqual({
      provider: "openai-compatible",
      model: "gpt-6.2-sol",
      keyId: "o",
    });
    expect(
      defaultModel(agent("codex", { model: "gpt-6.1" }) as never, keys)?.model,
    ).toBe("gpt-6.1");
    expect(
      defaultModel(
        codex as never,
        [key("o", "openai", "ok", ["zeta"])] as never,
      )?.model,
    ).toBe("zeta");
    expect(
      defaultModel(codex as never, [], {
        provider: "groq",
        model: "llama",
        key_id: "g",
      }),
    ).toEqual({ provider: "groq", model: "llama", keyId: "g" });
    expect(defaultModel(codex as never, [])).toEqual({
      provider: "openai",
      model: "gpt-5",
    });
  });

  it("reads key and suite status", () => {
    expect(
      keyStateFor([key("o", "openai", "untested")] as never, "openai"),
    ).toBe("untested");
    expect(keyStateFor([key("o", "openai", "error")] as never, "openai")).toBe(
      "failed",
    );
    expect(keyStateFor([] as never, "openai")).toBe("none");
    expect(
      suiteState(
        suite("x", 10, { admitted_count: 10, lock_state: "stale" }) as never,
      ),
    ).toMatchObject({ ready: false, readyTasks: 10, block: null });
    expect(suiteState(suite("x", 0) as never).block).toBe("noTasks");
    expect(
      suiteState(suite("x", 3, { admitted_count: 0 }) as never).block,
    ).toBe("noneReady");
    expect(
      defaultSuite([
        suite("coding-core", 3, { admitted_count: 0 }),
        suite("b"),
      ] as never),
    ).toBe("b");
  });
});
