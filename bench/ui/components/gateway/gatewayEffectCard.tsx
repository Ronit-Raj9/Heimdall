import { Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { FeatureTestDialog, suiteOption } from "@/app/gateway/views/featureTestDialog";
import { useGetExperimentQuery } from "@/lib/store/apis/benchApi";
import { useGetBifrostSettingQuery, useGetGatewayEffectOptionalQuery, useGetGatewayReportCardQuery } from "@/lib/store/apis/gatewayApi";
import type { GwArmResult, GwBifrostSettingAnswer, GwEffect, GwResult } from "@/lib/types/gateway";
import { cn } from "@/lib/utils";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import "@/app/gateway/views/uf7cI18n";
import "@/components/gateway/ufc7I18n";
import "@/app/gateway/views/ufl1I18n";
import { UseInBifrostButton } from "./useInBifrost";

/** Text color of a result word: Saved green, Lost and Failed red, Worked gray (status tokens). */
export const RESULT_TONE: Record<GwResult, string> = {
	saved: "text-chart-success-ink",
	lost: "text-chart-error-ink",
	worked: "text-muted-foreground",
	failed: "text-chart-error-ink",
};

const pct = (v?: number) => (v === undefined ? "" : `${Math.round(v * 100)}%`);
const usd = (v?: number) => (v === undefined ? "" : `$${v.toFixed(4)}`);
const ms = (v?: number) => (v === undefined ? "" : `${Math.round(v)} ms`);

/** The detail keys with a label in uf7c:effect.detail; any other key shows only under "Raw data". */
export const DETAIL_KEYS: ReadonlySet<string> = new Set([
	"tiers",
	"unclassified",
	"disagreements",
	"recommendation",
	"buckets",
	"no_time",
	"gateway",
	"tool_sets",
	"filter",
	"poison",
	"poison_note",
	"points",
	"best",
	"best_value",
	"arms",
	"thresholds",
	"recommended",
	"same_question_hits",
	"undecided_hits",
	"unknown_similarity_hits",
	"single_call_tasks",
	"estimated",
]);

/** The labelled rows of the details (plain values, lists as a count) and whether raw data is left to show. Pure. */
export function detailRows(details: Record<string, unknown>): { rows: { key: string; value: unknown }[]; raw: boolean } {
	const rows: { key: string; value: unknown }[] = [];
	let raw = false;
	for (const [key, value] of Object.entries(details)) {
		if (value === null || value === undefined || value === "") continue;
		if (DETAIL_KEYS.has(key) && (typeof value !== "object" || Array.isArray(value))) rows.push({ key, value });
		if (!DETAIL_KEYS.has(key) || typeof value === "object") raw = true;
	}
	return { rows, raw };
}

/** The value of one details cell, as plain text. */
export function detailText(v: unknown): string {
	if (v === null || v === undefined) return "";
	if (typeof v === "string" || typeof v === "number" || typeof v === "boolean") return String(v);
	return JSON.stringify(v);
}

/** Why the effect gives no verdict (round 4 rule): under 10 runs in a setup, or no setup solved a task; null when it may. Pure. */
export function noVerdictReason(effect: Pick<GwEffect, "arms">): "fewRuns" | "noneSolved" | null {
	if (effect.arms.length === 0) return null;
	if (effect.arms.some((a) => a.runs < 10)) return "fewRuns";
	if (effect.arms.every((a) => !a.solve_rate)) return "noneSolved";
	return null;
}

const signed = (v: number, fmt: (x: number) => string) => (v > 0 ? `+${fmt(v)}` : v < 0 ? `-${fmt(-v)}` : fmt(0));
const DIFF_FMT = {
	solve: (x: number) => `${Math.round(x * 1000) / 10} pts`,
	cost: (x: number) => `$${x.toFixed(4)}`,
	latency: (x: number) => `${Math.round(x)} ms`,
} as const;

/** The difference rows (variant minus control) with their ranges: kind, difference, range. Pure. */
export function diffRows(effect: Pick<GwEffect, "solve_delta" | "cost_delta" | "latency_delta">): { key: keyof typeof DIFF_FMT; diff: string; range: string }[] {
	const out: { key: keyof typeof DIFF_FMT; diff: string; range: string }[] = [];
	for (const [key, iv] of [
		["solve", effect.solve_delta],
		["cost", effect.cost_delta],
		["latency", effect.latency_delta],
	] as const) {
		if (!iv) continue;
		const fmt = DIFF_FMT[key];
		const range = iv.low !== undefined && iv.high !== undefined ? `${signed(iv.low, fmt)} to ${signed(iv.high, fmt)}` : "";
		out.push({ key, diff: signed(iv.point, fmt), range });
	}
	return out;
}

function DiffTable({ effect }: { effect: GwEffect }) {
	const { t } = useTranslation("ufl1");
	const rows = diffRows(effect);
	if (rows.length === 0) return null;
	const variant = effect.arms.find((a) => a.arm === effect.variant);
	const control = effect.arms.find((a) => a.arm === effect.control);
	const runs = control && variant ? `${control.runs} / ${variant.runs}` : "";
	return (
		<div className="overflow-x-auto">
			<table className="w-full text-sm" data-testid="gateway-effect-diffs">
				<thead className="text-muted-foreground text-left text-xs">
					<tr>
						<th className="py-1 pr-3 font-normal">{t("diff.measure")}</th>
						<th className="py-1 pr-3 font-normal">{t("diff.difference")}</th>
						<th className="py-1 pr-3 font-normal">{t("diff.range")}</th>
						<th className="py-1 font-normal">{t("diff.runs")}</th>
					</tr>
				</thead>
				<tbody>
					{rows.map((r) => (
						<tr key={r.key} className="border-t" data-testid={`gateway-effect-diff-${r.key}`}>
							<td className="py-1 pr-3">{t(`diff.${r.key}`)}</td>
							<td className="py-1 pr-3 tabular-nums">{r.diff}</td>
							<td className="py-1 pr-3 tabular-nums">{r.range}</td>
							<td className="py-1 tabular-nums">{runs}</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

function ArmsTable({ arms, control }: { arms: GwArmResult[]; control: string }) {
	const { t } = useTranslation("w4p6");
	return (
		<div className="overflow-x-auto">
			<table className="w-full text-sm" data-testid="gateway-effect-arms">
				<thead className="text-muted-foreground text-left text-xs">
					<tr>
						<th className="py-1 pr-3 font-normal">{t("effect.setup")}</th>
						<th className="py-1 pr-3 font-normal">{t("effect.runs")}</th>
						<th className="py-1 pr-3 font-normal">{t("effect.tasks")}</th>
						<th className="py-1 pr-3 font-normal">{t("effect.solveRate")}</th>
						<th className="py-1 pr-3 font-normal">{t("effect.costPerTask")}</th>
						<th className="py-1 font-normal">{t("effect.latency")}</th>
					</tr>
				</thead>
				<tbody>
					{arms.map((a) => (
						<tr key={a.arm} className="border-t" data-testid={`gateway-effect-arm-${a.arm}`}>
							<td className="py-1 pr-3">
								{a.label}
								<span className="text-muted-foreground ml-2 text-xs">{a.arm === control ? t("effect.control") : t("effect.variant")}</span>
							</td>
							<td className="py-1 pr-3">{a.runs}</td>
							<td className="py-1 pr-3">{a.tasks}</td>
							<td className="py-1 pr-3">{pct(a.solve_rate)}</td>
							<td className="py-1 pr-3">{usd(a.cost_per_run)}</td>
							<td className="py-1">{ms(a.latency_ms)}</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

/** The Effect of one feature test: state, headline, setups, the feature's details and the button. Pure. */
export function GatewayEffectView({
	effect,
	setting,
	onRunAgain,
}: {
	effect: GwEffect;
	setting?: GwBifrostSettingAnswer;
	/** Unknown effect: opens the same feature test again (the paid start asks in the dialog). */
	onRunAgain?: () => void;
}) {
	const { t: c } = useTranslation("ufc7");
	const { t } = useTranslation("w4p6");
	const { t: u } = useTranslation("uf7c");
	const { t: l } = useTranslation("ufl1");
	const { rows, raw } = detailRows((effect.details ?? {}) as Record<string, unknown>);
	const noVerdict = noVerdictReason(effect);
	return (
		<Section title={t("effect.title")} testId="gateway-effect-card">
			<div className="flex flex-col gap-3" data-state={effect.state}>
				<div className="flex flex-wrap items-center gap-2 text-sm">
					<span className="bg-muted rounded-full px-2 py-0.5 text-xs" data-testid="gateway-effect-state">
						{t(`effect.state.${effect.state}`)}
					</span>
					{effect.result && !noVerdict && <span className={cn("font-medium", RESULT_TONE[effect.result])}>{t(`line.result.${effect.result}`)}</span>}
					<span data-testid="gateway-effect-headline">{effect.headline}</span>
				</div>
				{effect.reason && effect.state !== "measured" && (
					<p className="text-muted-foreground text-sm" data-testid="gateway-effect-reason">
						{effect.state === "unknown" ? u("effect.unknownNext", { reason: effect.reason }) : t("line.unknown", { reason: effect.reason })}
					</p>
				)}
				<p className="text-muted-foreground text-xs">{t("line.fromNoDate", { source: effect.source.text })}</p>
				{noVerdict && effect.state !== "unknown" && (
					<p className="text-muted-foreground text-sm" data-testid="gateway-effect-no-verdict">
						{l("diff.noVerdict", { reason: l(`diff.${noVerdict}`) })}
					</p>
				)}
				<DiffTable effect={effect} />
				{effect.arms.length > 0 && <ArmsTable arms={effect.arms} control={effect.control} />}
				{(rows.length > 0 || raw) && (
					<div className="flex flex-col gap-1">
						<h4 className="text-sm font-medium">{t("effect.details")}</h4>
						{rows.length > 0 && (
							<table className="text-sm" data-testid="gateway-effect-details">
								<tbody>
									{rows.map(({ key, value }) => (
										<tr key={key} className="border-t">
											<td className="text-muted-foreground py-1 pr-3">{u(`effect.detail.${key}` as "effect.detail.tiers")}</td>
											<td className="py-1">
												{Array.isArray(value)
													? u("effect.entries", { count: value.length })
													: typeof value === "boolean"
														? value
															? u("dialog.yes")
															: u("dialog.no")
														: detailText(value)}
											</td>
										</tr>
									))}
								</tbody>
							</table>
						)}
						{raw && (
							<details className="text-xs" data-testid="gateway-effect-raw">
								<summary className="text-muted-foreground cursor-pointer">{u("effect.rawData")}</summary>
								<pre className="bg-muted mt-1 max-h-72 overflow-auto rounded-md p-2">{JSON.stringify(effect.details, null, 2)}</pre>
							</details>
						)}
					</div>
				)}
				{effect.state === "unknown" && onRunAgain && (
					<div>
						<Button type="button" variant="outline" size="sm" onClick={onRunAgain} data-testid="gateway-effect-run-again">
							{c("effect.runAgain")}
						</Button>
					</div>
				)}
				<div>
					<UseInBifrostButton setting={setting ? setting.setting : effect.setting} reason={setting?.reason} />
				</div>
			</div>
		</Section>
	);
}

/** The effect in an answer, null for {"effect": null}: the shared reader beside the query. */
export { effectOf } from "@/lib/store/apis/gatewayApi";

/** Experiment page: the feature test's Effect. Renders nothing when the comparison is not a feature test (404 or {"effect": null}) or on any error. */
export function GatewayEffectCard({ expId }: { expId: string }) {
	const q = useGetGatewayEffectOptionalQuery(expId);
	const effect = q.data ?? null;
	const setting = useGetBifrostSettingQuery(expId, { skip: !effect });
	const [again, setAgain] = useState(false);
	const unknown = effect?.state === "unknown";
	const exp = useGetExperimentQuery(expId, { skip: !unknown });
	const card = useGetGatewayReportCardQuery(undefined, { skip: !again });
	if (!effect) return null;
	const name = card.data?.lines?.find((l) => l.feature === effect.feature)?.name ?? effect.feature;
	const suite = exp.data?.suite?.includes("@") ? exp.data.suite : exp.data?.suite ? suiteOption({ id: exp.data.suite, version: exp.data.suite_version }) : "";
	return (
		<>
			<GatewayEffectView effect={effect} setting={setting.data} onRunAgain={unknown ? () => setAgain(true) : undefined} />
			{again && <FeatureTestDialog feature={effect.feature} name={name} open initial={{ suite }} onClose={() => setAgain(false)} />}
		</>
	);
}