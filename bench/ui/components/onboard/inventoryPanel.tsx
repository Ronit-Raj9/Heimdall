import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { WhyDisabled } from "@/components/ui/whyDisabled";
import { LabeledModelPicker, isPicked } from "@/components/models/labeledModelPicker";
import "@/components/home/uf2aI18n";
import { Link } from "@tanstack/react-router";
import { MAP_FIELDS, MappingStep } from "@/app/suites_/$suiteId/views/rowMapper";
import { suggestSuiteId, suiteIdBlocks, suiteIdCheck, type SuiteIdCheck } from "@/components/onboard/onboard.utils";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import { w4m5Text } from "@/components/onboard/w4m5I18n";
import type { OnboardInventory, OnboardMatch, OnboardPlan } from "@/lib/types/onboard";
import type { RowColumn, RowMapping } from "@/lib/types/suiteBuild";
import { AlertTriangle } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { flushSync } from "react-dom";
import { useTranslation } from "react-i18next";

const selectClass = "border-input bg-background h-9 rounded-md border px-2 text-sm";

/** Row fields the onboard rows reader maps (onboard/source_rows.go mappingKeys), in the Build tab mapper's order. */
export const ONBOARD_MAP_FIELDS = [
	"question",
	"expected",
	"rubric",
	"reference",
	"wrong_answers",
	"id",
	"title",
	"split",
] as const satisfies readonly (typeof MAP_FIELDS)[number][];

/** How many of total tasks a held-out share of pct percent keeps private, as the draft picks them (onboard ApplySplit): rounded, at least one. */
export function heldOutCount(pct: number, total: number): { n: number; total: number } {
	if (pct <= 0 || total <= 0) return { n: 0, total };
	const n = Math.max(1, Math.floor((total * Math.min(pct, 100) + 50) / 100));
	return { n, total };
}

/**
 * The cost line under what was found, for the grader chosen in the plan (wave 4, p10-1): a rubric
 * judge calls the judge model, so the line never says the runs cost nothing. Empty when no task
 * was found (p10-9c).
 */
export function estimateLine(
	tx: (k: string, o?: Record<string, unknown>) => string,
	inv: Pick<OnboardInventory, "estimate">,
	grader: string | undefined,
): string {
	const { tasks, readiness_runs: runs, judge_calls: judge } = inv.estimate;
	if (tasks === 0) return "";
	if (grader === "judge") return w4m5Text("estimate.judge", { tasks, runs });
	// Q-57: about ten readiness runs a minute, so the line also gives the time.
	const line = tx("found.estimate", { tasks, runs, minutes: readinessMinutes(runs) });
	return judge > 0 ? `${line} ${tx("found.estimateJudge", { judge })}` : line;
}

/** Q-57: the minutes the free readiness runs take, about ten a minute, at least one. */
export function readinessMinutes(runs: number): number {
	return Math.max(1, Math.ceil(runs / 10));
}

/** Where a source that cannot become tasks here is built instead (M2-112, M6-50): OpenAPI and GitHub. */
export function builderLink(kind: string): string | null {
	if (kind === "openapi") return "/suites/from-api";
	if (kind === "github") return "/suites/new";
	return null;
}

/**
 * Q-36: the search the builder link carries. An OpenAPI link goes to Tasks from your API with the
 * spec's address, so that page opens with the spec filled in; a file or pasted text has no address.
 */
export function builderSearch(kind: string, source: string): { spec: string } | undefined {
	return kind === "openapi" && /^https?:\/\//i.test(source.trim()) ? { spec: source.trim() } : undefined;
}

/** A whole number inside [min, max] (M2-112b). */
export function clampInt(v: number, min: number, max: number): number {
	if (!Number.isFinite(v)) return min;
	return Math.min(max, Math.max(min, Math.round(v)));
}

/** The inventory's column paths as row mapper columns, with an example from the first sample that has one. */
export function onboardColumns(inv: Pick<OnboardInventory, "columns" | "samples">): RowColumn[] {
	return (inv.columns ?? []).map((path) => {
		const example = inv.samples.find((s) => s.fields[path])?.fields[path];
		const filled = inv.samples.filter((s) => s.fields[path]).length;
		return { path, kind: "text", example, filled };
	});
}

/** The plan mapping without fields left on auto, onboard only reads the fields it knows. */
export function onboardMapping(m: RowMapping): Record<string, string> {
	const out: Record<string, string> = {};
	for (const f of ONBOARD_MAP_FIELDS) {
		const v = m[f];
		if (v) out[f] = v;
	}
	return out;
}

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

