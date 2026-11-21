import { Button } from "@/components/ui/button";
import { type EvaluationArm, type EvaluationSpec, useCreateEvaluationMutation } from "@/lib/store/apis/evaluationsApi";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { OWN_MODEL_LABEL, useOwnModelAgents } from "./ownModel";
import { FAULTS, specReady } from "./step4Setup";

/** Wizard step 5: a plain summary of the evaluation, an optional budget and the Launch button. */

function armText(a: EvaluationArm): string {
	const agent = a.agent || "your agent";
	const version = a.version ? ` v${a.version}` : "";
	const model = a.model ? ` on ${a.provider ? `${a.provider}/` : ""}${a.model}` : a.agent ? ` on ${OWN_MODEL_LABEL}` : "";
	return `${agent}${version}${model}`;
}

/** One sentence per fact, in plain English, describing what the evaluation will do. */
export function summaryLines(spec: EvaluationSpec): string[] {
	const [a, b] = spec.arms;
	const lines: string[] = [];
	if (spec.mode === "score" || !b) lines.push(`Score ${a ? armText(a) : "your agent"}.`);
	else if (spec.mode === "versions") lines.push(`Compare two versions: ${armText(a)} against ${armText(b)}.`);
	else if (spec.mode === "models") lines.push(`Compare two models: ${armText(a)} against ${armText(b)}.`);
	else lines.push(`Compare ${armText(a)} against the baseline ${armText(b)}.`);

	const n = spec.benchmarks.length;
	if (n > 0) lines.push(`Run ${n} ${n === 1 ? "benchmark" : "benchmarks"}: ${spec.benchmarks.join(", ")}.`);
	else if (spec.pack) lines.push(`Run the ${spec.pack} pack of benchmarks.`);

	const trials = spec.trials === 1 ? "once" : `${spec.trials} times`;
	lines.push(`Use ${spec.tasks_per_benchmark} tasks from each benchmark and try each task ${trials}.`);

	const faults = FAULTS.filter((f) => spec.faults.includes(f.id)).map((f) => f.label.toLowerCase());
	lines.push(faults.length ? `Run clean, then again with: ${faults.join(", ")}.` : "Run clean only, with no faults.");

	if (spec.budget_usd && spec.budget_usd > 0) lines.push(`Stop when spending reaches $${spec.budget_usd}.`);
	return lines;
}

/** Budget box text to a spec value: blank or not a positive number means no budget. */
export function parseBudget(text: string): number | undefined {
	const n = Number(text);
	return text.trim() !== "" && Number.isFinite(n) && n > 0 ? n : undefined;
}

function errorMessage(error: unknown): string {
	const data = (error as { data?: { error?: { message?: string } | string } } | undefined)?.data;
	if (typeof data?.error === "string") return data.error;
	return data?.error?.message ?? "Could not launch the evaluation.";
}

export interface Step5LaunchProps {
	spec: EvaluationSpec;
	onChange(spec: EvaluationSpec): void;
}

export function Step5Launch({ spec, onChange }: Step5LaunchProps) {
	const navigate = useNavigate();
	const [create, state] = useCreateEvaluationMutation();
	const [budget, setBudget] = useState(spec.budget_usd ? String(spec.budget_usd) : "");
	const ready = specReady(spec, useOwnModelAgents());

	const launch = async () => {
		const body: EvaluationSpec = { ...spec, name: spec.name.trim() || summaryLines(spec)[0].replace(/\.$/, "") };
		const res = await create(body);
		if ("data" in res && res.data) void navigate({ href: `/evaluations/${encodeURIComponent(res.data.id)}` });
	};

	return (
		<div className="flex flex-col gap-5" data-testid="step5-launch">
			<section className="flex flex-col gap-2">
				<h3 className="text-base font-semibold">What will happen</h3>
				<ul className="list-disc space-y-1 pl-5 text-sm" data-testid="step5-summary">
					{summaryLines(spec).map((line) => (
						<li key={line}>{line}</li>
					))}
				</ul>
			</section>

			<section className="flex flex-col gap-1.5">
				<label htmlFor="step5-budget" className="text-sm font-medium">
					Budget (USD, optional)
				</label>
				<input
					id="step5-budget"
					type="number"
					min={0}
					step="0.5"
					inputMode="decimal"
					className="border-input bg-background h-8 w-32 rounded-md border px-2 text-sm"
					placeholder="No limit"
					value={budget}
					onChange={(e) => {
						setBudget(e.target.value);
						onChange({ ...spec, budget_usd: parseBudget(e.target.value) });
					}}
					data-testid="step5-budget"
				/>
				<span className="text-muted-foreground text-xs">The evaluation stops when model spending reaches this amount.</span>
			</section>

			{state.error ? (
				<p className="text-chart-error-ink text-sm" role="alert" data-testid="step5-error">
					{errorMessage(state.error)}
				</p>
			) : null}

			<div>
				<Button onClick={() => void launch()} disabled={!ready || state.isLoading} data-testid="step5-launch-button">
					{state.isLoading ? "Launching..." : "Launch evaluation"}
				</Button>
				{!ready && <p className="text-muted-foreground mt-1 text-xs">Pick benchmarks and a model for every arm first.</p>}
			</div>
		</div>
	);
}

export default Step5Launch;