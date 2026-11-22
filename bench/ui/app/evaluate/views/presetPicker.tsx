import type { EstimatePreset, EvaluationEstimate, EvaluationSpec } from "@/lib/store/apis/evaluationsApi";
import i18n from "i18next";
import "./gw14I18n";
import { cn } from "@/lib/utils";

/**
 * Evaluation size presets (G4-16) and worker slots (G4-04) for wizard step 4. The presets come
 * from the estimate itself (`presets: true`), so each card shows the real runs, time, cost range
 * and smallest detectable change of this exact setup. Picking one sets tasks, trials and faults.
 */

function money(n: number): string {
	return n < 10 ? `$${n.toFixed(2)}` : `$${Math.round(n)}`;
}

/** "$1.20 to $4" or "price not known". */
export function presetCost(p: Pick<EstimatePreset, "est_cost_usd">): string {
	const c = p.est_cost_usd;
	if (!c?.known) return "price not known";
	return c.low === c.high ? money(c.low) : `${money(c.low)} to ${money(c.high)}`;
}

/** "about 20 to 45 min", "about 2 h" or "time not known". */
export function presetTime(p: Pick<EstimatePreset, "est_minutes">): string {
	const m = p.est_minutes;
	if (!m || m.known === false || (m.low <= 0 && m.high <= 0)) return "time not known";
	const fmt = (n: number) => (n >= 120 ? `${Math.round(n / 60)} h` : `${Math.max(1, Math.round(n))} min`);
	const lo = fmt(m.low);
	const hi = fmt(m.high);
	return lo === hi ? `about ${lo}` : `about ${lo} to ${hi}`;
}

/** "sees a change of about 16 points" or why it cannot. */
export function presetPower(p: Pick<EstimatePreset, "mde_pp">): string {
	return typeof p.mde_pp === "number" && Number.isFinite(p.mde_pp)
		? `sees a change of about ${Math.round(p.mde_pp)} points`
		: "too few tasks to see a change";
}

/** The preset the spec matches (tasks, trials and faults), if any. */
export function matchingPreset(spec: Pick<EvaluationSpec, "tasks_per_benchmark" | "trials" | "faults">, presets: readonly EstimatePreset[]): string | undefined {
	const same = (a: readonly string[], b: readonly string[]) => a.length === b.length && a.every((x) => b.includes(x));
	return presets.find((p) => p.tasks_per_benchmark === spec.tasks_per_benchmark && p.trials === spec.trials && same(p.faults, spec.faults))?.id;
}

export function PresetPicker({
	spec,
	presets,
	onPick,
}: {
	spec: EvaluationSpec;
	presets: readonly EstimatePreset[];
	onPick(p: EstimatePreset): void;
}) {
	if (presets.length === 0) return null;
	const current = matchingPreset(spec, presets);
	return (
		<section className="flex flex-col gap-2" data-testid="step4-presets">
			<h3 className="text-base font-semibold">Size</h3>
			<p className="text-muted-foreground text-sm">Start small for a first answer; more tasks see smaller changes.</p>
			<div className="grid gap-3 sm:grid-cols-3" role="radiogroup" aria-label="Size">
				{presets.map((p) => {
					const on = current === p.id;
					return (
						<button
							key={p.id}
							type="button"
							role="radio"
							aria-checked={on}
							onClick={() => onPick(p)}
							className={cn(
								"flex flex-col gap-1 rounded-lg border p-3 text-left text-sm transition-colors",
								on ? "border-primary bg-primary/10" : "border-input bg-background hover:bg-muted",
							)}
							data-testid={`step4-preset-${p.id}`}
						>
							<span className={cn("font-medium", on && "text-primary")}>{p.label}</span>
							<span className="text-muted-foreground text-xs">
								{p.tasks_per_benchmark} tasks, {p.trials === 1 ? "once each" : `${p.trials} times each`}
								{p.faults.length > 1 ? `, ${p.faults.length - 1} ${p.faults.length === 2 ? "fault" : "faults"}` : ""}
							</span>
							<span className="tabular-nums" data-testid={`step4-preset-${p.id}-facts`}>
								{p.runs} {p.runs === 1 ? "run" : "runs"}, {presetTime(p)}, {presetCost(p)}
							</span>
							<span className="text-muted-foreground text-xs">{presetPower(p)}</span>
						</button>
					);
				})}
			</div>
		</section>
	);
}

/**
 * "Up to 8 runs at once (4 per benchmark; your organization has 4 worker slots)." or, when the estimate
 * knows the worker's slots (N90), "Up to 2 runs at once: the worker this evaluation uses runs 2 at a time."
 */
export function slotsText(e: Pick<EvaluationEstimate, "slots">): string | undefined {
	const s = e.slots;
	if (!s || !s.parallel) return undefined;
	const t = i18n.getFixedT(null, "gw14");
	if (s.worker_slots && s.worker_slots > 0) return t("slots.worker", { count: s.parallel, n: s.worker_slots });
	const org = s.org_slots > 0 ? t("slots.orgSlots", { count: s.org_slots }) : "";
	return t("slots.upTo", { count: s.parallel, detail: `${t("slots.perBenchmark", { count: s.concurrency })}${org}` });
}

export const CONCURRENCY_CHOICES = [1, 2, 4, 8] as const;
