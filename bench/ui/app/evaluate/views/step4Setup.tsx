import { useState } from "react";
import { useTranslation } from "react-i18next";
import "./fw26I18n";
import { GroupedModelPicker } from "@/components/models/groupedModelPicker";
import { FailoverSettings, failoverProblem } from "@/components/evaluations/failoverSettings";
import { GateSettings } from "@/components/releases/gateSettings";
import {
	type EvaluationEstimate,
	type EvaluationMCPRoute,
	type EstimateRequest,
	type EvaluationSpec,
	type FaultCatalogItem,
	useEstimateEvaluationQuery,
	useGetBenchFaultsQuery,
} from "@/lib/store/apis/evaluationsApi";
import { fw05 } from "./fw05I18n";
import { armHasModel, useOwnModelAgents } from "./ownModel";
import { CONCURRENCY_CHOICES, PresetPicker, slotsText } from "./presetPicker";
import { useDebounced } from "./useDebounced";
import { evalArmLabel } from "@/lib/evaluationArms";
import { costRangeText, isRoughEstimate } from "./evaluate.utils";
import { KeyLines, useArmKeys } from "./armKeys";
import "@/lib/types/evaluateI18n";
import { MODEL_SWITCH_MAX, MODEL_SWITCH_MIN, MODEL_SWITCH_MODE } from "./evaluateDraft";
import { ufk6 } from "./ufk6I18n";
import { ufl3 } from "./ufl3I18n";

/**
 * Wizard step 4: the model for each arm, how many tasks per benchmark, trials and faults, with a
 * live estimate from POST /evaluations/estimate. The draft is the EvaluationSpec itself (A11 owns
 * the wizard state; this step only reads and writes the spec it is given).
 */

export const TASK_CHOICES = [3, 5, 8, 10, 20, 30, 50] as const;
export const TRIAL_CHOICES = [1, 2, 3, 5] as const;

/** Shown while GET /faults loads or when it fails. */
export const FAULTS: readonly FaultCatalogItem[] = [
	{
		id: "model-down",
		label: "Model down",
		help: "The model stops answering for a while, to see if the agent waits and recovers.",
		layer: "model",
		group: "Model",
	},
	{
		id: "broken-tool",
		label: "Broken tool",
		help: "Some tool calls return errors, to see if the agent notices and works around them.",
		layer: "tool",
		group: "Tools",
	},
	{
		id: "misleading-instruction",
		label: "Misleading instruction",
		help: "The task comes with a wrong or stale hint, to see if the agent checks before trusting it. Skipped on benchmarks without one.",
		layer: "instructions",
		group: "Instructions",
	},
];

const GROUP_ORDER = ["Model", "Tools", "Instructions", "Combined"];

/** Faults grouped for the checklist: known groups in a fixed order, then any other group, clean left out. */
export function groupFaults(faults: readonly FaultCatalogItem[]): { group: string; faults: FaultCatalogItem[] }[] {
	const by = new Map<string, FaultCatalogItem[]>();
	for (const f of faults) {
		if (f.id === "clean") continue;
		const g = f.group || "Other";
		by.set(g, [...(by.get(g) ?? []), f]);
	}
	const groups = [...by.keys()].sort((a, b) => {
		const ia = GROUP_ORDER.indexOf(a);
		const ib = GROUP_ORDER.indexOf(b);
		return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib) || a.localeCompare(b);
	});
	return groups.map((group) => ({ group, faults: by.get(group)! }));
}

/** Above this a detectable change is meaningless: a score moves at most 100 points. */
export const POWER_MAX_PP = 100;

/** The power line: "These settings can detect a change of about X points overall (Y per benchmark)." */
export function powerText(e: Pick<EvaluationEstimate, "power">): { line: string; advice: string } | undefined {
	const p = e.power;
	if (!p) return undefined;
	const overall = p.overall?.mde_pp;
	const per = (p.per_benchmark ?? []).map((b) => b.mde_pp).filter((x): x is number => typeof x === "number" && Number.isFinite(x));
	// A change above 100 points cannot happen (scores run 0 to 100): it means too few tasks (Q-9).
	const perMax = per.length ? Math.max(...per) : undefined;
	const perText =
		(p.per_benchmark ?? []).length === 0
			? ""
			: perMax === undefined || perMax > POWER_MAX_PP
				? fw05("power.perTooFew")
				: fw05("power.per", { n: String(Math.round(perMax)) });
	const line =
		typeof overall === "number" && Number.isFinite(overall) && overall <= POWER_MAX_PP
			? fw05("power.line", { n: String(Math.round(overall)), per: perText })
			: typeof overall === "number" && Number.isFinite(overall)
				? fw05("power.tooFewChange")
				: fw05("power.tooFew");
	return { line, advice: p.advice ?? "" };
}

