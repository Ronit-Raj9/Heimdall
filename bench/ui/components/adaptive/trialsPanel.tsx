import { ToneBadge } from "@/components/suites/suiteBadges";
import { useGetExperimentAdaptiveQuery } from "@/lib/store/apis/adaptiveApi";
import type { AdaptiveLook, AdaptiveView } from "@/lib/types/bench";
import type React from "react";
import { useTranslation } from "react-i18next";

/**
 * W7 adaptive trials and suite tiers (docs/adaptive-trials.md, docs/suite-tiers.md). The verdict
 * page's trials and tier panel, fetched by experiment id. Renders nothing while loading, on an
 * error, or for a classic fixed experiment with no tier, no follow-up and nothing to buy.
 */
export function ExperimentTrialsPanel({ expId }: { expId: string }) {
	const q = useGetExperimentAdaptiveQuery(expId);
	if (!q.data || !hasContent(q.data)) return null;
	return <TrialsPanel view={q.data} />;
}

/** Whether the panel has anything to show. */
export function hasContent(v: AdaptiveView): boolean {
	return v.plan !== null || v.tier !== "" || v.more_evidence !== null || v.extends !== null;
}

/** Tier, trials used against the maximum, runs saved, stopping reason and the "buy more evidence" line. */
export function TrialsPanel({ view }: { view: AdaptiveView }) {
	const { t } = useTranslation("bench");
	const rep = view.report;
	const me = view.more_evidence;
	return (
		<section
			className="flex min-w-0 flex-col gap-3 rounded-md border p-3 text-xs"
			data-testid="trials-panel"
			aria-label={t("adaptive.title")}
		>
			<div className="flex flex-wrap items-center gap-2">
				<p className="text-sm font-medium">{t("adaptive.title")}</p>
				{view.tier ? (
					<ToneBadge tone="neutral">
						<span data-testid="trials-tier">{t("adaptive.tier", { tier: view.tier, count: view.tier_tasks.length })}</span>
					</ToneBadge>
				) : null}
				{view.plan ? (
					<ToneBadge tone="neutral">{t(view.plan.rule === "sequential" ? "adaptive.ruleSequential" : "adaptive.ruleTask")}</ToneBadge>
				) : null}
				{view.extends ? (
					<ToneBadge tone={view.extends.rule === "exploratory" ? "warn" : "neutral"}>
						<span data-testid="trials-extends">
							{t(view.extends.rule === "exploratory" ? "adaptive.extendsExploratory" : "adaptive.extendsCombined", {
								exp: view.extends.exp_id,
							})}
						</span>
					</ToneBadge>
				) : null}
			</div>
			{view.tier ? <p className="text-muted-foreground">{t("adaptive.tierNote")}</p> : null}
			{rep ? (
				<div className="grid grid-cols-2 gap-2 md:grid-cols-4" data-testid="trials-counts">
					<Card
						label={t("adaptive.used")}
						value={t("adaptive.usedValue", { run: rep.run_slots, max: rep.max_slots })}
						testId="trials-used"
					/>
					<Card label={t("adaptive.saved")} value={String(rep.runs_saved)} hint={t("adaptive.savedHint")} testId="trials-saved" />
					<Card
						label={t("adaptive.stop")}
						value={<StopText reason={rep.stop_reason} look={rep.stop_look} looks={rep.looks} />}
						testId="trials-stop"
					/>
					<Card label={t("adaptive.alpha")} value={rep.decision_alpha.toFixed(4)} hint={t("adaptive.alphaHint")} testId="trials-alpha" />
				</div>
			) : null}
			{rep?.look_results?.length ? <LookTable looks={rep.look_results} /> : null}
			{rep?.tasks?.length ? (
				<p className="text-muted-foreground" data-testid="trials-tasks">
					{t("adaptive.taskSummary", {
						stopped: rep.tasks.filter((x) => x.reason === "concordant" && x.trials < x.max).length,
						full: rep.tasks.filter((x) => x.trials >= x.max).length,
						total: rep.tasks.length,
					})}
				</p>
			) : null}
			{rep?.excluded?.length ? (
				<p className="text-amber-600" data-testid="trials-excluded">
					{t("adaptive.excluded", { count: rep.excluded.length })}
				</p>
			) : null}
			{me ? (
				<div className="rounded border p-2" data-testid="trials-more-evidence">
					<p className="font-medium">
						{t("adaptive.more", {
							pp: me.target_pp.toFixed(1),
							tasks: me.tasks,
							cost: me.cost_usd === undefined ? t("adaptive.costUnknown") : formatUSD(me.cost_usd),
						})}
					</p>
					<p className="text-muted-foreground">
						{t(me.sd_assumed ? "adaptive.sdAssumed" : "adaptive.sdMeasured", { sd: me.sd.toFixed(2) })} {me.cost_basis}
					</p>
					<p className="text-muted-foreground">
						{me.combination === "inverse_normal"
							? t("adaptive.combined", { alpha: (me.alpha_kept ?? 0).toFixed(4) })
							: t("adaptive.exploratory")}
					</p>
					{me.command ? <code className="mt-1 block break-all">{me.command}</code> : null}
				</div>
			) : null}
		</section>
	);
}

function Card({ label, value, hint, testId }: { label: string; value: React.ReactNode; hint?: string; testId: string }) {
	return (
		<div className="rounded border p-2" data-testid={testId}>
			<p className="text-muted-foreground">{label}</p>
			<p className="text-base font-semibold tabular-nums">{value}</p>
			{hint ? <p className="text-muted-foreground">{hint}</p> : null}
		</div>
	);
}

function LookTable({ looks }: { looks: AdaptiveLook[] }) {
	const { t } = useTranslation("bench");
	return (
		<table className="w-full text-left tabular-nums" data-testid="trials-looks">
			<thead>
				<tr className="text-muted-foreground">
					<th>{t("adaptive.look")}</th>
					<th>{t("adaptive.lookTasks")}</th>
					<th>{t("adaptive.lookAlpha")}</th>
					<th>{t("adaptive.lookP")}</th>
					<th>{t("adaptive.lookOutcome")}</th>
				</tr>
			</thead>
			<tbody>
				{looks.map((l) => (
					<tr key={l.look} data-testid={`trials-look-${l.look}`}>
						<td>{l.look}</td>
						<td>{l.tasks}</td>
						<td>{l.alpha.toFixed(4)}</td>
						<td>{l.p_low === undefined || l.p_high === undefined ? "-" : `${l.p_low.toFixed(4)} / ${l.p_high.toFixed(4)}`}</td>
						<td>{l.outcome}</td>
					</tr>
				))}
			</tbody>
		</table>
	);
}

const REASON_KEYS = {
	concordant: "adaptive.reason.concordant",
	max_reached: "adaptive.reason.max_reached",
	efficacy: "adaptive.reason.efficacy",
	futility: "adaptive.reason.futility",
	final_look: "adaptive.reason.final_look",
	waiting: "adaptive.reason.waiting",
} as const;

/** The stopping reason in words, with the look for a sequential design. */
function StopText({ reason, look, looks }: { reason: string; look?: number; looks?: number }) {
	const { t } = useTranslation("bench");
	const key = REASON_KEYS[reason as keyof typeof REASON_KEYS];
	const text = key ? t(key) : reason;
	return <>{look && looks ? t("adaptive.atLook", { text, look, looks }) : text}</>;
}

/** US dollars with two decimals, four below a dollar. */
export function formatUSD(v: number): string {
	return v < 1 ? `$${v.toFixed(4)}` : `$${v.toFixed(2)}`;
}