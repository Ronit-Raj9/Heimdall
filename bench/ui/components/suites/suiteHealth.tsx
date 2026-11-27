import { formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import type { TFunction } from "i18next";
import type { HealthFlag, SuiteSummary, SuiteTask, TaskHealth, TaskQuarantine } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { CircleAlert, CircleCheck, CircleDashed, EyeOff, ShieldAlert, ShieldOff } from "lucide-react";
import { useTranslation } from "react-i18next";
import "./uf6aI18n";
import { ToneBadge } from "./suiteBadges";
import {
	HEALTH_FLAG_TONE,
	flagCounts,
	formatPercent,
	healthChips,
	heldOutAndQuarantined,
	isRedacted,
	taskQuarantine,
	type HealthChip,
	type HealthState,
} from "./suiteHealth.utils";

/** "Held out": the task's instruction and files are not served, unless the server makes them visible. */
export function HeldOutBadge({ task, testId, className }: { task: Pick<SuiteTask, "redacted">; testId?: string; className?: string }) {
	const { t } = useTranslation("bench");
	return (
		<ToneBadge
			tone="neutral"
			icon={EyeOff}
			hint={isRedacted(task) ? t("suiteHealth.heldOutHint") : t("suiteHealth.heldOutVisibleHint")}
			testId={testId}
			className={className}
		>
			{t("suiteHealth.heldOut")}
		</ToneBadge>
	);
}

/** "Quarantined", with the reason, source and date in its hint. */
export function QuarantineBadge({ quarantine, testId }: { quarantine: TaskQuarantine; testId?: string }) {
	const { t } = useTranslation("bench");
	const when = formatDateTime(quarantine.at, getLocale()) ?? quarantine.at;
	const source =
		quarantine.source === "manual" || quarantine.source === "auto"
			? t(`suiteHealth.quarantine.source.${quarantine.source}`)
			: quarantine.source;
	return (
		<ToneBadge
			tone="warn"
			icon={ShieldOff}
			hint={t("suiteHealth.quarantine.hint", { when, source, reason: quarantine.reason })}
			testId={testId}
		>
			{t("suiteHealth.flags.quarantined")}
		</ToneBadge>
	);
}

/** One chip's label: flaky verifier carries its evidence count, a mismatch both difficulties, rates their percent. */
function chipLabel(chip: HealthChip, health: TaskHealth | undefined, t: TFunction<"bench">, locale: string): string {
	if (!health || chip.key === "quarantined") return t(`suiteHealth.flags.${chip.key}`);
	switch (chip.key) {
		case "flaky_verifier":
			return t("suiteHealth.chip.flaky", { count: health.verifier.evidence.length });
		case "difficulty_mismatch":
			return health.difficulty.declared && health.difficulty.observed
				? t("suiteHealth.chip.mismatch", { declared: health.difficulty.declared, observed: health.difficulty.observed })
				: t("suiteHealth.flags.difficulty_mismatch");
		case "infra_trouble":
			return t("suiteHealth.chip.infra", { rate: formatPercent(health.infra.rate, locale) ?? "" });
		case "timeouts":
			return t("suiteHealth.chip.timeouts", { rate: formatPercent(health.infra.timeout_rate, locale) ?? "" });
		default:
			return t(`suiteHealth.flags.${chip.key}`);
	}
}

/**
 * A task's health cell in the task table: quarantine and flags as chips, most serious first, with
 * agent variance on its own muted line so it never reads as a defect. A failed health request shows
 * "n/a" and never breaks the row.
 */
export function TaskHealthCell({ task, health }: { task: SuiteTask; health: HealthState }) {
	const { t } = useTranslation("bench");
	const { t: tf } = useTranslation("uf6a");
	const locale = getLocale();
	const testId = `suites-task-health-${task.id}`;
	if (health.state === "loading") {
		return (
			// M6-5: one status line above the tabs says health is loading; each cell shows only a dash.
			<span className="text-muted-foreground text-xs" data-testid={testId} data-state="loading">
				<span aria-hidden="true">-</span>
				<span className="sr-only">{t("suiteHealth.loading")}</span>
			</span>
		);
	}
	const taskHealth = health.state === "ready" ? health.byTask.get(task.id) : undefined;
	const quarantine = taskQuarantine(task, taskHealth);
	const chips = healthChips(taskHealth, quarantine);
	return (
		<span className="flex flex-col items-start gap-1" data-testid={testId} data-state={health.state}>
			{chips.length > 0 && (
				<span className="flex flex-wrap gap-1">
					{chips.map((chip) =>
						chip.key === "quarantined" && quarantine ? (
							<QuarantineBadge key={chip.key} quarantine={quarantine} testId={`${testId}-quarantined`} />
						) : (
							<ToneBadge
								key={chip.key}
								tone={chip.tone}
								icon={chip.key === "flaky_verifier" ? ShieldAlert : CircleAlert}
								hint={t(`suiteHealth.flagHints.${chip.key}`)}
								testId={`${testId}-${chip.key}`}
							>
								{chip.key === "insufficient_data" ? tf("health.tooFewChip") : chipLabel(chip, taskHealth, t, locale)}
							</ToneBadge>
						),
					)}
				</span>
			)}
			{health.state === "unavailable" && chips.length === 0 && (
				<span className="text-muted-foreground text-xs" title={t("suiteHealth.unavailable")}>
					{t("suiteHealth.unavailableCell")}
				</span>
			)}
			{health.state === "ready" && !taskHealth && chips.length === 0 && (
				<ToneBadge tone="neutral" icon={CircleDashed} hint={t("suiteHealth.noRunsHint")} testId={`${testId}-no-runs`}>
					{t("suiteHealth.noRuns")}
				</ToneBadge>
			)}
			{taskHealth && chips.length === 0 && (
				<ToneBadge tone="good" icon={CircleCheck} testId={`${testId}-ok`}>
					{t("suiteHealth.noIssues")}
				</ToneBadge>
			)}
			{taskHealth && taskHealth.variance.configs > 0 && (
				<span className="text-muted-foreground text-xs" title={t("suiteHealth.varianceHint")} data-testid={`${testId}-variance`}>
					{t("suiteHealth.variance", { unstable: taskHealth.variance.unstable, configs: taskHealth.variance.configs })}
				</span>
			)}
		</span>
	);
}

/** Why health is unavailable, in words: no run store, no report, or the server's message. */
export function healthUnavailableText(health: Extract<HealthState, { state: "unavailable" }>, t: TFunction<"bench">): string {
	if (health.reason === "error") return t("suiteHealth.reason.error", { message: getBenchErrorMessage(health.error) });
	return t(`suiteHealth.reason.${health.reason}`);
}

/**
 * The suite's health summary: held-out, quarantined and canary counts from the suite list (always
 * shown), then the health report's flag counts, thresholds and notes. When the report is unavailable
 * the counts stay and a note says why.
 */
export function SuiteHealthSummary({ suite, health }: { suite: SuiteSummary; health: HealthState }) {
	const { t } = useTranslation("bench");
	const { t: tf } = useTranslation("uf6a");
	const locale = getLocale();
	const { heldOut, quarantined } = heldOutAndQuarantined(suite);
	const stats: { key: string; label: string; hint?: string; value: React.ReactNode }[] = [
		{ key: "held-out", label: t("suiteHealth.stats.heldOut"), hint: t("suiteHealth.stats.heldOutHint"), value: heldOut },
		{ key: "quarantined", label: t("suiteHealth.stats.quarantined"), hint: t("suiteHealth.stats.quarantinedHint"), value: quarantined },
		{
			key: "canary",
			label: t("suiteHealth.stats.canary"),
			value: suite.has_canary ? t("suiteHealth.stats.canarySet") : t("suiteHealth.stats.canaryNone"),
		},
	];
	return (
		<section
			className="bg-card flex min-w-0 flex-col gap-3 rounded-sm border p-4"
			aria-labelledby="suites-health-heading"
			data-testid="suites-health"
		>
			<div className="flex min-w-0 flex-col gap-0.5">
				<h2 id="suites-health-heading" className="text-foreground text-sm font-semibold tracking-tight">
					{t("suiteHealth.title")}
				</h2>
				<p className="text-muted-foreground text-xs">{t("suiteHealth.description")}</p>
			</div>
			<dl className="flex flex-wrap gap-x-6 gap-y-2 text-xs" aria-label={t("suiteHealth.stats.label")}>
				{stats.map((stat) => (
					<div key={stat.key} className="flex flex-col gap-0.5" title={stat.hint} data-testid={`suites-health-${stat.key}`}>
						<dt className="text-muted-foreground">{stat.label}</dt>
						<dd className="text-foreground font-mono tabular-nums">{stat.value}</dd>
					</div>
				))}
			</dl>
			{health.state === "loading" ? (
				<p className="text-muted-foreground text-xs" data-testid="suites-health-loading">
					{t("suiteHealth.loading")}
				</p>
			) : health.state === "unavailable" ? (
				<p
					role="note"
					className="border-border bg-muted/50 text-foreground flex items-start gap-2 rounded-sm border px-3 py-2 text-xs"
					data-testid="suites-health-unavailable"
					data-reason={health.reason}
				>
					<CircleDashed className="text-muted-foreground mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
					<span className="min-w-0 break-words">
						<span className="font-medium">{t("suiteHealth.unavailable")}.</span> {healthUnavailableText(health, t)}
					</span>
				</p>
			) : health.health.runs === 0 ? (
				// M6-37: with no runs there is nothing to rate; one line says when health appears.
				<p className="text-muted-foreground text-sm" data-testid="suites-health-no-runs">
					{tf("health.noRuns", { count: health.health.thresholds.min_runs })}
				</p>
			) : (
				<div className="flex min-w-0 flex-col gap-2" data-testid="suites-health-report">
					<p className="text-muted-foreground text-xs">
						{t("suiteHealth.computed", {
							when: formatDateTime(health.health.computed_at, locale) ?? health.health.computed_at,
							runs: health.health.runs,
							experiments: health.health.experiments,
						})}
					</p>
					<FlagCounts counts={health.health.counts} />
					<details className="text-xs">
						<summary className="text-muted-foreground hover:text-foreground cursor-pointer">{tf("health.method")}</summary>
						<p className="text-muted-foreground mt-1 text-xs" data-testid="suites-health-thresholds">
							{t("suiteHealth.thresholds", {
								minRuns: health.health.thresholds.min_runs,
								minConfigs: health.health.thresholds.min_configs,
								easy: formatPercent(health.health.thresholds.easy_above, locale) ?? "",
								hard: formatPercent(health.health.thresholds.hard_below, locale) ?? "",
								level: formatPercent(health.health.thresholds.interval_level, locale) ?? "",
								infra: formatPercent(health.health.thresholds.infra_rate, locale) ?? "",
								timeout: formatPercent(health.health.thresholds.timeout_rate, locale) ?? "",
							})}
						</p>
					</details>
					{health.health.notes.length > 0 && (
						<div className="flex min-w-0 flex-col gap-0.5 text-xs" data-testid="suites-health-notes">
							<span className="text-muted-foreground">{t("suiteHealth.notes")}</span>
							<ul className="text-foreground list-disc pl-5">
								{health.health.notes.map((note) => (
									<li key={note} className="break-words">
										{note}
									</li>
								))}
							</ul>
						</div>
					)}
				</div>
			)}
		</section>
	);
}

/** Tasks per health flag; the flaky verifier count is red, everything else amber or grey. */
function FlagCounts({ counts }: { counts: Record<string, number> }) {
	const { t } = useTranslation("bench");
	const { t: tf } = useTranslation("uf6a");
	const rows = flagCounts(counts);
	if (rows.length === 0) {
		return (
			<p className="text-success-ink text-xs" data-testid="suites-health-no-flags">
				{t("suiteHealth.noFlags")}
			</p>
		);
	}
	return (
		<ul className="flex flex-wrap gap-1.5" data-testid="suites-health-counts">
			{rows.map(({ flag, count }) => {
				const known = flag in HEALTH_FLAG_TONE;
				return (
					<li key={flag}>
						<ToneBadge
							tone={known ? HEALTH_FLAG_TONE[flag as HealthFlag] : "neutral"}
							icon={flag === "flaky_verifier" ? ShieldAlert : undefined}
							hint={known ? t(`suiteHealth.flagHints.${flag as HealthFlag}`) : undefined}
							testId={`suites-health-count-${flag}`}
							className={cn("tabular-nums")}
						>
							{/* M6-9: "3 tasks have too few runs to rate", not "New: not enough runs yet: 3". */}
						{flag === "insufficient_data"
							? tf("health.tooFewRuns", { count })
							: `${known ? t(`suiteHealth.flags.${flag as HealthFlag}`) : flag}: ${count}`}
						</ToneBadge>
					</li>
				);
			})}
		</ul>
	);
}