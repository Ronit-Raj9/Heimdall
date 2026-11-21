import { GroupedModelPicker } from "@/components/models/groupedModelPicker";
import { type EvaluationEstimate, type EvaluationSpec, useEstimateEvaluationQuery } from "@/lib/store/apis/evaluationsApi";

/**
 * Wizard step 4: the model for each arm, how many tasks per benchmark, trials and faults, with a
 * live estimate from POST /evaluations/estimate. The draft is the EvaluationSpec itself (A11 owns
 * the wizard state; this step only reads and writes the spec it is given).
 */

export const TASK_CHOICES = [3, 5, 8, 10, 20] as const;
export const TRIAL_CHOICES = [1, 2, 3, 5] as const;

export const FAULTS: readonly { id: string; label: string; help: string }[] = [
	{ id: "model-down", label: "Model down", help: "The model stops answering for a while, to see if the agent waits and recovers." },
	{ id: "broken-tool", label: "Broken tool", help: "Some tool calls return errors, to see if the agent notices and works around them." },
	{
		id: "misleading-instruction",
		label: "Misleading instruction",
		help: "The task comes with a wrong or stale hint, to see if the agent checks before trusting it. Skipped on benchmarks without one.",
	},
];

/** A starting spec for the wizard when no draft exists yet. */
export function defaultEvaluationSpec(): EvaluationSpec {
	return {
		name: "",
		mode: "score",
		arms: [{ label: "A", agent: "", model: "" }],
		benchmarks: [],
		tasks_per_benchmark: 8,
		trials: 2,
		faults: ["clean"],
	};
}

/** Faults with "clean" always first and present, no repeats. */
export function toggleFault(faults: string[], fault: string, on: boolean): string[] {
	const rest = faults.filter((f) => f !== "clean" && f !== fault);
	return ["clean", ...(on ? [...rest, fault] : rest)];
}

/** Whether the spec is complete enough to estimate or launch: benchmarks and a model on every arm. */
export function specReady(spec: EvaluationSpec): boolean {
	return (spec.benchmarks.length > 0 || Boolean(spec.pack)) && spec.arms.length > 0 && spec.arms.every((a) => a.model.trim() !== "");
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
export function estimateText(e: Pick<EvaluationEstimate, "runs" | "est_cost_usd" | "est_minutes">): { runs: string; cost: string; minutes: string } {
	const runs = Number(e.runs) || 0;
	const c = e.est_cost_usd;
	const cost = c?.known ? range(c.low, c.high, money) : "price not known";
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
		if (b.skipped || notes.length) out.push({ benchmark: b.benchmark, skipped: b.skipped || undefined, notes });
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
	const skipped = notes.filter((n) => n.skipped).length;
	return (
		<div className="flex flex-col gap-2">
			<span>
				About <strong data-testid="step4-est-runs">{est.runs}</strong>, <strong data-testid="step4-est-cost">{est.cost}</strong>,{" "}
				<strong data-testid="step4-est-minutes">{est.minutes}</strong>.
				{updating && <span className="text-muted-foreground"> Updating...</span>}
			</span>
			{notes.length > 0 && (
				<details data-testid="step4-est-notes">
					<summary className="text-muted-foreground hover:text-foreground cursor-pointer text-xs select-none">
						Details{skipped > 0 ? ` (${skipped} ${skipped === 1 ? "benchmark" : "benchmarks"} skipped)` : ""}
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
	const ready = specReady(spec);
	const estimate = useEstimateEvaluationQuery(spec, { skip: !ready });
	const setArm = (i: number, provider: string, model: string) =>
		onChange({ ...spec, arms: spec.arms.map((a, j) => (j === i ? { ...a, provider, model } : a)) });

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
						<GroupedModelPicker
							value={arm.model || arm.provider ? { provider: arm.provider ?? "", model: arm.model } : null}
							onChange={(v) => setArm(i, v.provider, v.model)}
							testId={`step4-model-${arm.label}`}
						/>
					</div>
				))}
			</section>

			<section className="flex flex-wrap gap-8">
				<Choices
					label="Tasks per benchmark"
					value={spec.tasks_per_benchmark}
					options={TASK_CHOICES}
					onPick={(n) => onChange({ ...spec, tasks_per_benchmark: n })}
					testId="step4-tasks"
				/>
				<Choices
					label="Trials per task"
					value={spec.trials}
					options={TRIAL_CHOICES}
					onPick={(n) => onChange({ ...spec, trials: n })}
					testId="step4-trials"
				/>
			</section>

			<section className="flex flex-col gap-2">
				<h3 className="text-base font-semibold">Faults</h3>
				<p className="text-muted-foreground text-sm">
					Every evaluation runs clean. Add faults to see how the agent copes when things go wrong.
				</p>
				{FAULTS.map((f) => (
					<label key={f.id} className="flex items-start gap-2 text-sm" data-testid={`step4-fault-${f.id}`}>
						<input
							type="checkbox"
							className="mt-1"
							checked={spec.faults.includes(f.id)}
							onChange={(e) => onChange({ ...spec, faults: toggleFault(spec.faults, f.id, e.target.checked) })}
						/>
						<span>
							<span className="font-medium">{f.label}</span>
							<span className="text-muted-foreground block">{f.help}</span>
						</span>
					</label>
				))}
			</section>

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