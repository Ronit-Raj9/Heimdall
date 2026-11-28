import type { WizardMode } from "./evaluateDraft";
import { isBaselineAgent } from "@/app/agents/views/agentDisplay";
import { pickableAgents } from "@/components/agents/agentPicker";
import { BASELINE_AGENTS, type Capability, type EvaluationMode } from "@/lib/store/apis/evaluationsApi";
import type { AgentSummary } from "@/lib/types/bench";
import { formatUsd } from "@/lib/money";

/**
 * The one way the wizard writes an estimated cost range ("$0.04 to $0.10"), on the size cards, in step 4 and
 * in step 5, so the same estimate never reads as two numbers. Undefined when the cost is not known.
 */
export function costRangeText(c: { low: number; high: number; known?: boolean } | undefined): string | undefined {
	if (!c?.known) return undefined;
	const a = formatUsd(c.low);
	const b = formatUsd(c.high);
	return a === b ? a : `${a} to ${b}`;
}

export const EVALUATE_STEPS = [1, 2, 3, 4, 5] as const;
export type EvaluateStep = (typeof EVALUATE_STEPS)[number];

export function asEvaluateStep(raw: number | null | undefined): EvaluateStep {
	return raw && raw >= 1 && raw <= 5 ? (Math.trunc(raw) as EvaluateStep) : 1;
}

export const LEARN_MODES: WizardMode[] = ["score", "versions", "models", "model_switch", "opponent"];

const CLI_CODING = new Set(["codex", "claude-code", "claude-code-gpt", "opencode", "gemini-cli", "mini-swe-agent", "aider"]);

/** Agent summary plus the optional `capabilities` the server adds (A01). */
export type AgentWithCaps = AgentSummary & { capabilities?: Capability[] | null; builtin?: boolean };

/**
 * The agent's capabilities: the config's own list when the server sends one, else the defaults by
 * adapter, the same as the server's (CLI coding agents shell+tools+mcp+sessions, remote agents
 * tools+sessions, ...). GET /agents sends the effective list, so the fallback only covers an
 * older server.
 */
export function agentCapabilities(a: AgentWithCaps): Capability[] {
	if (a.capabilities && a.capabilities.length) return a.capabilities;
	const adapter = (a.adapter || "").toLowerCase();
	const id = a.id.toLowerCase();
	if (CLI_CODING.has(id) || id.startsWith("aider")) return ["shell", "tools", "mcp", "sessions"];
	if (["remote_http", "a2a", "openai_agent"].includes(adapter)) return ["tools", "sessions"];
	if (adapter.includes("voice") || id.includes("voice")) return ["voice", "sessions"];
	if (adapter.includes("computer") || id.includes("computer-use")) return ["desktop", "browser", "shell"];
	return ["shell", "tools"];
}

/** Built-in library agents (shipped with Heimdall) versus the org's own: the server's flag, else the baseline list My agents uses. */
export function isBuiltinAgent(a: AgentWithCaps): boolean {
	return a.builtin ?? isBaselineAgent(a);
}

/**
 * Org agents first, then built-in; alphabetical inside each group, baselines first among built-in. The agents are the
 * ones every other agent picker offers (`pickableAgents`, N97): test doubles, demo agents and broken files never.
 */
export function splitAgents(agents: readonly AgentWithCaps[], keepTests = false): { own: AgentWithCaps[]; builtin: AgentWithCaps[] } {
	const { yours, baselines } = pickableAgents(agents, keepTests);
	const usable = [...yours, ...baselines] as AgentWithCaps[];
	const byId = (x: AgentWithCaps, y: AgentWithCaps) => x.id.localeCompare(y.id);
	return {
		own: usable.filter((a) => !isBuiltinAgent(a)).sort(byId),
		builtin: usable
			.filter(isBuiltinAgent)
			.sort((x, y) => Number(!BASELINE_AGENTS.includes(x.id)) - Number(!BASELINE_AGENTS.includes(y.id)) || byId(x, y)),
	};
}

/** The highest saved version of an agent (what "Latest" runs), from GET /agents/{id}/versions. */
export function latestVersion(versions: readonly { version: number }[] | null | undefined): number | undefined {
	let max: number | undefined;
	for (const v of versions ?? []) if (max === undefined || v.version > max) max = v.version;
	return max;
}

/** Baseline agents a user can compare against, without the agent under test. */
export function opponentChoices(agents: readonly AgentWithCaps[], self: string | null): AgentWithCaps[] {
	return agents.filter((a) => BASELINE_AGENTS.includes(a.id) && a.id !== self);
}

/** Where /agents/new sends the user back after creating an agent. */
export const REGISTER_RETURN = "/evaluate";
/**
 * True when the server says some benchmark's cost is only a rough guess from list prices (G1 `estimate_basis:
 * "list_price"`, M3-28): the setup has no past runs here. An older server never sends it.
 */
export function isRoughEstimate(est: { per_benchmark?: readonly object[] | null } | undefined): boolean {
	return (est?.per_benchmark ?? []).some((b) => (b as { estimate_basis?: unknown } | null)?.estimate_basis === "list_price");
}
