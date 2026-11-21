import type { ModelRef, ProviderId } from "@/components/models/modelPicker";
import { orderSuites, suiteSections } from "@/components/suites/suiteDisplay";
import { suiteReadiness } from "@/components/suites/suiteIdentity";
import type { AgentSummary, SuiteSummary } from "@/lib/types/bench";
import type { ModelKey, ModelRefView } from "@/lib/types/models";

export const WIZARD_STEPS = [
  "suite",
  "agent",
  "model",
  "run",
  "verdict",
] as const;
export type WizardStep = (typeof WIZARD_STEPS)[number];

/** The starter suite the plan pre-selects. */
export const STARTER_SUITE = "coding-core";

/** Starter agents in the order a first-time user should see them. */
const STARTER_AGENTS = [
  "claude-code",
  "codex",
  "mini-swe",
  "plain",
  "opencode",
];

/** Run sizes offered on the Run step; 5 tasks x 1 trial is the default. */
export const RUN_SIZES = [1, 5, 10] as const;
export const DEFAULT_TASKS = 5;

/** localStorage flag: this browser has opened the wizard, so Home stops redirecting to it. */
export const WELCOME_SEEN_KEY = "heimdall.welcome.seen";

export function asStep(raw: string | null | undefined): WizardStep {
  return (WIZARD_STEPS as readonly string[]).includes(raw ?? "")
    ? (raw as WizardStep)
    : "suite";
}

type SuiteReadyFields = Pick<
  SuiteSummary,
  "error" | "task_count" | "admitted_count" | "lock_state"
>;

/** Why a suite cannot be picked, or null when it can (at least one task ready). */
export type SuiteBlock = "error" | "noTasks" | "noneReady";

/**
 * The wizard's view of a suite, with the same rule as Home and the benchmarks list: ready means every
 * task admitted and the lock current; otherwise "N of M tasks ready", pickable while at least one is.
 */
export function suiteState(suite: SuiteReadyFields): {
  ready: boolean;
  readyTasks: number;
  total: number;
  block: SuiteBlock | null;
} {
  const state = suiteReadiness(suite);
  const readyTasks = Math.max(
    0,
    Math.min(suite.admitted_count ?? 0, suite.task_count),
  );
  const block: SuiteBlock | null =
    state === "error"
      ? "error"
      : state === "noTasks"
        ? "noTasks"
        : readyTasks < 1
          ? "noneReady"
          : null;
  return {
    ready: state === "ready",
    readyTasks,
    total: suite.task_count,
    block,
  };
}

/**
 * The suite to pre-select: the first ready built-in library benchmark (Terminal-Bench 2, then
 * SWE-bench Verified, in library order), else coding-core when pickable, else the first ready one,
 * else the first pickable, else the first.
 */
export function defaultSuite(
  suites: readonly SuiteSummary[],
): string | undefined {
  const pickable = (s: SuiteSummary) => suiteState(s).block === null;
  const { builtin } = suiteSections(suites);
  return (
    builtin.find((s) => !s.error && suiteState(s).ready) ??
    suites.find((s) => s.id === STARTER_SUITE && pickable(s)) ??
    suites.find((s) => suiteState(s).ready) ??
    suites.find(pickable) ??
    suites[0]
  )?.id;
}

/** Suites sorted for the picker: built-in library first, then the org's own, then practice examples. */
export function sortSuites(suites: readonly SuiteSummary[]): SuiteSummary[] {
  return orderSuites(suites);
}

function starterRank(id: string): number {
  const i = STARTER_AGENTS.findIndex(
    (s) => id === s || id.startsWith(`${s}-`) || id.startsWith(`${s}_`),
  );
  return i < 0 ? STARTER_AGENTS.length : i;
}

/** Agents sorted for the picker: starter agents first, scripted test doubles last. */
export function sortAgents(agents: readonly AgentSummary[]): AgentSummary[] {
  return [...agents].sort(
    (a, b) =>
      Number(Boolean(a.scripted_kind)) - Number(Boolean(b.scripted_kind)) ||
      starterRank(a.id) - starterRank(b.id) ||
      a.id.localeCompare(b.id),
  );
}

