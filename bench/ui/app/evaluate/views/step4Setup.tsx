import { useState } from "react";
import { GroupedModelPicker } from "@/components/models/groupedModelPicker";
import { FailoverSettings, failoverProblem } from "@/components/evaluations/failoverSettings";
import { GateSettings } from "@/components/releases/gateSettings";
import {
	type EvaluationEstimate,
	type EvaluationMCPRoute,
	type EvaluationSpec,
	type FaultCatalogItem,
	useEstimateEvaluationQuery,
	useGetBenchFaultsQuery,
} from "@/lib/store/apis/evaluationsApi";
import { armHasModel, useOwnModelAgents } from "./ownModel";
import { CONCURRENCY_CHOICES, PresetPicker, slotsText } from "./presetPicker";

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

/** The power line: "These settings can detect a change of about X points overall (Y per benchmark)." */
export function powerText(e: Pick<EvaluationEstimate, "power">): { line: string; advice: string } | undefined {
	const p = e.power;
	if (!p) return undefined;
	const overall = p.overall?.mde_pp;
	const per = (p.per_benchmark ?? []).map((b) => b.mde_pp).filter((x): x is number => typeof x === "number" && Number.isFinite(x));
	const perText =
		(p.per_benchmark ?? []).length === 0
			? ""
			: per.length === 0
				? " (too few tasks to decide per benchmark)"
				: ` (${Math.round(Math.max(...per))} per benchmark)`;
	const line =
		typeof overall === "number" && Number.isFinite(overall)
			? `These settings can detect a change of about ${Math.round(overall)} points overall${perText}.`
			: "These settings have too few tasks to detect a change.";
	return { line, advice: p.advice ?? "" };
}

/** A starting spec for the wizard when no draft exists yet. */
export function defaultEvaluationSpec(): EvaluationSpec {
	return {
		name: "",
		mode: "score",
		arms: [{ label: "A", agent: "", model: "" }],
		benchmarks: [],
		tasks_per_benchmark: 10,
		trials: 1,
		faults: ["clean"],
		preset: "quick",
	};
}

/** The MCP routes step 4 offers, in order. */
export const MCP_ROUTES: { id: EvaluationMCPRoute; label: string; help: string }[] = [
	{ id: "direct", label: "Direct", help: "The agent calls the task's MCP servers through Heimdall's tool proxy." },
	{
		id: "bifrost",
		label: "Through Bifrost MCP gateway",
		help: "Each run gets its own Bifrost MCP clients and MCP key, so every tool call shows in Bifrost's MCP logs. Tool faults still apply: the tool proxy sits behind Bifrost.",
	},
	{
		id: "bifrost_code_mode",
		label: "Through Bifrost, Code Mode",
		help: "As above, but the agent sees Bifrost's Code Mode tools and writes programs that call the servers.",
	},
];

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
 * Whether the spec is complete enough to estimate or launch: benchmarks and a model on every arm. An
 * arm on an agent in `owns` (it brings its own model) may leave the model blank.
 */
export function specReady(spec: EvaluationSpec, owns: ReadonlySet<string> = new Set()): boolean {
	return (
		(spec.benchmarks.length > 0 || Boolean(spec.pack)) &&
		spec.arms.length > 0 &&
		spec.arms.every((a) => armHasModel(a, owns)) &&
		failoverProblem(spec.arms[0], spec.failover) === ""
	);
}

function money(n: number): string {
	return n < 10 ? `$${n.toFixed(2)}` : `$${Math.round(n)}`;
}