/** A starting spec for the wizard when no draft exists yet. */
export function defaultEvaluationSpec(): EvaluationSpec {
	return {
		name: "",
		mode: "score",
		arms: [{ label: evalArmLabel(0), agent: "", model: "" }],
		benchmarks: [],
		tasks_per_benchmark: 10,
		trials: 1,
		faults: ["clean"],
		preset: "quick",
	};
}

/** The MCP routes step 4 offers, in order. */
export const MCP_ROUTES: { id: EvaluationMCPRoute; label: string; help: string }[] = [
	{ id: "direct", label: "Direct (default)", help: "The agent calls the task's MCP servers through Heimdall's tool proxy." },
	{
		id: "bifrost",
		label: "Through the gateway (logs every tool call)",
		help: "Each run gets its own Bifrost MCP clients and MCP key, so every tool call shows in Bifrost's MCP logs. Tool faults still apply: the tool proxy sits behind Bifrost.",
	},
	{
		id: "bifrost_code_mode",
		label: "Through Bifrost, Code Mode",
		help: "As above, but the agent sees Bifrost's Code Mode tools and writes programs that call the servers.",
	},
];

/**
 * The default for Runs at once (M3-31): the worker's slot count from the estimate, so the default never sits next to its
 * own "only N at once" warning; 2 when the estimate does not say.
 */
export function defaultConcurrency(slots: { worker_slots?: number; concurrency?: number } | undefined): number {
	if (slots?.worker_slots && slots.worker_slots > 0) return slots.worker_slots;
	if (slots?.concurrency && slots.concurrency > 0) return slots.concurrency;
	return 2;
}

/** Whether any setting under "More settings" differs from its default, or the goal is a failure test (M3-29). */
export function advancedChanged(spec: EvaluationSpec): boolean {
	return (
		spec.faults.some((f) => f !== "clean") ||
		Boolean(spec.bifrost_cache) ||
		Boolean(spec.mcp_route && spec.mcp_route !== "direct") ||
		Boolean(spec.failover?.enabled) ||
		Boolean(spec.gate) ||
		spec.concurrency != null ||
		spec.split === "held_out"
	);
}

/** spec with route set; "direct" leaves mcp_route out so the spec is as before. */
export function withMCPRoute(spec: EvaluationSpec, route: EvaluationMCPRoute): EvaluationSpec {
	const { mcp_route: _old, ...rest } = spec;
	return route === "direct" ? rest : { ...rest, mcp_route: route };
}

/** Faults with "clean" always first and present, no repeats. */
export function toggleFault(faults: string[], fault: string, on: boolean): string[] {
	const rest = faults.filter((f) => f !== "clean" && f !== fault);
	return ["clean", ...(on ? [...rest, fault] : rest)];
}

/**
 * Why the spec cannot be estimated or launched yet, in plain words; "" when it can. In order: no
 * benchmark (and no pack), the first setup without a model (an arm on an agent in `owns` brings
 * its own model and may leave it blank), then the failover problem.
 */
export function specProblem(spec: EvaluationSpec, owns: ReadonlySet<string> = new Set()): string {
	if (spec.benchmarks.length === 0 && !spec.pack) return fw05("problem.noBenchmark");
	if (spec.arms.length === 0) return fw05("problem.noModel", { label: "A" });
	const noModel = spec.arms.find((a) => !armHasModel(a, owns));
	if (noModel) return fw05("problem.noModel", { label: noModel.label });
	if (isModelSwitch(spec)) {
		if (spec.arms.length < MODEL_SWITCH_MIN) return ufl3("models.needMore", { min: MODEL_SWITCH_MIN });
		const seen = new Set(spec.arms.map((a) => `${a.provider ?? ""}/${a.model}`));
		if (seen.size < spec.arms.length) return ufl3("models.same");
	}
	if (spec.mode === "models" && spec.arms.length === 2) {
		const [a, b] = spec.arms;
		if (a.model && a.model === b.model && (a.provider ?? "") === (b.provider ?? "")) return ufk6("twoModels.same");
	}
	return failoverProblem(spec.arms[0], spec.failover);
}

/** Mode model_switch ("Find the best model"): the shared spec type does not list it yet. */
export function isModelSwitch(spec: Pick<EvaluationSpec, "mode">): boolean {
	return (spec.mode as string) === MODEL_SWITCH_MODE;
}

