import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { OnboardInventory, OnboardMatch, OnboardPlan } from "@/lib/types/onboard";
import { AlertTriangle } from "lucide-react";
import { useTranslation } from "react-i18next";

const selectClass = "border-input bg-background h-9 rounded-md border px-2 text-sm";
const MAP_FIELDS = ["question", "expected", "rubric", "wrong_answers"] as const;

/** The detected matches as chips; a click reads the input as that source. */
export function MatchChips({ matches, active, onPick }: { matches: OnboardMatch[]; active?: string; onPick(source: string): void }) {
	const { t } = useTranslation("onboard");
	if (matches.length === 0) return null;
	return (
		<div className="flex flex-wrap items-center gap-2" data-testid="onboard-matches">
			<span className="text-muted-foreground text-xs">{matches.length > 1 ? t("source.pick") : t("source.found")}</span>
			{matches.map((m) => (
				<button
					key={m.source}
					type="button"
					onClick={() => onPick(m.source)}
					className="cursor-pointer"
					title={m.note}
					data-testid={`onboard-match-${m.source}`}
				>
					<Badge variant={m.source === active ? "default" : "outline"}>{m.label}</Badge>
				</button>
			))}
		</div>
	);
}

/** Step 2: what was found, and the plan the person edits before drafting. */
export function InventoryPanel({
	inv,
	plan,
	onPlan,
	onDraft,
	busy,
}: {
	inv: OnboardInventory;
	plan: OnboardPlan;
	onPlan(p: OnboardPlan): void;
	onDraft(): void;
	busy: boolean;
}) {
	const { t } = useTranslation("onboard");
	const tx = (k: string, o?: Record<string, unknown>) => t(k as never, o as never) as unknown as string;
	const rows = inv.kind === "rows" || inv.kind === "hf_dataset";
	const sampleKeys = Array.from(new Set(inv.samples.flatMap((s) => Object.keys(s.fields)))).slice(0, 4);
	const mappable = rows && (inv.columns?.length ?? 0) > 0;
	const set = (patch: Partial<OnboardPlan>) => onPlan({ ...plan, ...patch });
	return (
		<div className="flex min-w-0 flex-col gap-4" data-testid="onboard-inventory">
			<div className="flex flex-wrap items-center gap-2 text-sm">
				<span className="font-semibold">{inv.title}</span>
				<Badge variant="outline">{inv.kind}</Badge>
				<span>{tx("found.units", { count: inv.units, unit: inv.unit })}</span>
				<span className="text-muted-foreground">{tx("found.license", { license: inv.license })}</span>
			</div>
			{inv.columns && inv.columns.length > 0 && (
				<p className="text-muted-foreground text-xs" data-testid="onboard-columns">
					{t("found.columns")}: {inv.columns.join(", ")}
				</p>
			)}
			{inv.samples.length > 0 ? (
				<div className="overflow-x-auto">
					<p className="text-muted-foreground mb-1 text-xs">{t("found.samples")}</p>
					<table className="w-full min-w-[480px] text-left text-xs" data-testid="onboard-samples">
						<thead>
							<tr className="text-muted-foreground border-b">
								<th className="py-1 pr-3 font-medium" />
								{sampleKeys.map((k) => (
									<th key={k} className="py-1 pr-3 font-medium">
										{k}
									</th>
								))}
							</tr>
						</thead>
						<tbody>
							{inv.samples.map((s) => (
								<tr key={s.ref} className="border-b align-top last:border-0">
									<td className="text-muted-foreground py-1 pr-3 whitespace-nowrap">{s.ref}</td>
									{sampleKeys.map((k) => (
										<td key={k} className="max-w-[320px] py-1 pr-3 break-words">
											{s.fields[k] ?? ""}
										</td>
									))}
								</tr>
							))}
						</tbody>
					</table>
				</div>
			) : (
				<p className="text-muted-foreground text-xs">{t("found.noSamples")}</p>
			)}
			{(inv.warnings?.length ?? 0) > 0 && (
				<ul className="flex flex-col gap-1 text-xs" data-testid="onboard-warnings">
					{inv.warnings?.map((w) => (
						<li key={w} className="flex items-start gap-1.5">
							<AlertTriangle className="text-chart-warning-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
							{w}
						</li>
					))}
				</ul>
			)}
			{(inv.needs?.length ?? 0) > 0 && (
				<p className="text-xs">
					{t("found.needs")}: {inv.needs?.join("; ")}
				</p>
			)}
			<p className="text-muted-foreground text-xs" data-testid="onboard-estimate">
				{tx("found.estimate", { tasks: inv.estimate.tasks, runs: inv.estimate.readiness_runs, judge: inv.estimate.judge_calls })}
			</p>
			{!inv.can_draft && mappable && (
				<p role="note" className="text-sm" data-testid="onboard-map-first">
					{inv.next}
				</p>
			)}
			{!inv.can_draft && !mappable ? (
				<p role="note" className="border-chart-info/50 bg-chart-info/10 rounded-sm border px-3 py-2 text-sm" data-testid="onboard-cannot-draft">
					<span className="font-medium">{t("source.cannotDraft")}.</span> {inv.next}
				</p>
			) : (
				<div className="flex flex-col gap-3 rounded-lg border p-3" data-testid="onboard-plan">
					<div className="flex flex-wrap gap-3">
						<label className="flex flex-col gap-1 text-xs">
							<span className="font-medium">{t("plan.suite")}</span>
							<Input value={plan.suite} onChange={(e) => set({ suite: e.target.value.toLowerCase() })} data-testid="onboard-plan-suite" />
							<span className="text-muted-foreground max-w-xs">{t("plan.suiteHint")}</span>
						</label>
						{rows && (
							<label className="flex flex-col gap-1 text-xs">
								<span className="font-medium">{t("plan.grader")}</span>
								<select
									className={selectClass}
									value={plan.grader ?? "answer_match"}
									onChange={(e) => set({ grader: e.target.value })}
									data-testid="onboard-plan-grader"
								>
									<option value="answer_match">{t("plan.exact")}</option>
									<option value="judge">{t("plan.judge")}</option>
								</select>
							</label>
						)}
						{rows && plan.grader === "judge" && (
							<>
								<label className="flex flex-col gap-1 text-xs">
									<span className="font-medium">{t("plan.judgeProvider")}</span>
									<Input
										value={plan.judge?.provider ?? ""}
										onChange={(e) => set({ judge: { provider: e.target.value, model: plan.judge?.model ?? "" } })}
										placeholder="openai"
										data-testid="onboard-plan-judge-provider"
									/>
								</label>
								<label className="flex flex-col gap-1 text-xs">
									<span className="font-medium">{t("plan.judgeModel")}</span>
									<Input
										value={plan.judge?.model ?? ""}
										onChange={(e) => set({ judge: { provider: plan.judge?.provider ?? "", model: e.target.value } })}
										placeholder="gpt-4.1-mini"
										data-testid="onboard-plan-judge-model"
									/>
								</label>
							</>
						)}
						{(inv.importers?.length ?? 0) > 0 && (
							<label className="flex flex-col gap-1 text-xs">
								<span className="font-medium">{t("plan.importer")}</span>
								<select
									className={selectClass}
									value={plan.importer ?? ""}
									onChange={(e) => set({ importer: e.target.value })}
									data-testid="onboard-plan-importer"
								>
									<option value="" />
									{inv.importers?.map((n) => (
										<option key={n} value={n}>
											{n}
										</option>
									))}
								</select>
							</label>
						)}
						<label className="flex flex-col gap-1 text-xs">
							<span className="font-medium">{t("plan.limit")}</span>
							<Input
								type="number"
								min={1}
								max={500}
								value={plan.limit ?? ""}
								onChange={(e) => set({ limit: e.target.value ? Number(e.target.value) : undefined })}
								className="w-28"
								data-testid="onboard-plan-limit"
							/>
						</label>
						<label className="flex flex-col gap-1 text-xs">
							<span className="font-medium">{t("plan.heldOut")}</span>
							<Input
								type="number"
								min={0}
								max={100}
								value={plan.held_out?.pct ?? 0}
								onChange={(e) => set({ held_out: { pct: Number(e.target.value) || 0, seed: plan.held_out?.seed || 1 } })}
								className="w-24"
								data-testid="onboard-plan-heldout"
							/>
							<span className="text-muted-foreground max-w-xs">{t("plan.heldOutHint")}</span>
						</label>
					</div>
					{rows && (inv.columns?.length ?? 0) > 0 && (
						<div className="flex flex-wrap gap-3" data-testid="onboard-plan-mapping">
							{MAP_FIELDS.map((f) => (
								<label key={f} className="flex flex-col gap-1 text-xs">
									<span className="font-medium">{tx(`plan.field.${f}`)}</span>
									<select
										className={selectClass}
										value={plan.mapping?.[f] ?? ""}
										onChange={(e) => {
											const m = { ...(plan.mapping ?? {}) };
											if (e.target.value) m[f] = e.target.value;
											else delete m[f];
											set({ mapping: m });
										}}
										data-testid={`onboard-map-${f}`}
									>
										<option value="">{t("plan.mapAuto")}</option>
										{inv.columns?.map((c) => (
											<option key={c} value={c}>
												{c}
											</option>
										))}
									</select>
								</label>
							))}
						</div>
					)}
					<div className="flex flex-wrap items-center gap-3">
						<Button onClick={onDraft} isLoading={busy} disabled={busy || !plan.suite} dataTestId="onboard-draft">
							{t("plan.draft")}
						</Button>
						<span className="text-muted-foreground text-xs">{t("plan.draftHint")}</span>
					</div>
				</div>
			)}
		</div>
	);
}
