import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { EvaluationGate, EvaluationGateSlice } from "@/lib/store/apis/evaluationsApi";
import { Plus, Trash2 } from "lucide-react";

/** Capabilities a minimum score can name; "all" is every benchmark pooled. */
export const GATE_CAPABILITIES = ["all", "terminal", "coding", "memory", "support", "research", "voice", "safety"];

const DEFAULT_GATE: EvaluationGate = { max_regression: 0.05 };

/** Percent text for a fraction, "" for none. */
const pctText = (x: number | undefined) => (x === undefined ? "" : String(Math.round(x * 1000) / 10));
const fromPct = (s: string) => (s.trim() === "" ? undefined : Math.min(1, Math.max(0, Number(s) / 100)));

const SLICE_KEY = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/;

/** What is wrong with a segment rule, the same checks as the server; "" when it is fine. */
export function gateSliceError(s: EvaluationGateSlice): string {
	if (!SLICE_KEY.test(s.key)) return "Key: letters, digits, _ . - (up to 64, starting with a letter or digit).";
	if (s.value.length < 1 || s.value.length > 200) return "Value: 1 to 200 characters.";
	const bad = (x: number | undefined) => x !== undefined && !(x >= 0 && x <= 1);
	if (bad(s.min_score) || bad(s.max_regression)) return "Numbers must be between 0 and 100.";
	if (s.min_score === undefined && s.max_regression === undefined) return "Set a lowest score, a largest drop, or both.";
	return "";
}

/** Segment keys and values from a report, offered as suggestions. */
export interface GateSegmentSuggestions {
	[key: string]: string[];
}

/**
 * The release gate of an evaluation: compared with the live version (or a picked one), no capability
 * may drop by more than the limit, and each listed capability needs at least its score. Checked
 * automatically when the evaluation finishes; the result shows on its report card.
 */
export function GateSettings({
	gate,
	onChange,
	segments,
}: {
	gate?: EvaluationGate;
	onChange(gate: EvaluationGate | undefined): void;
	segments?: GateSegmentSuggestions;
}) {
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
					<GateSliceRules
						slices={gate.slices ?? []}
						segments={segments}
						onChange={(slices) => onChange({ ...gate, slices: slices.length ? slices : undefined })}
					/>
				</div>
			)}
		</section>
	);
}

/** Rules for task segments: tasks whose metadata key has a value need a lowest score or a largest drop. */
function GateSliceRules({
	slices,
	segments,
	onChange,
}: {
	slices: EvaluationGateSlice[];
	segments?: GateSegmentSuggestions;
	onChange(slices: EvaluationGateSlice[]): void;
}) {
	const set = (i: number, patch: Partial<EvaluationGateSlice>) => onChange(slices.map((s, j) => (j === i ? { ...s, ...patch } : s)));
	const keys = Object.keys(segments ?? {});
	return (
		<div className="flex flex-col gap-2" data-testid="gate-slices">
			<span className="text-muted-foreground text-xs">Rules per segment (tasks whose metadata key has a value)</span>
			{keys.length > 0 && (
				<datalist id="gate-slice-keys">
					{keys.map((k) => (
						<option key={k} value={k} />
					))}
				</datalist>
			)}
			{slices.map((s, i) => {
				const err = gateSliceError(s);
				const values = segments?.[s.key] ?? [];
				return (
					<div key={i} className="flex flex-col gap-1" data-testid={`gate-slice-${i}`}>
						<div className="flex flex-wrap items-center gap-2">
							<Input
								aria-label="Key"
								placeholder="key, e.g. tier"
								className="w-36"
								list={keys.length ? "gate-slice-keys" : undefined}
								value={s.key}
								onChange={(e) => set(i, { key: e.target.value.trim() })}
							/>
							<Input
								aria-label="Value"
								placeholder="value, e.g. enterprise"
								className="w-40"
								list={values.length ? `gate-slice-values-${i}` : undefined}
								value={s.value}
								onChange={(e) => set(i, { value: e.target.value })}
							/>
							{values.length > 0 && (
								<datalist id={`gate-slice-values-${i}`}>
									{values.map((v) => (
										<option key={v} value={v} />
									))}
								</datalist>
							)}
							<Input
								type="number"
								min={0}
								max={100}
								step="any"
								aria-label="Lowest score (%)"
								placeholder="lowest %"
								className="w-28"
								value={pctText(s.min_score)}
								onChange={(e) => set(i, { min_score: fromPct(e.target.value) })}
							/>
							<Input
								type="number"
								min={0}
								max={100}
								step="any"
								aria-label="Largest drop (points)"
								placeholder="drop pts"
								className="w-28"
								value={pctText(s.max_regression)}
								onChange={(e) => set(i, { max_regression: fromPct(e.target.value) })}
							/>
							<Button type="button" size="sm" variant="ghost" aria-label="Remove" onClick={() => onChange(slices.filter((_, j) => j !== i))}>
								<Trash2 className="size-4" aria-hidden="true" />
							</Button>
						</div>
						{err && (
							<p className="text-destructive text-xs" role="alert" data-testid={`gate-slice-error-${i}`}>
								{err}
							</p>
						)}
					</div>
				);
			})}
			<Button
				type="button"
				size="sm"
				variant="outline"
				className="w-fit"
				onClick={() => onChange([...slices, { key: keys[0] ?? "", value: segments?.[keys[0]]?.[0] ?? "", min_score: 0.5 }])}
				data-testid="gate-add-slice"
			>
				<Plus className="size-4" aria-hidden="true" />
				Add a segment rule
			</Button>
		</div>
	);
}
