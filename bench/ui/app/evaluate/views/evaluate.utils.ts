import { BASELINE_AGENTS, type Capability, type EvaluationMode } from "@/lib/store/apis/evaluationsApi";
import type { AgentSummary } from "@/lib/types/bench";

export const EVALUATE_STEPS = [1, 2, 3, 4, 5] as const;
export type EvaluateStep = (typeof EVALUATE_STEPS)[number];

export function asEvaluateStep(raw: number | null | undefined): EvaluateStep {
	return raw && raw >= 1 && raw <= 5 ? (Math.trunc(raw) as EvaluateStep) : 1;
}

export const LEARN_MODES: EvaluationMode[] = ["score", "versions", "models", "opponent"];

const CLI_CODING = new Set(["codex", "claude-code", "claude-code-gpt", "opencode", "gemini-cli", "mini-swe-agent", "aider"]);

/** Agent summary plus the optional `capabilities` the server adds (A01). */
export type AgentWithCaps = AgentSummary & { capabilities?: Capability[] | null; builtin?: boolean };

/**
 * The agent's capabilities: the config's own list when the server sends one, else the defaults by
 * adapter from the contract (CLI coding agents shell+tools+mcp, remote agents tools+sessions, ...).
 */
export function agentCapabilities(a: AgentWithCaps): Capability[] {
	if (a.capabilities && a.capabilities.length) return a.capabilities;
	const adapter = (a.adapter || "").toLowerCase();
	const id = a.id.toLowerCase();
	if (CLI_CODING.has(id) || id.startsWith("aider")) return ["shell", "tools", "mcp"];
	if (["remote_http", "a2a", "openai_agent"].includes(adapter)) return ["tools", "sessions"];
	if (adapter.includes("voice") || id.includes("voice")) return ["voice", "sessions"];
	if (adapter.includes("computer") || id.includes("computer-use")) return ["desktop", "browser", "shell"];
	return ["shell", "tools"];
}

/** Scripted doubles and demo agents: not useful to evaluate, hidden from step 1. */
const TEST_IDS = new Set(["oracle", "noop", "toy", "refuse", "exploit", "subagents-control", "team-control", "mcploop"]);

export function isTestAgent(a: Pick<AgentSummary, "id" | "scripted_kind">): boolean {
	return Boolean(a.scripted_kind) || TEST_IDS.has(a.id) || a.id.startsWith("live-");
}

/** Built-in library agents (shipped with Heimdall) versus the org's own. */
export function isBuiltinAgent(a: AgentWithCaps): boolean {
	if (typeof a.builtin === "boolean") return a.builtin;
	return BASELINE_AGENTS.includes(a.id) || a.id.startsWith("fw-") || a.id === "custom-python" || a.id === "claude-code-gpt";
}

/** Org agents first, then built-in, test doubles dropped; alphabetical inside each group. */
export function splitAgents(agents: readonly AgentWithCaps[]): { own: AgentWithCaps[]; builtin: AgentWithCaps[] } {
	const usable = agents.filter((a) => !isTestAgent(a) && !a.error);
	const byId = (x: AgentWithCaps, y: AgentWithCaps) => x.id.localeCompare(y.id);
	return {
		own: usable.filter((a) => !isBuiltinAgent(a)).sort(byId),
		builtin: usable
			.filter(isBuiltinAgent)
			.sort((x, y) => Number(!BASELINE_AGENTS.includes(x.id)) - Number(!BASELINE_AGENTS.includes(y.id)) || byId(x, y)),
	};
}

/** Baseline agents a user can compare against, without the agent under test. */
export function opponentChoices(agents: readonly AgentWithCaps[], self: string | null): AgentWithCaps[] {
	return agents.filter((a) => BASELINE_AGENTS.includes(a.id) && a.id !== self);
}

/** Where /agents/new sends the user back after creating an agent. */
export const REGISTER_RETURN = "/evaluate";
