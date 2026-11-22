import type {
	EvaluationArm,
	EvaluationFailover,
	EvaluationGate,
	EvaluationMCPRoute,
	EvaluationMode,
	EvaluationPresetId,
	EvaluationSpec,
} from "@/lib/store/apis/evaluationsApi";
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

/**
 * Wizard state for /evaluate, shared by every step (lanes A11, A12, A13). One object, kept in
 * sessionStorage so leaving to register an agent (/agents/new) and coming back keeps the choices.
 *
 * Step 1 sets `agent` and `version`; step 2 sets `mode` and the A/B extras (`compareVersion`,
 * `opponent`); `arms` is derived from those by `armsFor` and can be edited further (models in
 * step 4). Steps 3 and 4 fill `benchmarks`, `pack`, `tasksPerBenchmark`, `trials`, `faults`,
 * `budgetUsd`. Step 5 posts `draftToSpec(draft)`.
 */
export interface EvaluateDraft {
	/** The name typed in the wizard; blank means the default name (`defaultName`). */
	name: string;
	/** The agent under test (config id). */
	agent: string | null;
	/** Its version; null means the latest. */
	version: number | null;
	mode: EvaluationMode;
	/** mode "versions": the other version of the same agent (arm B). */
	compareVersion: number | null;
	/** mode "opponent": the baseline agent (arm B). */
	opponent: string | null;
	arms: EvaluationArm[];
	benchmarks: string[];
	pack: string | null;
	tasksPerBenchmark: number;
	trials: number;
	faults: string[];
	budgetUsd: number | null;
	/** Runs of one benchmark at once; null is the server default (4). */
	concurrency?: number | null;
	/** The size preset the numbers came from, if any. */
	preset?: EvaluationPresetId | null;
	/** The worker the evaluation runs on; "" is the org's default worker. */
	worker?: string;
	/** Bifrost cache on vs off (score mode only). */
	bifrostCache?: boolean;
	/** How the arms reach MCP tool servers; unset is direct (the tool proxy only). */
	mcpRoute?: EvaluationMCPRoute;
	/** Provider failover option (score mode only). */
	failover?: EvaluationFailover | null;
	/** The release gate checked when the evaluation finishes; null or unset is no gate. */
	gate?: EvaluationGate | null;
	/** Run each benchmark's tasks one after another with the agent's memory kept. */
	stream?: EvaluationSpec["stream"] | null;
	/** Tell the agent its budget and deadline. */
	disclose_budget?: boolean;
	/** Arms dropped by a mode change (for example arm B in "score"), so going back restores their models. */
	stashedArms?: EvaluationArm[];
}

/** The Quick look preset: 10 tasks once each (the server's defaults too). */
export const DEFAULT_TASKS_PER_BENCHMARK = 10;
export const DEFAULT_TRIALS = 1;
export const DEFAULT_CONCURRENCY = 4;

/** The shipped bare-model baseline (agents/bare-model.yaml): the model alone, no tools. */
export const BARE_MODEL_AGENT = "bare-model";

export const EMPTY_DRAFT: EvaluateDraft = {
	name: "",
	agent: null,
	version: null,
	mode: "score",
	compareVersion: null,
	opponent: null,
	arms: [],
	benchmarks: [],
	pack: null,
	tasksPerBenchmark: DEFAULT_TASKS_PER_BENCHMARK,
	trials: DEFAULT_TRIALS,
	faults: ["clean"],
	budgetUsd: null,
	preset: "quick",
};

/**
 * The arms for the draft's mode. Models already chosen on matching arms are kept, so changing the
 * mode back and forth never loses them. `defaultModel` seeds arm A (the agent's own default).
 */