function range(low: number, high: number, fmt: (n: number) => string): string {
	return low === high ? fmt(low) : `${fmt(low)} to ${fmt(high)}`;
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
	const cost = c?.known ? range(c.low, c.high, money) : c?.reason ? `price not known (${c.reason})` : "price not known";
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

/** Per-benchmark skips and notes in plain words, plus the estimate's own notes under "". Empty ones are left out. */
export function estimateNotes(e: Pick<EvaluationEstimate, "per_benchmark" | "notes">): EstimateNote[] {
	const out: EstimateNote[] = [];
	const general = (e.notes ?? []).filter(Boolean);
	if (general.length) out.push({ benchmark: "", notes: general });
	for (const b of e.per_benchmark ?? []) {
		const notes = [...(b.notes ?? []), ...(b.faults_skipped ?? []).map((f) => `${f.fault} is skipped: ${f.reason}`)].filter(Boolean);
		if (b.skipped || notes.length)
			out.push({
				benchmark: b.benchmark,
				skipped: b.skipped || undefined,
				notes,
			});
	}
	return out;
}

/** Whether a failed estimate is a real HTTP error (a status code), not a missing answer. */
export function estimateFailed(error: unknown): boolean {
	return Boolean(error && typeof error === "object" && "status" in error && typeof (error as { status: unknown }).status === "number");
}

/** The estimate result: runs, cost and time, then the per-benchmark notes collapsed. */
export function EstimateSummary({ estimate, updating }: { estimate: EvaluationEstimate; updating?: boolean }) {
	const est = estimateText(estimate);
	const notes = estimateNotes(estimate);
	const power = powerText(estimate);
	const slots = slotsText(estimate);
	const skipped = notes.filter((n) => n.skipped).length;
	return (
		<div className="flex flex-col gap-2">
			{estimate.from_library && (
				<span className="text-chart-info-ink text-xs font-medium" data-testid="step4-from-library">
					First run, estimate from library data: other agents on the same tasks. Your agent may differ.
				</span>
			)}
			<span>
				About <strong data-testid="step4-est-runs">{est.runs}</strong>, <strong data-testid="step4-est-cost">{est.cost}</strong>,{" "}
				<strong data-testid="step4-est-minutes">{est.minutes}</strong>.
				{updating && <span className="text-muted-foreground"> Updating...</span>}
			</span>
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
				<details data-testid="step4-est-notes">
					<summary className="text-muted-foreground hover:text-foreground cursor-pointer text-xs select-none">
						Details
						{skipped > 0 ? ` (${skipped} ${skipped === 1 ? "benchmark" : "benchmarks"} skipped)` : ""}
					</summary>
					<ul className="mt-1.5 flex flex-col gap-1.5 text-xs">
						{notes.map((n) => (
							<li key={n.benchmark || "_all"} data-testid={`step4-est-note-${n.benchmark || "all"}`}>
								{n.benchmark && <span className="font-medium">{n.benchmark}: </span>}
								{n.skipped && <span>skipped, {n.skipped}. </span>}
								{n.notes.length > 0 && <span className="text-muted-foreground">{n.notes.join(". ")}</span>}
							</li>
						))}
					</ul>
				</details>
			)}
		</div>
	);
}