/** Scripted test doubles and demo agents: hidden on the agent step unless asked for. */
const TEST_AGENT_IDS = new Set([
  "oracle",
  "noop",
  "toy",
  "broken",
  "voice-broken",
  "unsafe",
  "storm",
  "exploit",
  "refuse",
]);

export function isTestAgent(
  agent: Pick<AgentSummary, "id" | "scripted_kind">,
): boolean {
  return (
    Boolean(agent.scripted_kind) ||
    TEST_AGENT_IDS.has(agent.id) ||
    agent.id.startsWith("live-")
  );
}

/** Best guess of the gateway provider of a model name. */
export function providerForModel(model: string): ProviderId {
  const m = model.toLowerCase();
  if (m.startsWith("claude") || m.startsWith("anthropic/")) return "anthropic";
  if (m.startsWith("gemini")) return "gemini";
  return "openai";
}

/** Providers whose keys can serve a model of this provider (an OpenAI-compatible key serves OpenAI models). */
function servingProviders(p: ProviderId): ProviderId[] {
  return p === "openai" ? ["openai", "openai-compatible"] : [p];
}

/** A key works only when its last test passed and it is under budget. */
export function keyWorks(k: Pick<ModelKey, "status" | "over_budget">): boolean {
  return k.status === "ok" && !k.over_budget;
}

/** The status of a provider's keys, from GET /models: ok only when a key's last test passed. */
export type KeyState = "ok" | "untested" | "failed" | "none";

export function keyStateFor(
  keys: readonly ModelKey[],
  provider?: ProviderId,
  keyId?: string,
): KeyState {
  const mine = keyId
    ? keys.filter((k) => k.id === keyId)
    : provider
      ? keys.filter((k) => servingProviders(provider).includes(k.provider))
      : [...keys];
  if (mine.length === 0) return "none";
  if (mine.some(keyWorks)) return "ok";
  if (mine.some((k) => k.status === "untested")) return "untested";
  return "failed";
}

/**
 * The agent to pre-select: the first non-test agent whose model provider has a working key, codex
 * first when an OpenAI or OpenAI-compatible key works. No working key: codex, else the first
 * non-test agent, else the first agent.
 */
export function defaultAgent(
  agents: readonly AgentSummary[],
  keys: readonly ModelKey[] = [],
): string | undefined {
  const sorted = sortAgents(agents);
  const real = sorted.filter((a) => !isTestAgent(a));
  const codex = real.find((a) => a.id === "codex");
  const works = (a: AgentSummary) =>
    keyStateFor(keys, providerForModel(a.model ?? "")) === "ok";
  if (codex && keyStateFor(keys, "openai") === "ok") return codex.id;
  return (real.find(works) ?? codex ?? real[0] ?? sorted[0])?.id;
}

/** Picked first when a working key reports it. */
export const PREFERRED_MODEL = "gpt-6.2-sol";

/**
 * The model to pre-select for an agent: its own model when a working key reports it, else the first
 * model a working key reports (the preferred one when present), else the server default, else the
 * agent's own model.
 */
export function defaultModel(
  agent: Pick<AgentSummary, "model"> | undefined,
  keys: readonly ModelKey[],
  serverDefault?: ModelRefView,
): ModelRef | null {
  const own = agent?.model ?? "";
  const ownProvider = own ? providerForModel(own) : undefined;
  const working = keys.filter(keyWorks);
  const served = (k: ModelKey) => [...(k.models_available ?? []), ...k.models];
  const ordered = ownProvider
    ? [...working].sort(
        (a, b) =>
          Number(servingProviders(ownProvider).includes(b.provider)) -
          Number(servingProviders(ownProvider).includes(a.provider)),
      )
    : working;
  if (own) {
    const k = ordered.find((x) => served(x).includes(own));
    if (k) return { provider: k.provider, model: own, keyId: k.id };
  }
  const pref = ordered.find((x) => served(x).includes(PREFERRED_MODEL));
  if (pref)
    return { provider: pref.provider, model: PREFERRED_MODEL, keyId: pref.id };
  const first = ordered.find((x) => served(x).length > 0);
  if (first)
    return {
      provider: first.provider,
      model: served(first)[0],
      keyId: first.id,
    };
  if (serverDefault)
    return {
      provider: serverDefault.provider,
      model: serverDefault.model,
      keyId: serverDefault.key_id,
    };
  return own && ownProvider ? { provider: ownProvider, model: own } : null;
}