export function armsFor(d: EvaluateDraft, defaultModel = ""): EvaluationArm[] {
	if (!d.agent) return [];
	const prev = (label: string) => d.arms.find((a) => a.label === label) ?? d.stashedArms?.find((a) => a.label === label);
	const a: EvaluationArm = {
		label: "A",
		agent: d.agent,
		model: prev("A")?.model || defaultModel,
		...(prev("A")?.provider ? { provider: prev("A")?.provider } : {}),
		...(d.version != null ? { version: d.version } : {}),
	};
	const b0 = prev("B");
	const keepB = (x: EvaluationArm): EvaluationArm => (b0?.provider ? { ...x, provider: b0.provider } : x);
	switch (d.mode) {
		case "score":
			return [a];
		case "versions":
			return [
				a,
				keepB({
					label: "B",
					agent: d.agent,
					model: b0?.model || a.model,
					...(d.compareVersion != null ? { version: d.compareVersion } : {}),
				}),
			];
		case "models":
			return [a, keepB({ label: "B", agent: d.agent, model: b0?.model ?? "", ...(d.version != null ? { version: d.version } : {}) })];
		case "opponent":
			return [a, keepB({ label: "B", agent: d.opponent ?? "", model: b0?.model || a.model })];
	}
}

/** True when step 2 has what its mode needs. */
export function learnStepReady(d: EvaluateDraft): boolean {
	if (!d.agent) return false;
	if (d.mode === "versions") return d.compareVersion != null && d.compareVersion !== d.version;
	if (d.mode === "opponent") return Boolean(d.opponent) && d.opponent !== d.agent;
	return true;
}

/** The POST /evaluations body. */
export function draftToSpec(d: EvaluateDraft): EvaluationSpec {
	const faults = d.faults.includes("clean") ? d.faults : ["clean", ...d.faults];
	return {
		name: d.name.trim() || defaultName(d),
		mode: d.mode,
		arms: withBareModel(d.arms),
		benchmarks: d.benchmarks,
		...(d.pack ? { pack: d.pack } : {}),
		tasks_per_benchmark: d.tasksPerBenchmark,
		trials: d.trials,
		faults,
		...(d.budgetUsd != null ? { budget_usd: d.budgetUsd } : {}),
		...(d.worker ? { worker: d.worker } : {}),
		...(d.concurrency ? { concurrency: d.concurrency } : {}),
		...(d.preset ? { preset: d.preset } : {}),
		...(d.bifrostCache && d.mode === "score" ? { bifrost_cache: true } : {}),
		...(d.mcpRoute && d.mcpRoute !== "direct" ? { mcp_route: d.mcpRoute } : {}),
		...(d.failover?.enabled && d.arms.length === 1 ? { failover: d.failover } : {}),
		...(d.gate ? { gate: d.gate } : {}),
		...(d.stream ? { stream: d.stream } : {}),
		...(d.disclose_budget ? { disclose_budget: true } : {}),
	};
}

/** The bare-model baseline always runs on arm A's model: that is the comparison it stands for. */
export function withBareModel(arms: EvaluationArm[]): EvaluationArm[] {
	const a = arms[0];
	if (!a) return arms;
	return arms.map((x, i) => {
		if (i === 0 || x.agent !== BARE_MODEL_AGENT) return x;
		const { provider: _p, ...rest } = x;
		return { ...rest, model: a.model, ...(a.provider ? { provider: a.provider } : {}) };
	});
}

/**
 * The draft fields a step that edits an EvaluationSpec (steps 4 and 5) changes. With the current
 * draft `d`, the spec's name is carried too, except when it is the default name, so the default
 * keeps following the mode; without `d` the name stays the draft's.
 */