/** A model_switch spec with one more empty model row (same agent and version as row A), up to MODEL_SWITCH_MAX. */
export function addModelArm(spec: EvaluationSpec): EvaluationSpec {
	const a = spec.arms[0];
	if (!a || spec.arms.length >= MODEL_SWITCH_MAX) return spec;
	const arm = { label: evalArmLabel(spec.arms.length), agent: a.agent, model: "", ...(a.version != null ? { version: a.version } : {}) };
	return { ...spec, arms: [...spec.arms, arm] };
}

/** A model_switch spec without row i, relabelled A, B, C... in order; never below MODEL_SWITCH_MIN. */
export function removeModelArm(spec: EvaluationSpec, i: number): EvaluationSpec {
	if (i === 0 || spec.arms.length <= MODEL_SWITCH_MIN) return spec;
	return { ...spec, arms: spec.arms.filter((_, j) => j !== i).map((x, j) => ({ ...x, label: evalArmLabel(j) })) };
}

/** Whether the spec is complete enough to estimate or launch: specProblem has nothing to say. */
export function specReady(spec: EvaluationSpec, owns: ReadonlySet<string> = new Set()): boolean {
	return specProblem(spec, owns) === "";
}

/** The estimate's slots, including what the worker runs at a time (`worker_slots`). */
type EstimateSlots = EvaluationEstimate["slots"];

/**
 * The note under "Runs at once per benchmark" when the picked value is above what the worker runs
 * at a time; "" otherwise (no worker bound known, or the value fits).
 */
export function concurrencyNote(slots: EstimateSlots | undefined, value: number): string {
	const n = slots?.worker_slots ?? 0;
	if (!(n > 0) || value <= n) return "";
	return fw05("slots.overWorker", { n: String(n), value: String(value) });
}

function range(low: number, high: number, fmt: (n: number) => string): string {
	// Compared after formatting, so 0.6 to 1.4 minutes reads "1 min", never "1 to 1" (N211).
	const a = fmt(low);
	const b = fmt(high);
	return a === b ? a : `${a} to ${b}`;
}

/**
 * The estimate as three short phrases: runs, cost (or "price not known") and minutes (or "time not
 * known"). Tolerates missing ranges, so a partial answer still reads.
 */
export function estimateText(e: Pick<EvaluationEstimate, "runs" | "est_cost_usd" | "est_minutes">): {
	runs: string;
	cost: string;
	minutes: string;
} {
	const runs = Number(e.runs) || 0;
	const c = e.est_cost_usd;
	const cost = c?.known ? (costRangeText(c) ?? "") : c?.reason ? `price not known (${c.reason})` : "price not known";
	const m = e.est_minutes;
	const minutesKnown = m && m.known !== false && (m.low > 0 || m.high > 0);
	const minutes = minutesKnown ? `${range(m.low, m.high, (n) => String(Math.max(1, Math.round(n))))} min` : "time not known";
	return { runs: `${runs} ${runs === 1 ? "run" : "runs"}`, cost, minutes };
}

export interface EstimateNote {
	benchmark: string;
	skipped?: string;
	notes: string[];
}

/**
 * A server note in plain words (Q-66): the same-config refusal of a two-model draft becomes a hint, the
 * "arm A:" and "rough:" prefixes go, and long numbers get thousands separators.
 */
export function plainNote(note: string): string {
	if (/same config hash/i.test(note)) return fw05("note.sameConfig");
	const s = note
		.replace(/^\s*arms? [A-Za-z0-9]+:\s*/i, "")
		.replace(/^\s*rough:\s*/i, "")
		.replace(/(?<![\d.,])\d{4,}(?![\d.,])/g, (n) => Number(n).toLocaleString("en-US"))
		.trim();
	return s.charAt(0).toUpperCase() + s.slice(1);
}

/** Notes through plainNote, empty ones and repeats left out (each sentence once). */
export function plainNotes(notes: readonly string[]): string[] {
	const out: string[] = [];
	for (const n of notes) {
		if (!n) continue;
		const p = plainNote(n);
		if (p && !out.includes(p)) out.push(p);
	}
	return out;
}

/** Per-benchmark skips and notes in plain words, plus the estimate's own notes under "". Empty ones are left out. */
export function estimateNotes(e: Pick<EvaluationEstimate, "per_benchmark" | "notes">): EstimateNote[] {
	const out: EstimateNote[] = [];
	const general = plainNotes(e.notes ?? []);
	if (general.length) out.push({ benchmark: "", notes: general });
	for (const b of e.per_benchmark ?? []) {
		const notes = plainNotes([...(b.notes ?? []), ...(b.faults_skipped ?? []).map((f) => `${f.fault} is skipped: ${f.reason}`)]);
		if (b.skipped || notes.length)
			out.push({
				benchmark: b.benchmark,
				skipped: b.skipped || undefined,
				notes,
			});
	}
	return out;
}