const chip = "rounded-md border px-3 py-1 text-sm";
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
	const owns = useOwnModelAgents();
	const [picking, setPicking] = useState<Set<string>>(() => new Set());
	const ready = specReady(spec, owns);
	const estimate = useEstimateEvaluationQuery({ ...spec, presets: true }, { skip: !ready });
	const catalog = useGetBenchFaultsQuery();
	const faultGroups = groupFaults(catalog.data && catalog.data.length > 0 ? catalog.data : FAULTS);
	const setArm = (i: number, provider: string, model: string) =>
		onChange({
			...spec,
			arms: spec.arms.map((a, j) => (j === i ? { ...a, provider, model } : a)),
		});

	return (
		<div className="flex flex-col gap-6" data-testid="step4-setup">
			<section className="flex flex-col gap-3">
				<h3 className="text-base font-semibold">Model</h3>
				{spec.arms.map((arm, i) => (
					<div key={`${arm.label}-${i}`} className="flex flex-col gap-1.5" data-testid={`step4-arm-${arm.label}`}>
						<span className="text-sm font-medium">
							{spec.arms.length > 1 ? `${arm.label}: ` : ""}
							{arm.agent || "Your agent"}
							{arm.version ? ` v${arm.version}` : ""} uses
						</span>
						{owns.has(arm.agent) && !arm.model && !picking.has(arm.label) ? (
							<div className="bg-muted/40 flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm" data-testid={`step4-own-model-${arm.label}`}>
								<span>
									<span className="font-medium">Its own model.</span>{" "}
									<span className="text-muted-foreground">This agent picks its model itself; cost is what the agent reports.</span>
								</span>
								<button type="button" className="text-primary text-xs hover:underline" onClick={() => setPicking((p) => new Set(p).add(arm.label))} data-testid={`step4-pick-model-${arm.label}`}>
									Pick a model instead
								</button>
							</div>
						) : (
							<>
								<GroupedModelPicker
									value={arm.model || arm.provider ? { provider: arm.provider ?? "", model: arm.model } : null}
									onChange={(v) => setArm(i, v.provider, v.model)}
									testId={`step4-model-${arm.label}`}
								/>
								{owns.has(arm.agent) && (
									<button type="button" className="text-primary w-fit text-xs hover:underline" onClick={() => {
											setPicking((p) => {
												const n = new Set(p);
												n.delete(arm.label);
												return n;
											});
											setArm(i, "", "");
										}} data-testid={`step4-use-own-${arm.label}`}>
										Use the agent's own model
									</button>
								)}
							</>
						)}
					</div>
				))}
			</section>

			<PresetPicker
				spec={spec}
				presets={estimate.data?.presets ?? []}
				onPick={(p) => onChange({ ...spec, tasks_per_benchmark: p.tasks_per_benchmark, trials: p.trials, faults: p.faults, preset: p.id })}
			/>

			<section className="flex flex-wrap gap-8">
				<Choices
					label="Tasks per benchmark"
					value={spec.tasks_per_benchmark}
					options={TASK_CHOICES}
					onPick={(n) => onChange({ ...spec, tasks_per_benchmark: n, preset: undefined })}
					testId="step4-tasks"
				/>
				<Choices
					label="Trials per task"
					value={spec.trials}
					options={TRIAL_CHOICES}
					onPick={(n) => onChange({ ...spec, trials: n, preset: undefined })}
					testId="step4-trials"
				/>
				<Choices
					label="Runs at once per benchmark"
					value={spec.concurrency ?? 4}
					options={CONCURRENCY_CHOICES}
					onPick={(n) => onChange({ ...spec, concurrency: n })}
					testId="step4-concurrency"
				/>
			</section>

			<section className="flex flex-col gap-2">
				<h3 className="text-base font-semibold">Faults</h3>
				<p className="text-muted-foreground text-sm">
					Every evaluation runs clean. Add faults to see how the agent copes when things go wrong.
				</p>
				{faultGroups.map((g) => (
					<fieldset key={g.group} className="flex flex-col gap-2" data-testid={`step4-fault-group-${g.group}`}>
						<legend className="text-muted-foreground mb-1 text-xs font-medium tracking-wide uppercase">{g.group}</legend>
						{g.faults.map((f) => (
							<label key={f.id} className="flex items-start gap-2 text-sm" data-testid={`step4-fault-${f.id}`}>
								<input
									type="checkbox"
									className="mt-1"
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
							className="mt-1"
							checked={Boolean(spec.bifrost_cache)}
							onChange={(e) => {
								const { bifrost_cache: _old, ...rest } = spec;
								onChange(e.target.checked ? { ...rest, bifrost_cache: true } : rest);
							}}
						/>
						<span>
							<span className="font-medium">Bifrost cache on vs off</span>
							<span className="text-muted-foreground block">
								Runs your agent twice, identical except that Bifrost&apos;s semantic cache is on for the second copy. The report shows the cache hit
								rate, the latency and cost saved, and any change in success. Doubles the runs.
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

			<section className="bg-muted/40 rounded-md border p-3 text-sm" data-testid="step4-estimate" aria-live="polite">
				{!ready ? (
					<span className="text-muted-foreground">Pick benchmarks and a model to see an estimate.</span>
				) : estimate.isError && estimateFailed(estimate.error) ? (
					<span className="text-muted-foreground">Could not estimate this evaluation right now.</span>
				) : estimate.data ? (
					<EstimateSummary estimate={estimate.data} updating={estimate.isFetching} />
				) : estimate.isError ? (
					<span className="text-muted-foreground">No estimate yet.</span>
				) : (
					<span className="text-muted-foreground">Estimating...</span>
				)}
			</section>
		</div>
	);
}

export default Step4Setup;