export function specToPatch(spec: EvaluationSpec, d?: EvaluateDraft): Partial<EvaluateDraft> {
	const name = d ? { name: spec.name.trim() === defaultName(d) ? "" : spec.name } : {};
	return {
		...name,
		arms: spec.arms,
		benchmarks: spec.benchmarks,
		pack: spec.pack ? spec.pack : null,
		tasksPerBenchmark: spec.tasks_per_benchmark,
		trials: spec.trials,
		faults: spec.faults,
		budgetUsd: spec.budget_usd ?? null,
		worker: spec.worker ?? "",
		concurrency: spec.concurrency ?? null,
		preset: spec.preset === "first_look" ? null : (spec.preset ?? null), // first_look (Connect box) is not a wizard size preset
		bifrostCache: Boolean(spec.bifrost_cache),
		mcpRoute: spec.mcp_route ?? "direct",
		failover: spec.failover ?? null,
		gate: spec.gate ?? null,
		stream: spec.stream ?? null,
		disclose_budget: Boolean(spec.disclose_budget),
	};
}

export function defaultName(d: EvaluateDraft): string {
	const who = d.agent ? `${d.agent}${d.version != null ? ` v${d.version}` : ""}` : "agent";
	switch (d.mode) {
		case "versions":
			return `${who} vs v${d.compareVersion ?? "?"}`;
		case "models":
			return `${who}: two models`;
		case "opponent":
			return d.opponent === BARE_MODEL_AGENT ? `${who} vs the bare model` : `${who} vs ${d.opponent ?? "baseline"}`;
		default:
			return `${who} score`;
	}
}

const ARM_KEYS: (keyof EvaluateDraft)[] = ["agent", "version", "mode", "compareVersion", "opponent"];

/** Merge a patch; a change to who or what is compared re-derives the arms unless the patch sets them. */
export function applyPatch(d: EvaluateDraft, patch: Partial<EvaluateDraft>): EvaluateDraft {
	const next = { ...d, ...patch };
	if (!("arms" in patch) && ARM_KEYS.some((k) => k in patch)) {
		next.arms = armsFor(next);
		const kept = new Set(next.arms.map((a) => a.label));
		next.stashedArms = [...d.arms, ...(d.stashedArms ?? []).filter((a) => !d.arms.some((x) => x.label === a.label))].filter(
			(a) => !kept.has(a.label),
		);
	}
	return next;
}

const STORAGE_KEY = "heimdall.evaluateDraft";

function load(): EvaluateDraft {
	try {
		const raw = typeof sessionStorage !== "undefined" ? sessionStorage.getItem(STORAGE_KEY) : null;
		if (raw) return { ...EMPTY_DRAFT, ...(JSON.parse(raw) as Partial<EvaluateDraft>) };
	} catch {
		/* private window or bad JSON: start fresh */
	}
	return EMPTY_DRAFT;
}

export interface EvaluateDraftContextValue {
	draft: EvaluateDraft;
	/** Merge fields into the draft. */
	update(patch: Partial<EvaluateDraft>): void;
	reset(): void;
}

const EvaluateDraftContext = createContext<EvaluateDraftContextValue | null>(null);

export function EvaluateDraftProvider({ children, initial }: { children: ReactNode; initial?: Partial<EvaluateDraft> }) {
	const [draft, setDraft] = useState<EvaluateDraft>(() => ({ ...load(), ...initial }));
	useEffect(() => {
		try {
			sessionStorage.setItem(STORAGE_KEY, JSON.stringify(draft));
		} catch {
			/* storage blocked: the draft still works for this page */
		}
	}, [draft]);
	const update = useCallback((patch: Partial<EvaluateDraft>) => setDraft((d) => applyPatch(d, patch)), []);
	const reset = useCallback(() => setDraft(EMPTY_DRAFT), []);
	const value = useMemo(() => ({ draft, update, reset }), [draft, update, reset]);
	return <EvaluateDraftContext.Provider value={value}>{children}</EvaluateDraftContext.Provider>;
}

/** The wizard draft. Throws outside the /evaluate shell. */
export function useEvaluateDraft(): EvaluateDraftContextValue {
	const v = useContext(EvaluateDraftContext);
	if (!v) throw new Error("useEvaluateDraft must be used inside EvaluateDraftProvider");
	return v;
}

/** Alias the lanes were told to import. */
export const evaluateDraft = { Provider: EvaluateDraftProvider, use: useEvaluateDraft };