import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { getLocale } from "@/lib/i18n";
import type { Action, Decision, Effect, GuardResult, Verdict } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import type { TFunction } from "i18next";
import { Ban, CircleCheck, CircleHelp, CircleX, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { MethodHint } from "./methodHint";
import {
	DECISION_TONE,
	GUARD_TONE,
	P_VALUE_FLOOR,
	TONE_CLASS,
	boundsDiffer,
	formatPValue,
	formatPp,
	formatSd,
	formatSignedEffect,
	formatSignedPp,
	metricUnit,
	integrityTone,
	integrityView,
	tasksNeededView,
	type Tone,
} from "./verdict.utils";

const ACTION_TONE = {
	ship: "good",
	hold: "bad",
	no_decision: "warn",
	fix_harness: "neutral",
} as const satisfies Record<Action, Tone>;

// The decision's shape, so it reads without colour.
const DECISION_ICON = {
	improved: CircleCheck,
	regressed: CircleX,
	inconclusive: CircleHelp,
	invalid: Ban,
} as const satisfies Record<Decision, LucideIcon>;

// Hero surface per tone: a faint wash of the status hue behind the decision and the action.
const HERO_SURFACE = {
	good: "bg-chart-success/[0.07] border-chart-success/40",
	bad: "bg-chart-error/[0.07] border-chart-error/40",
	warn: "bg-chart-warning/[0.08] border-chart-warning/45",
	neutral: "bg-muted/60 border-border",
} as const satisfies Record<Tone, string>;

const TONE_INK = {
	good: "text-chart-success-ink",
	bad: "text-chart-error-ink",
	warn: "text-chart-warning-ink",
	neutral: "text-foreground",
} as const satisfies Record<Tone, string>;

export interface VerdictCardProps {
	verdict: Verdict;
	/** Display label of the primary metric, from the catalog; the metric id when absent. */
	metricLabel?: string;
	className?: string;
}

/**
 * The release decision on the primary endpoint (DESIGN.md 9.3, 9.4): the decision, the release
 * effect with its descriptive interval, discordant tasks, the exact p-value, the MDE, guards,
 * integrity and the one action.
 */
export function VerdictCard({ verdict, metricLabel, className }: VerdictCardProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const tone = DECISION_TONE[verdict.decision];
	const effect = verdict.effect;
	const tasksNeeded = tasksNeededView(verdict);
	const integrity = integrityView(verdict.integrity);

	return (
		<Card className={cn("gap-0 overflow-hidden py-0", className)} data-testid="verdict-card" data-decision={verdict.decision}>
			<div
				className={cn("grid grid-cols-1 gap-5 border-b p-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] lg:items-center", HERO_SURFACE[tone])}
			>
				<div className="flex flex-col gap-3">
					<CardHeader className="px-0">
						<CardTitle className="text-muted-foreground text-xs font-medium tracking-[0.06em] uppercase">
							{t("verdict.card.title", { metric: metricLabel ?? verdict.primary.metric, condition: verdict.primary.condition })}
						</CardTitle>
					</CardHeader>
					<div className="flex flex-wrap items-end gap-x-6 gap-y-3">
						<span
							className={cn("inline-flex items-center gap-2.5 text-4xl leading-none font-bold tracking-[-0.02em]", TONE_INK[tone])}
							data-testid="verdict-decision"
							data-tone={tone}
						>
							<DecisionIcon decision={verdict.decision} />
							{t(`verdict.card.decision.${verdict.decision}`)}
						</span>
						<span className="flex flex-col gap-0.5" data-testid="verdict-effect">
							<span className="text-foreground text-2xl leading-none font-semibold tabular-nums">
								{effect ? formatSignedEffect(effect.metric, effect.d_bar, locale) : t("verdict.card.noEffect")}
							</span>
							<span className="text-muted-foreground text-xs">{t("verdict.card.releaseEffect")}</span>
						</span>
					</div>
				</div>
				<ActionLine verdict={verdict} />
			</div>
			<CardContent className="flex flex-col gap-6 py-6">
				<dl className="grid grid-cols-1 gap-x-8 gap-y-4 text-sm sm:grid-cols-2 xl:grid-cols-3">
					<Stat
						label={t("verdict.card.interval")}
						hint={<DescriptiveTag title={t("verdict.card.intervalHint")} label={t("verdict.card.descriptive")} />}
						testId="verdict-stat-interval"
					>
						{effect?.interval
							? metricUnit(effect.metric) === null
								? t("verdict.card.intervalValue", {
										lo: formatSignedPp(effect.interval[0], locale),
										hi: formatSignedPp(effect.interval[1], locale),
									})
								: `${formatSignedEffect(effect.metric, effect.interval[0], locale)} .. ${formatSignedEffect(effect.metric, effect.interval[1], locale)}`
							: t("verdict.card.noEffect")}
					</Stat>
					<Stat label={t("verdict.card.discordant")} testId="verdict-stat-discordant">
						{effect ? (
							<>
								{t("verdict.card.discordantValue", { count: effect.n_nonzero, n: effect.n_tasks })}
								<span className="text-muted-foreground ml-2 text-xs">
									{t("verdict.card.discordantSplit", { positive: effect.n_positive, negative: effect.n_negative })}
								</span>
							</>
						) : (
							t("verdict.card.noEffect")
						)}
					</Stat>
					<Stat label={t("verdict.card.pValue")} hint={<MethodHint topic="pValue" />} testId="verdict-stat-p-value">
						<PValue effect={effect} />
						<span className="text-muted-foreground ml-2 text-xs">{t("verdict.card.pValueUnit", { unit: verdict.randomization_unit })}</span>
					</Stat>
					<Stat label={t("verdict.card.mde")} hint={<MethodHint topic="mde" />} testId="verdict-stat-mde">
						{t("verdict.card.pp", { value: formatPp(verdict.mde, locale) })}
						<span className="text-muted-foreground ml-2 text-xs" data-testid="verdict-stat-mde-sd">
							{verdict.mde_assumed_sd
								? t("verdict.card.assumedSd", { sd: formatSd(verdict.sd, locale) })
								: t("verdict.card.observedSd", { sd: formatSd(verdict.sd, locale) })}
						</span>
					</Stat>
					{tasksNeeded && (
						<Stat label={t("verdict.card.tasksNeeded")} testId="verdict-stat-tasks-needed">
							{tasksNeeded.kind === "over"
								? t("verdict.card.tasksNeededOver", { count: tasksNeeded.count })
								: t("verdict.card.tasksNeededValue", { count: tasksNeeded.count })}
						</Stat>
					)}
					{effect && boundsDiffer(effect) && effect.lower_bound && effect.upper_bound && (
						<Stat label={t("verdict.card.bounds")} testId="verdict-stat-bounds">
							{t("verdict.card.boundsValue", {
								lo: formatSignedPp(effect.lower_bound.d_bar, locale),
								hi: formatSignedPp(effect.upper_bound.d_bar, locale),
							})}
						</Stat>
					)}
				</dl>

				<GuardList guards={verdict.guards} />

				{verdict.invalid_reasons && verdict.invalid_reasons.length > 0 && (
					<section className="flex flex-col gap-1" data-testid="verdict-invalid-reasons">
						<h3 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{t("verdict.card.invalidReasons")}</h3>
						<ul className="list-disc pl-5 text-sm">
							{verdict.invalid_reasons.map((reason) => (
								<li key={reason} className="font-mono text-xs">
									{reason}
								</li>
							))}
						</ul>
					</section>
				)}

				<div className="flex items-center gap-2 text-sm" data-testid="verdict-integrity">
					<span className="text-muted-foreground">{t("verdict.card.integrity")}</span>
					<Badge
						variant="outline"
						className={TONE_CLASS[integrityTone(integrity, verdict.decision)]}
						data-integrity={integrity}
						data-tone={integrityTone(integrity, verdict.decision)}
					>
						{t(`verdict.card.integrityStatus.${integrity}`)}
					</Badge>
				</div>
			</CardContent>
		</Card>
	);
}

function DecisionIcon({ decision }: { decision: Decision }) {
	const Icon = DECISION_ICON[decision];
	return <Icon className="size-8 shrink-0" strokeWidth={2.25} aria-hidden="true" />;
}

function Stat({ label, hint, testId, children }: { label: string; hint?: ReactNode; testId: string; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-0.5" data-testid={testId}>
			<dt className="text-muted-foreground flex items-center gap-1.5 text-xs">
				{label}
				{hint}
			</dt>
			<dd className="text-foreground text-[15px] font-medium tabular-nums">{children}</dd>
		</div>
	);
}

function DescriptiveTag({ title, label }: { title: string; label: string }) {
	return (
		<span className="rounded-sm border border-dashed px-1 text-[10px] leading-4 uppercase" title={title} aria-label={title}>
			{label}
		</span>
	);
}

function PValue({ effect }: { effect: Effect | undefined }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	if (!effect || typeof effect.p_value !== "number") return <>{t("verdict.card.noEffect")}</>;
	const sampled = effect.p_draws ? <> {t("verdict.card.pValueSampled", { draws: effect.p_draws })}</> : null;
	if (effect.p_value < P_VALUE_FLOOR) return <>{t("verdict.card.pValueBelow", { p: formatPValue(P_VALUE_FLOOR, locale) })}{sampled}</>;
	return <>{formatPValue(effect.p_value, locale)}{sampled}</>;
}

function GuardList({ guards }: { guards: GuardResult[] }) {
	const { t } = useTranslation("bench");
	return (
		<section className="flex flex-col gap-2" data-testid="verdict-guards">
			<h3 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{t("verdict.card.guards")}</h3>
			{guards.length === 0 ? (
				<p className="text-muted-foreground text-sm">{t("verdict.card.noGuards")}</p>
			) : (
				<ul className="flex flex-col gap-2">
					{guards.map((guard) => (
						<GuardItem key={`${guard.spec.rule}:${guard.spec.condition ?? ""}:${guard.spec.metric ?? ""}`} guard={guard} />
					))}
				</ul>
			)}
		</section>
	);
}

function GuardItem({ guard }: { guard: GuardResult }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const tone = GUARD_TONE[guard.status];
	const effect = guard.effect;
	const testKey = `${guard.spec.rule}${guard.spec.condition ? `-${guard.spec.condition}` : ""}`;

	return (
		<li className="flex flex-col gap-0.5 text-sm" data-testid={`verdict-guard-${testKey}`} data-status={guard.status}>
			<div className="flex flex-wrap items-center gap-2">
				<span className="font-medium">{guardLabel(guard, t)}</span>
				<Badge variant="outline" className={cn("font-mono", TONE_CLASS[tone])}>
					{t(`verdict.card.guardStatus.${guard.status}`)}
				</Badge>
				{effect && (
					<span className="text-muted-foreground text-xs tabular-nums">
						{t("verdict.card.guardEffect", { effect: formatSignedPp(effect.d_bar, locale), count: effect.n_nonzero })}
						{effect.interval &&
							` · ${t("verdict.card.intervalValue", { lo: formatSignedPp(effect.interval[0], locale), hi: formatSignedPp(effect.interval[1], locale) })}`}
					</span>
				)}
			</div>
			{effect && boundsDiffer(effect) && effect.lower_bound && effect.upper_bound && (
				<span className="text-muted-foreground text-xs">
					{t("verdict.card.guardBounds", {
						lo: formatSignedPp(effect.lower_bound.d_bar, locale),
						hi: formatSignedPp(effect.upper_bound.d_bar, locale),
					})}
				</span>
			)}
			{guard.detail && <span className="text-muted-foreground text-xs">{guard.detail}</span>}
		</li>
	);
}

type TFn = TFunction<"bench">;

function guardLabel(guard: GuardResult, t: TFn): string {
	switch (guard.spec.rule) {
		case "no_regression":
			return guard.spec.condition
				? t("verdict.card.guardRule.noRegression", { condition: guard.spec.condition })
				: t("verdict.card.guardRule.noRegressionPrimary");
		case "no_new_integrity_violation":
			return t("verdict.card.guardRule.noNewIntegrityViolation");
		default:
			return t("verdict.card.guardRule.other", { rule: guard.spec.rule });
	}
}

function ActionLine({ verdict }: { verdict: Verdict }) {
	const { t } = useTranslation("bench");
	const tone = ACTION_TONE[verdict.action];
	return (
		<div
			className={cn("flex flex-col gap-1.5 rounded-lg border border-l-4 px-4 py-3.5 shadow-(--shadow-card)", TONE_CLASS[tone], "bg-card")}
			data-testid="verdict-action"
			data-action={verdict.action}
		>
			<p className="text-base font-bold tracking-[0.04em]">
				{t("verdict.card.actionLine", { action: t(`verdict.card.actionValue.${verdict.action}`) })}
			</p>
			<p className="text-foreground/85 text-sm leading-relaxed">{actionHint(verdict, t)}</p>
		</div>
	);
}

function actionHint(verdict: Verdict, t: TFn): string {
	switch (verdict.action) {
		case "ship":
			return t("verdict.card.actionHint.ship");
		case "hold":
			return verdict.decision === "regressed" ? t("verdict.card.actionHint.holdRegressed") : t("verdict.card.actionHint.holdViolations");
		case "no_decision":
			return t("verdict.card.actionHint.no_decision");
		case "fix_harness": {
			const gate = verdict.invalid_reasons?.[0];
			return gate ? t("verdict.card.actionHint.fix_harness", { gate }) : t("verdict.card.actionHint.fixHarnessUnnamed");
		}
	}
}