/** The server's message for a failed estimate, if it sent one. */
export function estimateErrorText(error: unknown): string {
	const data = (error as { data?: { error?: { message?: string } | string } } | undefined)?.data;
	const msg = typeof data?.error === "string" ? data.error : data?.error?.message;
	return msg ? plainNote(msg) : "";
}

/**
 * Tasks per benchmark for the texts that name it (Q-8): "10 tasks" when every benchmark has that many ready tasks,
 * else "2 ready of 10 tasks" with the ready count from the estimate, so the number never disagrees with the runs.
 */
export function tasksText(
	t: (key: string, opts?: Record<string, unknown>) => string,
	spec: Pick<EvaluationSpec, "tasks_per_benchmark">,
	est: Pick<EvaluationEstimate, "power"> | undefined,
): string {
	const ready = readyTasks(spec, est);
	return ready === null ? t("step4.tasksN", { count: spec.tasks_per_benchmark }) : t("step4.tasksReady", { n: spec.tasks_per_benchmark, ready });
}

/** Ready tasks over all benchmarks when some benchmark has fewer than asked; null when every one has enough or it is not known. */
export function readyTasks(spec: Pick<EvaluationSpec, "tasks_per_benchmark">, est: Pick<EvaluationEstimate, "power"> | undefined): number | null {
	const per = est?.power?.per_benchmark ?? [];
	if (per.length === 0 || !per.some((b) => b.tasks < spec.tasks_per_benchmark)) return null;
	return per.reduce((n, b) => n + Math.min(b.tasks, spec.tasks_per_benchmark), 0);
}

/** Whether a failed estimate is a real HTTP error (a status code), not a missing answer. */
export function estimateFailed(error: unknown): boolean {
	return Boolean(error && typeof error === "object" && "status" in error && typeof (error as { status: unknown }).status === "number");
}

/**
 * Chosen faults that apply to none of the benchmarks that run, with the first reason. A benchmark
 * skipped as a whole does not count; with no benchmark left nothing is flagged.
 */
export function faultsNotApplying(e: Pick<EvaluationEstimate, "per_benchmark"> | undefined): Map<string, string> {
	const out = new Map<string, string>();
	const running = (e?.per_benchmark ?? []).filter((b) => !b.skipped);
	if (running.length === 0) return out;
	const first = running[0].faults_skipped ?? [];
	for (const f of first) {
		if (out.has(f.fault)) continue;
		if (running.every((b) => (b.faults_skipped ?? []).some((x) => x.fault === f.fault))) out.set(f.fault, f.reason);
	}
	return out;
}

/** Why nothing can run when the estimate plans no runs ("<benchmark>: <reason>", the first one); null otherwise. */
export function nothingRunsReason(e: Pick<EvaluationEstimate, "runs" | "per_benchmark" | "notes"> | undefined): string | null {
	if (!e || Number(e.runs) > 0) return null;
	const b = (e.per_benchmark ?? []).find((x) => x.skipped);
	if (b) return `${b.benchmark}: ${b.skipped}`;
	return (e.notes ?? []).find(Boolean) ?? "no run is planned";
}

/**
 * The request for the preset sizes. Tasks, trials, the preset and the name are left at fixed values,
 * so changing them never asks for the presets again (each preset is a full estimate on the server).
 */
export function presetsRequest(spec: EvaluationSpec): EstimateRequest {
	const { name: _n, tasks_per_benchmark: _t, trials: _r, preset: _p, budget_usd: _b, gate: _g, ...rest } = spec;
	return { ...rest, name: "", tasks_per_benchmark: 10, trials: 1, presets: true };
}

/** How long step 4 waits after the last change before it asks for a new estimate. */
export const ESTIMATE_DEBOUNCE_MS = 400;

/**
 * Step 4's estimate: the spec held back 400 ms, the main estimate without presets and a second
 * one for the preset sizes keyed on what changes them. `pending` is true while a change waits.
 * The wizard calls it too (same arguments, so the same cached request) to block Next.
 */
export function useStep4Estimate(spec: EvaluationSpec, skip: boolean) {
	const owns = useOwnModelAgents();
	const settled = useDebounced(spec, ESTIMATE_DEBOUNCE_MS);
	// Q-7: the request sends the held-back spec, so its readiness decides the skip too. A link that fills the
	// draft makes the live spec ready while the held-back one is still half made; that one is never posted.
	const skipSettled = estimateSkip(skip, specReady(settled, owns));
	const main = useEstimateEvaluationQuery(settled, { skip: skipSettled });
	const presets = useEstimateEvaluationQuery(presetsRequest(settled), { skip: skipSettled });
	const pending = JSON.stringify(settled) !== JSON.stringify(spec);
	return { main, presets, pending };
}