/**
 * M2-112c: a typed Name gives the benchmark its title and, until the id box was edited by
 * hand, an id made from it (the preview line under the id box shows the result).
 */
export function planForName(
	plan: OnboardPlan,
	name: string,
	idEdited: boolean,
	known: Parameters<typeof suggestSuiteId>[1],
): OnboardPlan {
	return idEdited ? { ...plan, title: name } : { ...plan, title: name, suite: suggestSuiteId(name, known) };
}

/** The line under the Benchmark id box: the rule, a refusal, or what drafting into this id does. */
function suiteIdLine(
	check: SuiteIdCheck,
	id: string,
	tx: (k: string, o?: Record<string, unknown>) => string,
): { text: string; error: boolean } {
	switch (check.state) {
		case "invalid":
		case "empty":
			return { text: tx("plan.suiteInvalid"), error: check.state === "invalid" };
		case "builtin":
			return { text: tx("plan.suiteBuiltin", { id }), error: true };
		case "existing":
			return { text: tx("plan.suiteExisting", { id, count: check.count }), error: false };
		default:
			return { text: tx("plan.suiteNew", { id }), error: false };
	}
}

/** Step 2: what was found, and the plan the person edits before drafting. */
export function InventoryPanel({
	inv,
	plan,
	onPlan,
	onDraft,
	onLeave,
	busy,
}: {
	inv: OnboardInventory;
	plan: OnboardPlan;
	onPlan(p: OnboardPlan): void;
	onDraft(): void;
	/** Called before the app's own next-step link navigates, so the page drops its leave guard (Q-269). */
	onLeave?(): void;
	busy: boolean;
}) {
	const { t } = useTranslation("onboard");
	const { t: tb } = useTranslation("suiteBuild");
	const tx = (k: string, o?: Record<string, unknown>) => t(k as never, o as never) as unknown as string;
	const rows = inv.kind === "rows" || inv.kind === "hf_dataset";
	const sampleKeys = Array.from(new Set(inv.samples.flatMap((s) => Object.keys(s.fields)))).slice(0, 4);
	const mappable = rows && (inv.columns?.length ?? 0) > 0;
	const set = (patch: Partial<OnboardPlan>) => onPlan({ ...plan, ...patch });
	// N46: the id is checked as it is typed, against the server's rule and the benchmarks already listed.
	const suitesLight = useGetSuitesLightQuery();
	const known = suitesLight.data?.suites ?? [];
	const idCheck = suiteIdCheck(plan.suite, known, suitesLight.data?.reserved_ids);
	const idLine = suiteIdLine(idCheck, plan.suite, tx);
	const idLineId = useId();
	// M2-112c: the id follows the Name until it is typed by hand.
	const [idEdited, setIdEdited] = useState(false);
	const estimate = estimateLine(tx, inv, plan.grader);
	const { t: tf } = useTranslation("uf2a");
	// M6-54: rubric grading needs a judge model before drafting can start.
	const judgeMissing = rows && plan.grader === "judge" && !isPicked(plan.judge ?? null);
	// An empty box on first show gets a free id made from the source name.
	useEffect(() => {
		if (plan.suite) return;
		const id = suggestSuiteId(inv.title, known);
		if (id) onPlan({ ...plan, suite: id });
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [inv.title, suitesLight.data]);
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
			{estimate && (
				<p className="text-muted-foreground text-xs" data-testid="onboard-estimate">
					{estimate}
				</p>
			)}
			{!inv.can_draft && mappable && (
				<p role="note" className="text-sm" data-testid="onboard-map-first">
					{inv.next}
				</p>
			)}
			{!inv.can_draft && !mappable ? (
				<p
					role="note"
					className="border-chart-info/50 bg-chart-info/10 rounded-sm border px-3 py-2 text-sm"
					data-testid="onboard-cannot-draft"
				>
					{inv.kind === "openapi" ? (
						t("source.openapiNext")
					) : (
						<>
							<span className="font-medium">{t("source.cannotDraft")}.</span> {inv.next}
						</>
					)}
					{builderLink(inv.kind) && (
						<Link
							to={builderLink(inv.kind) as never}
							search={builderSearch(inv.kind, inv.source) as never}
							onClick={() => flushSync(() => onLeave?.())}
							className="text-primary mt-2 block font-medium underline-offset-4 hover:underline"
							data-testid="onboard-builder-link"
						>
							{tf(`onboard.builder.${inv.kind as "openapi"}`)}
						</Link>
					)}
				</p>
			) : (
				<div className="flex flex-col gap-3 rounded-lg border p-3" data-testid="onboard-plan">
					<div className="flex flex-wrap gap-3">
						<label className="flex flex-col gap-1 text-xs">
							<span className="font-medium">{t("plan.name")}</span>
							<Input
								value={plan.title ?? inv.title}
								onChange={(e) => onPlan(planForName(plan, e.target.value, idEdited, known))}
								data-testid="onboard-plan-name"
							/>
							<span className="text-muted-foreground max-w-xs">{t("plan.nameHint")}</span>
						</label>
						<label className="flex flex-col gap-1 text-xs">
							<span className="font-medium">{t("plan.suite")}</span>
							<Input
								value={plan.suite}
								// M2-112c: what is typed stays as typed; the check line names a rule it breaks.
								onChange={(e) => {
									setIdEdited(true);
									set({ suite: e.target.value });
								}}
								placeholder="my-benchmark"
								aria-invalid={idLine.error || undefined}
								aria-describedby={idLineId}
								data-testid="onboard-plan-suite"
							/>
							<span
								id={idLineId}
								className={idLine.error ? "text-destructive max-w-xs" : "text-muted-foreground max-w-xs"}
								data-state={idCheck.state}
								data-testid="onboard-plan-suite-check"
							>
								{idLine.text}
							</span>
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
								<LabeledModelPicker
									label={`${t("plan.judgeProvider")} / ${t("plan.judgeModel")}`}
									value={plan.judge?.provider || plan.judge?.model ? { provider: plan.judge?.provider ?? "", model: plan.judge?.model ?? "" } : null}
									onChange={(v) => set({ judge: { provider: v.provider, model: v.model } })}
									hint={judgeMissing ? tf("onboard.judgeNeeded") : undefined}
									testId="onboard-plan-judge"
								/>
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
						{/* Q-119: how many tasks and the held-out share are extras, folded under More options. */}
						<details className="w-full" data-testid="onboard-plan-more">
							<summary className="text-primary min-h-11 cursor-pointer py-2 text-xs font-medium">{t("plan.more")}</summary>
							<div className="flex flex-wrap gap-3 pt-1">
								<label className="flex flex-col gap-1 text-xs">
									<span className="font-medium">{t("plan.limit")}</span>
									<Input
										type="number"
										min={1}
										max={500}
										value={plan.limit ?? ""}
										onChange={(e) => set({ limit: e.target.value ? clampInt(Number(e.target.value), 1, 500) : undefined })}
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
										onChange={(e) => set({ held_out: { pct: clampInt(Number(e.target.value) || 0, 0, 100), seed: plan.held_out?.seed || 1 } })}
										className="w-24"
										data-testid="onboard-plan-heldout"
									/>
									<span className="text-muted-foreground max-w-xs">{t("plan.heldOutHint")}</span>
									{(plan.held_out?.pct ?? 0) > 0 && inv.estimate.tasks > 0 && (
										<span className="max-w-xs" data-testid="onboard-plan-heldout-line">
											{t("plan.heldOutLine", {
												pct: plan.held_out.pct,
												...heldOutCount(plan.held_out.pct, Math.min(plan.limit || inv.estimate.tasks, inv.estimate.tasks)),
											})}
										</span>
									)}
								</label>
							</div>
						</details>
					</div>
					{rows && (inv.columns?.length ?? 0) > 0 && (
						<div data-testid="onboard-plan-mapping">
							<MappingStep
								columns={onboardColumns(inv)}
								mapping={plan.mapping ?? {}}
								onChange={(m) => set({ mapping: onboardMapping(m) })}
								showGradeOn={false}
								fields={ONBOARD_MAP_FIELDS}
								showMetadata={false}
								help={w4m5Text("mapping.help")}
								t={tb}
							/>
						</div>
					)}
					<div className="flex flex-wrap items-center gap-3">
						<Button
							onClick={onDraft}
							isLoading={busy}
							disabled={busy || suiteIdBlocks(idCheck) || judgeMissing}
							aria-describedby={suiteIdBlocks(idCheck) ? idLineId : undefined}
							dataTestId="onboard-draft"
						>
							{t("plan.draft")}
						</Button>
						<span className="text-muted-foreground text-xs">{t("plan.draftHint")}</span>
						<WhyDisabled reason={judgeMissing ? tf("onboard.judgeNeeded") : null} testId="onboard-draft-why" />
					</div>
				</div>
			)}
		</div>
	);
}