/** The run size actually used: never more tasks than the suite has (when it reports a count). */
export function clampTasks(
  tasks: number,
  suite: SuiteSummary | undefined,
): number {
  if (!suite || suite.task_count <= 0) return tasks;
  return Math.max(1, Math.min(tasks, suite.task_count));
}

export function markWelcomeSeen(): void {
  try {
    window.localStorage.setItem(WELCOME_SEEN_KEY, "1");
  } catch {
    // Private window or blocked storage: Home may offer the wizard again, nothing breaks.
  }
}

export function welcomeSeen(): boolean {
  try {
    return window.localStorage.getItem(WELCOME_SEEN_KEY) === "1";
  } catch {
    return true;
  }
}

/** An HTTP status from an RTK Query error, when there is one. */
export function errorStatusOf(err: unknown): number | undefined {
  const s = (err as { status?: unknown } | undefined)?.status;
  return typeof s === "number" ? s : undefined;
}

/** The server's error message from an RTK Query error, or a fallback. */
export function errorMessageOf(err: unknown, fallback: string): string {
  const data = (err as { data?: unknown } | undefined)?.data as
    { message?: unknown; error?: { message?: unknown } } | undefined;
  const m = data?.message ?? data?.error?.message;
  return typeof m === "string" && m ? m : fallback;
}

/** Benchmark families the wizard groups suites into, in display order. */
export const SUITE_CATEGORIES = [
  "coding",
  "terminal",
  "memory",
  "computer",
  "browser",
  "voice",
  "support",
  "research",
  "safety",
  "multiagent",
  "tools",
] as const;
export type SuiteCategory = (typeof SUITE_CATEGORIES)[number];

const TRACK_CATEGORY: Record<string, SuiteCategory> = {
  coding: "coding",
  artifact: "coding",
  skills: "coding",
  terminal: "terminal",
  memory: "memory",
  computer_use: "computer",
  browser: "browser",
  support: "support",
  qa: "research",
  safety: "safety",
  tool_use: "tools",
  mcp: "tools",
};

const KEYWORD_CATEGORY: [RegExp, SuiteCategory][] = [
  [/voice|speech|audio|stt|tts/, "voice"],
  [/multi[-_ ]?agent|swarm|team/, "multiagent"],
  [/research|search|deep[-_ ]?dive/, "research"],
  [/memory|recall|longmem/, "memory"],
  [/browser|web[-_ ]?arena|webshop/, "browser"],
  [/computer|desktop|osworld|gui/, "computer"],
  [/support|customer|helpdesk|tau/, "support"],
  [/safety|jailbreak|red[-_ ]?team|harm/, "safety"],
  [/terminal|shell|bash|cli/, "terminal"],
];

/** The benchmark family of a suite: obvious words in its id or description first, then its track. */
export function categoryOf(
  suite: Pick<SuiteSummary, "id" | "description" | "track">,
): SuiteCategory {
  const text = `${suite.id} ${suite.description ?? ""}`.toLowerCase();
  const strong = KEYWORD_CATEGORY.find(([re]) =>
    re.test(suite.id.toLowerCase()),
  );
  if (strong) return strong[1];
  if (suite.track && suite.track !== "coding" && TRACK_CATEGORY[suite.track])
    return TRACK_CATEGORY[suite.track];
  const weak = KEYWORD_CATEGORY.find(([re]) => re.test(text));
  return (
    weak?.[1] ??
    (suite.track ? (TRACK_CATEGORY[suite.track] ?? "coding") : "coding")
  );
}

/** "coding-core" as "Coding core". */
export function prettyName(id: string): string {
  const s = id.replace(/[-_]+/g, " ").trim();
  return s ? s[0].toUpperCase() + s.slice(1) : id;
}

/** Runs of a wizard test: every task runs once in each of the two setups (A and B). */
export function wizardRuns(tasks: number, trials = 1): number {
  return tasks * trials * 2;
}

/** Two-letter badge text of a name: "claude-code" as "CC", "codex" as "CO". */
export function initialsOf(name: string): string {
  const parts = name.split(/[-_ /.]+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return name.slice(0, 2).toUpperCase();
}