/** Whether step 4 skips its estimate: the live spec says skip, or the held-back spec it would post is not ready. */
export function estimateSkip(skipLive: boolean, settledReady: boolean): boolean {
	return skipLive || !settledReady;
}

/** Whether step 4 is still working out its estimate (Next waits): a change is held back or the request is in flight. */
export function estimateBusy(e: { pending: boolean; main: { isFetching?: boolean } }, skipped: boolean): boolean {
	return !skipped && (e.pending || Boolean(e.main.isFetching));
}

/**
 * What the estimate's cost rests on, for the basis line (Q-10): other agents' runs ("library"), a rough list
 * price guess ("rough"), this setup's own past runs ("own"), or nothing to say (null: no known cost).
 */
export function estimateBasis(
	e: Pick<EvaluationEstimate, "from_library" | "per_benchmark"> & { est_cost_usd?: { known?: boolean } },
): "library" | "rough" | "own" | null {
	if (e.from_library) return "library";
	if (isRoughEstimate(e)) return "rough";
	return e.est_cost_usd?.known ? "own" : null;
}

/** The estimate result: runs, cost and time, what it is based on, why nothing runs, then every note. */
export function EstimateSummary({ estimate, updating }: { estimate: EvaluationEstimate; updating?: boolean }) {
	const { t } = useTranslation("fw26");
	const est = estimateText(estimate);
	const notes = estimateNotes(estimate);
	const power = powerText(estimate);
	const slots = slotsText(estimate);
	const none = Number(estimate.runs) > 0 ? [] : notes.filter((n) => n.skipped);
	const basis = estimateBasis(estimate);
	return (
		<div className="flex flex-col gap-2">
			<span>
				About <strong data-testid="step4-est-runs">{est.runs}</strong>, <strong data-testid="step4-est-cost">{est.cost}</strong>,{" "}
				<strong data-testid="step4-est-minutes">{est.minutes}</strong>.
				{updating && <span className="text-muted-foreground"> Updating...</span>}
			</span>
			{/* "From past runs" only when there are past runs: a list price guess says so instead (Q-10). */}
			{basis && (
				<span
					className={basis === "library" ? "text-chart-info-ink text-xs font-medium" : "text-muted-foreground text-xs"}
					data-testid="step4-basis"
				>
					{basis === "library" ? t("step4.basisLibrary") : basis === "rough" ? fw05("basis.rough") : t("step4.basisOwn")}
				</span>
			)}
			{none.length > 0 && (
				<div className="border-chart-warning/40 bg-chart-warning/10 rounded-md border px-3 py-2" data-testid="step4-nothing-runs">
					<span className="font-medium">{t("step4.nothingRuns")}</span>
					<ul className="mt-1 flex flex-col gap-1">
						{none.map((n) => (
							<li key={n.benchmark}>
								<span className="font-medium">{n.benchmark}: </span>
								{n.skipped}
							</li>
						))}
					</ul>
				</div>
			)}
			{power && (
				<span data-testid="step4-power">
					{power.line}
					{power.advice && <span className="text-muted-foreground"> {power.advice}</span>}
				</span>
			)}
			{slots && (
				<span className="text-muted-foreground" data-testid="step4-slots">
					{slots}
				</span>
			)}
			{notes.length > 0 && (
				<div data-testid="step4-est-notes">
					<ul className="flex flex-col gap-1.5 text-xs">
						{notes.map((n) => (
							<li key={n.benchmark || "_all"} data-testid={`step4-est-note-${n.benchmark || "all"}`}>
								{n.benchmark && <span className="font-medium">{n.benchmark}: </span>}
								{n.skipped && <span>skipped, {n.skipped}. </span>}
								{n.notes.length > 0 && <span className="text-muted-foreground">{n.notes.join(". ")}</span>}
							</li>
						))}
					</ul>
				</div>
			)}
		</div>
	);
}

// Chips big enough to hit on a phone (M3-47).
const chip = "inline-flex min-h-11 items-center rounded-md border px-3 text-sm sm:min-h-9";
const chipOn = "border-primary bg-primary/10 text-primary";
const chipOff = "border-input bg-background hover:bg-muted";

