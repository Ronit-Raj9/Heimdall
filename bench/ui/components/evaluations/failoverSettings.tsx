import { GroupedModelPicker } from "@/components/models/groupedModelPicker";
import type { EvaluationArm, EvaluationFailover, FailoverReport } from "@/lib/store/apis/evaluationsApi";

/** The provider an arm's model is served through: its provider, else the "<provider>/<model>" prefix. */
export function armProvider(arm: EvaluationArm | undefined): string {
	if (!arm) return "";
	if (arm.provider) return arm.provider;
	const i = arm.model.indexOf("/");
	return i > 0 ? arm.model.slice(0, i) : "";
}

/** Why the failover option cannot run as set, in plain words; "" when it can. */
export function failoverProblem(arm: EvaluationArm | undefined, f: EvaluationFailover | undefined): string {
	if (!f?.enabled) return "";
	const provider = armProvider(arm);
	if (!arm?.model || !provider) return "Pick the agent's model first; failover needs to know its provider.";
	const fb = f.fallbacks[0];
	if (!fb?.model) return "Pick a fallback model.";
	if (fb.provider && fb.provider !== provider) return `The fallback must be on ${provider}, the same provider as the agent's model.`;
	if (fb.model === arm.model || `${provider}/${fb.model}` === arm.model) return "The fallback must be a different model.";
	return "";
}

export interface FailoverSettingsProps {
	arm: EvaluationArm | undefined;
	value: EvaluationFailover | undefined;
	onChange(f: EvaluationFailover | undefined): void;
}

/** Wizard step 4: the "Provider failover" toggle and fallback model picker (one agent only). */
export function FailoverSettings({ arm, value, onChange }: FailoverSettingsProps) {
	const on = Boolean(value?.enabled);
	const fb = value?.fallbacks[0];
	const problem = failoverProblem(arm, value);
	return (
		<section className="flex flex-col gap-2" data-testid="step4-failover">
			<h3 className="text-base font-semibold">Provider failover</h3>
			<label className="flex items-start gap-2 text-sm">
				<input
					type="checkbox"
					className="mt-1"
					checked={on}
					onChange={(e) => onChange(e.target.checked ? { enabled: true, fallbacks: value?.fallbacks ?? [] } : undefined)}
					data-testid="step4-failover-toggle"
				/>
				<span>
					<span className="font-medium">Test Bifrost fallbacks</span>
					<span className="text-muted-foreground block">
						Adds a run of the agent with a Bifrost fallback model, and takes the primary model down. The report shows if failover worked,
						the extra time and cost, and task success with and without it.
					</span>
				</span>
			</label>
			{on && (
				<div className="flex flex-col gap-1.5 pl-6">
					<span className="text-sm font-medium">Fallback model (same provider)</span>
					<GroupedModelPicker
						value={fb ? { provider: fb.provider ?? "", model: fb.model } : null}
						onChange={(v) => onChange({ enabled: true, fallbacks: [{ provider: v.provider, model: v.model }] })}
						testId="step4-failover-model"
						providers={armProvider(arm) ? [armProvider(arm)] : []}
					/>
					{problem && (
						<p className="text-destructive text-xs" data-testid="step4-failover-problem">
							{problem}
						</p>
					)}
				</div>
			)}
		</section>
	);
}

const ms = (v: number) => `${v >= 0 ? "+" : ""}${(v / 1000).toFixed(1)} s`;
const usd = (v: number) => `${v >= 0 ? "+" : "-"}$${Math.abs(v).toFixed(4)}`;
const yes = (b: boolean) => (b ? "passed" : "failed");

/** The "Provider failover" section of the evaluation page and report card. */
export function FailoverSection({ report }: { report: FailoverReport }) {
	const per = report.per_run ? " per run" : " per call";
	return (
		<section className="flex flex-col gap-2 rounded-md border p-3 text-sm" data-testid="failover-section">
			<h3 className="text-base font-semibold">Provider failover</h3>
			<p>{report.summary}</p>
			<dl className="grid grid-cols-2 gap-x-4 gap-y-1 sm:grid-cols-4">
				<dt className="text-muted-foreground">Failover worked</dt>
				<dd data-testid="failover-worked">{report.failover_worked ? "Yes" : "No"}</dd>
				<dt className="text-muted-foreground">Extra latency{per}</dt>
				<dd>{ms(report.extra_latency_ms)}</dd>
				<dt className="text-muted-foreground">Cost change{per}</dt>
				<dd>{report.cost_change_usd == null ? "Not known" : usd(report.cost_change_usd)}</dd>
				<dt className="text-muted-foreground">Task with / without failover</dt>
				<dd data-testid="failover-success">
					{yes(report.success_with_failover)} / {yes(report.success_without_failover)}
				</dd>
			</dl>
		</section>
	);
}

export default FailoverSettings;