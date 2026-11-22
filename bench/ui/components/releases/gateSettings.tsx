import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { EvaluationGate } from "@/lib/store/apis/evaluationsApi";
import { Plus, Trash2 } from "lucide-react";
import { tasksFor } from "./gateSize";

/** Capabilities a minimum score can name; "all" is every benchmark pooled. */
export const GATE_CAPABILITIES = ["all", "terminal", "coding", "memory", "support", "research", "voice", "safety"];

const DEFAULT_GATE: EvaluationGate = { max_regression: 0.05 };

/** Percent text for a fraction, "" for none. */
const pctText = (x: number | undefined) => (x === undefined ? "" : String(Math.round(x * 1000) / 10));
const fromPct = (s: string) => (s.trim() === "" ? undefined : Math.min(1, Math.max(0, Number(s) / 100)));

/** How many paired tasks a drop limit needs before a drop that size can be told from noise. */
export function sizeHint(limit: number | undefined): string {
	if (!limit) return "";
	const pts = Math.round(limit * 1000) / 10;
	const n = tasksFor(limit);
	const need = n ? `about ${n} tasks` : "more tasks than any benchmark has";
	return `The gate fails only on a real drop larger than ${pts} points. Telling a ${pts} point drop from noise needs ${need} per capability; with fewer, a drop that size reads as inconclusive.`;
}

/**
 * The release gate of an evaluation: compared with the live version (or a picked one), no capability
 * may drop by more than the limit, and each listed capability needs at least its score. Checked
 * automatically when the evaluation finishes; the result shows on its report card.
 */
export function GateSettings({ gate, onChange }: { gate?: EvaluationGate; onChange(gate: EvaluationGate | undefined): void }) {
	const on = gate !== undefined;
	const mins = Object.entries(gate?.min_score ?? {});
	const setMins = (entries: [string, number][]) => onChange({ ...gate, min_score: entries.length ? Object.fromEntries(entries) : undefined });
	return (
		<section className="flex flex-col gap-3" data-testid="gate-settings">
			<label className="flex items-start gap-2 text-sm">
				<input type="checkbox" className="mt-1" checked={on} onChange={(e) => onChange(e.target.checked ? DEFAULT_GATE : undefined)} data-testid="gate-enable" />
				<span>
					<span className="font-medium">Release gate</span>
					<span className="text-muted-foreground block">
						Check the result against the version that is live now when the evaluation finishes. A failed gate blocks promoting this version
						unless someone gives a reason.
					</span>
				</span>
			</label>
			{on && (
				<div className="flex flex-col gap-3 pl-6">
					<div className="grid gap-3 sm:grid-cols-2">
						<label className="text-muted-foreground flex flex-col gap-1 text-xs">
							Largest drop allowed on any capability (points)
							<Input
								type="number"
								min={0}
								max={100}
								step="any"
								value={pctText(gate.max_regression)}
								onChange={(e) => onChange({ ...gate, max_regression: fromPct(e.target.value) })}
								data-testid="gate-max-regression"
							/>
						</label>
						<label className="text-muted-foreground flex flex-col gap-1 text-xs">
							Compare with version (empty: the live version)
							<Input
								type="number"
								min={1}
								value={gate.baseline_version ?? ""}
								onChange={(e) => onChange({ ...gate, baseline_version: e.target.value === "" ? undefined : Math.max(1, Math.round(Number(e.target.value))) })}
								data-testid="gate-baseline-version"
							/>
						</label>
					</div>
					{sizeHint(gate.max_regression) && (
						<p className="text-muted-foreground text-xs" data-testid="gate-size-hint">
							{sizeHint(gate.max_regression)}
						</p>
					)}
					<div className="flex flex-col gap-2">
						<span className="text-muted-foreground text-xs">Lowest score needed per capability</span>
						{mins.map(([cap, v], i) => (
							<div key={`${cap}-${i}`} className="flex items-center gap-2" data-testid={`gate-min-${i}`}>
								<select
									className="border-input bg-background h-9 rounded-md border px-2 text-sm"
									aria-label="Capability"
									value={cap}
									onChange={(e) => setMins(mins.map(([c, x], j) => (j === i ? [e.target.value, x] : [c, x])))}
								>
									{GATE_CAPABILITIES.map((c) => (
										<option key={c} value={c} disabled={c !== cap && mins.some(([m]) => m === c)}>
											{c === "all" ? "all benchmarks" : c}
										</option>
									))}
								</select>
								<Input
									type="number"
									min={0}
									max={100}
									step="any"
									aria-label="Lowest score (%)"
									className="w-28"
									value={pctText(v)}
									onChange={(e) => setMins(mins.map(([c, x], j) => (j === i ? [c, fromPct(e.target.value) ?? 0] : [c, x])))}
								/>
								<span className="text-muted-foreground text-xs">%</span>
								<Button type="button" size="sm" variant="ghost" aria-label="Remove" onClick={() => setMins(mins.filter((_, j) => j !== i))}>
									<Trash2 className="size-4" aria-hidden="true" />
								</Button>
							</div>
						))}
						{mins.length < GATE_CAPABILITIES.length && (
							<Button
								type="button"
								size="sm"
								variant="outline"
								className="w-fit"
								onClick={() => setMins([...mins, [GATE_CAPABILITIES.find((c) => !mins.some(([m]) => m === c)) ?? "all", 0.5]])}
								data-testid="gate-add-min"
							>
								<Plus className="size-4" aria-hidden="true" />
								Add a minimum score
							</Button>
						)}
					</div>
					<label className="flex items-start gap-2 text-sm">
						<input
							type="checkbox"
							className="mt-1"
							checked={Boolean(gate.allow_uncalibrated_judge)}
							onChange={(e) => onChange({ ...gate, allow_uncalibrated_judge: e.target.checked || undefined })}
							data-testid="gate-allow-uncalibrated"
						/>
						<span>
							Count minimum scores on benchmarks graded by a judge that is not calibrated
							<span className="text-muted-foreground block text-xs">
								Off: such a minimum is inconclusive until the judge is calibrated on its controls or on human labels.
							</span>
						</span>
					</label>
					<label className="flex items-start gap-2 text-sm">
						<input
							type="checkbox"
							className="mt-1"
							checked={Boolean(gate.fail_on_inconclusive)}
							onChange={(e) => onChange({ ...gate, fail_on_inconclusive: e.target.checked || undefined })}
							data-testid="gate-fail-inconclusive"
						/>
						<span>
							Block promoting when the gate is inconclusive
							<span className="text-muted-foreground block text-xs">
								Inconclusive: a drop over the limit that could be noise at this size, or a minimum on an uncalibrated judge.
							</span>
						</span>
					</label>
				</div>
			)}
		</section>
	);
}