function Choices({
	label,
	value,
	options,
	onPick,
	testId,
}: {
	label: string;
	value: number;
	options: readonly number[];
	onPick(n: number): void;
	testId: string;
}) {
	return (
		<div className="flex flex-col gap-1.5">
			<span className="text-sm font-medium">{label}</span>
			<div className="flex gap-2" role="radiogroup" aria-label={label} data-testid={testId}>
				{options.map((n) => (
					<button
						key={n}
						type="button"
						role="radio"
						aria-checked={value === n}
						className={`${chip} ${value === n ? chipOn : chipOff}`}
						onClick={() => onPick(n)}
						data-testid={`${testId}-${n}`}
					>
						{n}
					</button>
				))}
			</div>
		</div>
	);
}

export interface Step4SetupProps {
	spec: EvaluationSpec;
	onChange(spec: EvaluationSpec): void;
}

export function Step4Setup({ spec, onChange }: Step4SetupProps) {
	const { t } = useTranslation("fw26");
	const owns = useOwnModelAgents();
	const [picking, setPicking] = useState<Set<string>>(() => new Set());
	const problem = specProblem(spec, owns);
	const ready = problem === "";
	const { main: estimate, presets, pending } = useStep4Estimate(spec, !ready);
	const concurrency = spec.concurrency ?? defaultConcurrency(estimate.data?.slots);
	const { t: te } = useTranslation("evaluate");
	const faultCount = spec.faults.filter((f) => f !== "clean").length;
	const moreSummary = te("step4.moreSummary", {
		tasks: tasksText(te as never, spec, estimate.data),
		tries: te("step4.triesN", { count: spec.trials }),
		faults: faultCount ? te("step4.faultsN", { count: faultCount }) : te("step4.noFaults"),
		gateway: spec.mcp_route && spec.mcp_route !== "direct" ? te("step4.gatewayOn") : te("step4.gatewayOff"),
	});
	const keys = useArmKeys(spec.arms);
	const overWorker = concurrencyNote(estimate.data?.slots, concurrency);
	const catalog = useGetBenchFaultsQuery();
	const faultList = catalog.data && catalog.data.length > 0 ? catalog.data : FAULTS;
	const faultGroups = groupFaults(faultList);
	const notApplying = faultsNotApplying(estimate.data);
	const chosenNotApplying = spec.faults
		.filter((f) => f !== "clean" && notApplying.has(f))
		.map((f) => faultList.find((x) => x.id === f)?.label ?? f);
	const setArm = (i: number, provider: string, model: string) =>
		onChange({
			...spec,
			arms: spec.arms.map((a, j) => (j === i ? { ...a, provider, model } : a)),
		});

	return (
		<div className="flex flex-col gap-6" data-testid="step4-setup">
			<section className="flex flex-col gap-3">
				<h3 className="text-base font-semibold">{te("step4.model")}</h3>
				{spec.arms.map((arm, i) => (
					<div key={`${arm.label}-${i}`} className="flex flex-col gap-1.5" data-testid={`step4-arm-${arm.label}`}>
						<span className="text-sm font-medium">
							{te("step4.armUses", {
								label: spec.arms.length > 1 ? `${arm.label}: ` : "",
								agent: arm.agent || te("step4.yourAgent"),
								version: arm.version ? ` v${arm.version}` : "",
							})}
						</span>
						{owns.has(arm.agent) && !arm.model && !picking.has(arm.label) ? (
							<div
								className="bg-muted/40 flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm"
								data-testid={`step4-own-model-${arm.label}`}
							>
								<span>
									<span className="font-medium">{te("step4.ownModel")}</span>{" "}
									<span className="text-muted-foreground">{te("step4.ownModelHelp")}</span>
								</span>
								<button
									type="button"
									className="text-primary text-xs hover:underline"
									onClick={() => setPicking((p) => new Set(p).add(arm.label))}
									data-testid={`step4-pick-model-${arm.label}`}
								>
									{te("step4.pickModelInstead")}
								</button>
							</div>
						) : (
							<>
								<GroupedModelPicker
									value={arm.model || arm.provider ? { provider: arm.provider ?? "", model: arm.model } : null}
									onChange={(v) => setArm(i, v.provider, v.model)}
									testId={`step4-model-${arm.label}`}
									label={te("step4.modelFor", { agent: arm.agent || te("step4.yourAgentLower") })}
								/>
								<KeyLines rows={keys.rows.filter((k) => k.label === arm.label)} returnTo="/evaluate?step=4" okOnly testId={`step4-key-${arm.label}`} />
								{owns.has(arm.agent) && (
									<button
										type="button"
										className="text-primary w-fit text-xs hover:underline"
										onClick={() => {
											setPicking((p) => {
												const n = new Set(p);
												n.delete(arm.label);
												return n;
											});
											setArm(i, "", "");
										}}
										data-testid={`step4-use-own-${arm.label}`}
									>
										{te("step4.useOwnModel")}
									</button>
								)}
							</>
						)}
						{isModelSwitch(spec) && i > 0 && spec.arms.length > MODEL_SWITCH_MIN && (
							<button
								type="button"
								className="text-muted-foreground w-fit text-xs hover:underline"
								onClick={() => onChange(removeModelArm(spec, i))}
								data-testid={`step4-remove-model-${arm.label}`}
							>
								{ufl3("models.remove")}
							</button>
						)}
					</div>
				))}
				{isModelSwitch(spec) && (
					<div className="flex flex-wrap items-center gap-3 text-sm" data-testid="step4-model-switch">
						<button
							type="button"
							className="text-primary disabled:text-muted-foreground w-fit hover:underline disabled:no-underline"
							disabled={spec.arms.length >= MODEL_SWITCH_MAX}
							onClick={() => onChange(addModelArm(spec))}
							data-testid="step4-add-model"
						>
							{ufl3("models.add")}
						</button>
						<span className="text-muted-foreground text-xs">{ufl3("models.count", { count: spec.arms.length, max: MODEL_SWITCH_MAX })}</span>
					</div>
				)}
			</section>

			<PresetPicker
				spec={spec}
				presets={presets.currentData?.presets ?? []}
				draft={estimate.currentData}
				onPick={(p) => onChange({ ...spec, tasks_per_benchmark: p.tasks_per_benchmark, trials: p.trials, faults: p.faults, preset: p.id })}
			/>

			{/* Model and the size presets stay open; the rest folds into More settings with its values in the summary (M3-29). */}
			<details className="rounded-lg border" open={advancedChanged(spec) || undefined} data-testid="step4-more">
				<summary className="hover:bg-muted/50 flex min-h-11 cursor-pointer flex-wrap items-center gap-x-3 rounded-lg px-3 py-2 text-sm">
					<span className="font-medium">{te("step4.more")}</span>
					<span className="text-muted-foreground">{moreSummary}</span>
				</summary>
				<div className="flex flex-col gap-6 border-t px-3 py-4">
			<section className="flex flex-wrap gap-8">
				<Choices
					label={te("step4.tasks")}
					value={spec.tasks_per_benchmark}
					options={TASK_CHOICES}
					onPick={(n) => onChange({ ...spec, tasks_per_benchmark: n, preset: undefined })}
					testId="step4-tasks"
				/>
				<Choices
					label={te("step4.tries")}
					value={spec.trials}
					options={TRIAL_CHOICES}
					onPick={(n) => onChange({ ...spec, trials: n, preset: undefined })}
					testId="step4-trials"
				/>
				<div className="flex flex-col gap-1.5">
					<Choices
						label={te("step4.parallel")}
						value={concurrency}
						options={CONCURRENCY_CHOICES}
						onPick={(n) => onChange({ ...spec, concurrency: n })}
						testId="step4-concurrency"
					/>
					{overWorker && (
						<p className="text-muted-foreground max-w-xs text-xs" data-testid="step4-concurrency-note">
							{overWorker}
						</p>
					)}
				</div>
			</section>

			<section className="flex flex-col gap-2">
				<h3 className="text-base font-semibold">Faults</h3>
				<p className="text-muted-foreground text-sm">
					Every evaluation runs clean. Add faults to see how the agent copes when things go wrong.
				</p>
				{chosenNotApplying.length > 0 && (
					<p
						className="border-chart-warning/40 bg-chart-warning/10 text-chart-warning-ink rounded-md border px-3 py-2 text-sm"
						data-testid="step4-faults-na"
					>
						{t("step4.faultsNotApplying", { faults: chosenNotApplying.join(", ") })}
					</p>
				)}
				{faultGroups.map((g) => (
					<fieldset key={g.group} className="flex flex-col gap-2" data-testid={`step4-fault-group-${g.group}`}>
						<legend className="text-muted-foreground mb-1 text-xs font-medium tracking-wide uppercase">{g.group}</legend>
						{g.faults.map((f) => (
							<label key={f.id} className="flex items-start gap-2 text-sm" data-testid={`step4-fault-${f.id}`}>
								<input
									type="checkbox"
									className="mt-1 size-4"
									checked={spec.faults.includes(f.id)}
									onChange={(e) =>
										onChange({
											...spec,
											faults: toggleFault(spec.faults, f.id, e.target.checked),
											preset: undefined,
										})
									}
								/>
								<span>
									<span className="font-medium">{f.label}</span>
									<span className="text-muted-foreground block">{f.help}</span>
									{notApplying.has(f.id) && (
										<span className="text-chart-warning-ink block text-xs" data-testid={`step4-fault-na-${f.id}`}>
											{t("step4.faultNotApplying", { reason: notApplying.get(f.id) })}
										</span>
									)}
								</span>
							</label>
						))}
					</fieldset>
				))}
			</section>

			{spec.mode === "score" ? (
				<section className="flex flex-col gap-2">
					<h3 className="text-base font-semibold">Bifrost cache</h3>
					<label className="flex items-start gap-2 text-sm" data-testid="step4-bifrost-cache">
						<input
							type="checkbox"
							className="mt-1 size-4"
							checked={Boolean(spec.bifrost_cache)}
							onChange={(e) => {
								const { bifrost_cache: _old, ...rest } = spec;
								onChange(e.target.checked ? { ...rest, bifrost_cache: true } : rest);
							}}
						/>
						<span>
							<span className="font-medium">{te("step4.cache")}</span>
							<span className="text-muted-foreground block">
								Runs your agent twice, identical except that Bifrost&apos;s semantic cache is on for the second copy. The report shows the
								cache hit rate, the latency and cost saved, and any change in success. Doubles the runs.
							</span>
						</span>
					</label>
				</section>
			) : null}

			<section className="flex flex-col gap-2" data-testid="step4-mcp-route">
				<h3 className="text-base font-semibold">MCP tools</h3>
				<p className="text-muted-foreground text-sm">
					How agents reach the task's MCP tool servers. Benchmarks with no MCP server, and agents that cannot reach Bifrost, are skipped
					when routed through Bifrost.
				</p>
				{MCP_ROUTES.map((r) => (
					<label key={r.id} className="flex items-start gap-2 text-sm" data-testid={`step4-mcp-route-${r.id}`}>
						<input
							type="radio"
							name="step4-mcp-route"
							className="mt-1"
							checked={(spec.mcp_route ?? "direct") === r.id}
							onChange={() => onChange(withMCPRoute(spec, r.id))}
						/>
						<span>
							<span className="font-medium">{r.label}</span>
							<span className="text-muted-foreground block">{r.help}</span>
						</span>
					</label>
				))}
			</section>

			<fieldset className="flex flex-col gap-2" data-testid="step4-split">
				<legend className="mb-1 text-base font-semibold">{ufl3("split.title")}</legend>
				{(["public", "held_out"] as const).map((v) => (
					<label key={v} className="flex items-start gap-2 text-sm" data-testid={`step4-split-${v}`}>
						<input
							type="radio"
							name="step4-split"
							className="mt-1"
							checked={(spec.split === "held_out" ? "held_out" : "public") === v}
							onChange={() => {
								const { split: _old, ...rest } = spec;
								onChange(v === "held_out" ? { ...rest, split: "held_out" } : rest);
							}}
						/>
						<span>
							<span className="font-medium">{v === "held_out" ? ufl3("split.heldOut") : ufl3("split.public")}</span>
							{v === "held_out" && <span className="text-muted-foreground block">{ufl3("split.heldOutHelp")}</span>}
						</span>
					</label>
				))}
			</fieldset>

			{spec.arms.length === 1 && (
				<FailoverSettings
					arm={spec.arms[0]}
					value={spec.failover}
					onChange={(failover) => {
						const { failover: _old, ...rest } = spec;
						onChange(failover ? { ...rest, failover } : rest);
					}}
				/>
			)}

			<GateSettings
				gate={spec.gate}
				onChange={(gate) => {
					const { gate: _old, ...rest } = spec;
					onChange(gate ? { ...rest, gate } : rest);
				}}
			/>
				</div>
			</details>

			<section className="bg-muted/40 rounded-md border p-3 text-sm" data-testid="step4-estimate" aria-live="polite">
				{!ready ? (
					<span className="text-muted-foreground" data-testid="step4-estimate-problem">
						{problem}
					</span>
				) : estimate.isError && !pending ? (
					// A failed estimate says so, with the server's reason when it gave one; never a stuck "Estimating..." (Q-7).
					<span className="text-muted-foreground" role="alert" data-testid="step4-estimate-error">
						{estimateFailed(estimate.error) ? te("step4.estimateFailed") : te("step4.noEstimate")}
						{estimateErrorText(estimate.error) && ` ${estimateErrorText(estimate.error)}`}
					</span>
				) : estimate.data ? (
					<EstimateSummary estimate={estimate.data} updating={estimate.isFetching || pending} />
				) : (
					<span className="text-muted-foreground" data-testid="step4-estimating">
						{te("step4.estimating")}
					</span>
				)}
			</section>
		</div>
	);
}

export default Step4Setup;