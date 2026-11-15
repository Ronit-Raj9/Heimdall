import { formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { DifficultyHealth, InfraHealth, OutcomeVariance, TaskQuarantine, VerifierHealth } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { CircleCheck, CircleDashed, ShieldAlert, ShieldOff } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ToneBadge } from "./suiteBadges";
import { healthUnavailableText } from "./suiteHealth";
import { TONE_CLASS } from "./suites.utils";
import { difficultyMatchTone, evidenceKinds, formatPercent, hasNoInformation, type HealthState } from "./suiteHealth.utils";

export interface TaskHealthBlockProps {
	taskId: string;
	/** The task's quarantine from the suite list; the health report's is used when this is null. */
	quarantine: TaskQuarantine | null;
	health: HealthState;
}

/**
 * The open task's health: quarantine, verifier, difficulty, discrimination, infra and, kept apart
 * under its own heading, agent variance. It stands outside the task detail fetch, so a held-out task
 * whose detail is not served still shows its health.
 */
export function TaskHealthBlock({ taskId, quarantine, health }: TaskHealthBlockProps) {
	const { t } = useTranslation("bench");
	const taskHealth = health.state === "ready" ? health.byTask.get(taskId) : undefined;
	const q = quarantine ?? taskHealth?.quarantine ?? null;
	return (
		<section className="flex min-w-0 flex-col gap-3" data-testid="suites-task-health" data-state={health.state}>
			<h3 className="text-foreground text-xs font-semibold tracking-wide uppercase">{t("suiteHealth.detail.title")}</h3>
			{q && <QuarantineDetail quarantine={q} />}
			{health.state === "loading" ? (
				<p className="text-muted-foreground text-xs">{t("suiteHealth.loading")}</p>
			) : health.state === "unavailable" ? (
				<p className="text-muted-foreground text-xs" data-testid="suites-task-health-unavailable">
					<span className="text-foreground font-medium">{t("suiteHealth.unavailable")}.</span> {healthUnavailableText(health, t)}
				</p>
			) : !taskHealth ? (
				<p className="text-muted-foreground text-xs" data-testid="suites-task-health-no-runs">
					{t("suiteHealth.detail.noRuns")}
				</p>
			) : (
				<>
					<VerifierDetail verifier={taskHealth.verifier} />
					<DifficultyDetail difficulty={taskHealth.difficulty} level={health.health.thresholds.interval_level} />
					<InfraDetail infra={taskHealth.infra} />
					<VarianceDetail variance={taskHealth.variance} />
				</>
			)}
		</section>
	);
}

function Sub({ title, children, testId, className }: { title: string; children: React.ReactNode; testId: string; className?: string }) {
	return (
		<div className={cn("flex min-w-0 flex-col gap-1 rounded-sm border px-3 py-2", className)} data-testid={testId}>
			<h4 className="text-foreground text-xs font-medium">{title}</h4>
			{children}
		</div>
	);
}

function Facts({ rows }: { rows: { key: string; label: string; value: React.ReactNode; className?: string }[] }) {
	return (
		<dl className="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-0.5 text-xs">
			{rows.map((row) => (
				<div key={row.key} className="contents" data-testid={`suites-task-health-${row.key}`}>
					<dt className="text-muted-foreground">{row.label}</dt>
					<dd className={cn("text-foreground min-w-0 font-mono break-words", row.className)}>{row.value}</dd>
				</div>
			))}
		</dl>
	);
}

function QuarantineDetail({ quarantine }: { quarantine: TaskQuarantine }) {
	const { t } = useTranslation("bench");
	const source =
		quarantine.source === "manual" || quarantine.source === "auto"
			? t(`suiteHealth.quarantine.source.${quarantine.source}`)
			: quarantine.source;
	const rows = [
		{ key: "q-reason", label: t("suiteHealth.detail.reason"), value: quarantine.reason, className: "font-sans" },
		{ key: "q-source", label: t("suiteHealth.detail.source"), value: source, className: "font-sans" },
		{ key: "q-at", label: t("suiteHealth.detail.at"), value: formatDateTime(quarantine.at, getLocale()) ?? quarantine.at },
		...(quarantine.task_version ? [{ key: "q-version", label: t("suiteHealth.detail.version"), value: quarantine.task_version }] : []),
	];
	return (
		<Sub title={t("suiteHealth.detail.quarantineTitle")} testId="suites-task-health-quarantine" className={TONE_CLASS.warn}>
			<p className="text-foreground flex items-start gap-1.5 text-xs">
				<ShieldOff className="text-chart-warning-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
				{t("suiteHealth.detail.quarantineBody")}
			</p>
			<Facts rows={rows} />
			{quarantine.evidence && quarantine.evidence.length > 0 && (
				<div className="flex min-w-0 flex-col gap-0.5 text-xs">
					<span className="text-muted-foreground">{t("suiteHealth.detail.evidence")}</span>
					<ul className="flex min-w-0 flex-col gap-0.5 font-mono text-[11px]">
						{quarantine.evidence.map((line) => (
							<li key={line} className="text-foreground break-all">
								{line}
							</li>
						))}
					</ul>
				</div>
			)}
		</Sub>
	);
}

/** The verifier's evidence, grouped by kind. Flaky is red and serious: it means verdicts cannot be trusted. */
function VerifierDetail({ verifier }: { verifier: VerifierHealth }) {
	const { t } = useTranslation("bench");
	const kinds = evidenceKinds(verifier);
	const known = (kind: string): kind is "regrade_disagrees" | "same_output_differs" | "admission_reference" | "oracle_failed" =>
		kind === "regrade_disagrees" || kind === "same_output_differs" || kind === "admission_reference" || kind === "oracle_failed";
	return (
		<Sub
			title={t("suiteHealth.detail.verifierTitle")}
			testId="suites-task-health-verifier"
			className={verifier.flaky ? TONE_CLASS.bad : undefined}
		>
			{verifier.flaky ? (
				<>
					<p className="text-chart-error-ink flex items-start gap-1.5 text-xs font-medium" role="alert">
						<ShieldAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
						{t("suiteHealth.detail.flakyBody", { count: verifier.evidence.length, checks: verifier.checks })}
					</p>
					<ul className="flex flex-wrap gap-1" data-testid="suites-task-health-evidence-kinds">
						{kinds.map(({ kind, count }) => (
							<li key={kind}>
								<ToneBadge tone="bad" testId={`suites-task-health-kind-${kind}`}>
									{known(kind) ? t(`suiteHealth.detail.evidenceKind.${kind}`) : kind}: {count}
								</ToneBadge>
							</li>
						))}
					</ul>
					<ul className="flex min-w-0 flex-col gap-1 text-[11px]" data-testid="suites-task-health-evidence">
						{verifier.evidence.map((item, i) => (
							<li key={`${item.kind}-${i}`} className="text-foreground flex min-w-0 flex-col gap-0.5 border-l-2 border-current/30 pl-2">
								<span className="font-medium">{known(item.kind) ? t(`suiteHealth.detail.evidenceKind.${item.kind}`) : item.kind}</span>
								<span className="break-words">{item.detail}</span>
								{(item.exp_id || (item.run_ids && item.run_ids.length > 0)) && (
									<span className="text-muted-foreground font-mono break-all">
										{[
											item.exp_id ? t("suiteHealth.detail.evidenceExp", { exp: item.exp_id }) : null,
											item.run_ids && item.run_ids.length > 0
												? t("suiteHealth.detail.evidenceRuns", { runs: item.run_ids.join(", ") })
												: null,
										]
											.filter(Boolean)
											.join("; ")}
									</span>
								)}
							</li>
						))}
					</ul>
				</>
			) : verifier.checks > 0 ? (
				<p className="text-chart-success-ink flex items-start gap-1.5 text-xs">
					<CircleCheck className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
					{t("suiteHealth.detail.verifierStable", { count: verifier.checks })}
				</p>
			) : (
				<p className="text-muted-foreground flex items-start gap-1.5 text-xs">
					<CircleDashed className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
					{t("suiteHealth.detail.verifierUnchecked")}
				</p>
			)}
		</Sub>
	);
}

/** Declared against observed difficulty, with the pooled pass rate, its interval and how much data it rests on. */
function DifficultyDetail({ difficulty, level }: { difficulty: DifficultyHealth; level: number }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const tone = difficultyMatchTone(difficulty.match);
	const observed = ["easy", "medium", "hard", "unknown"].includes(difficulty.observed)
		? t(`suiteHealth.detail.observedValue.${difficulty.observed}`)
		: difficulty.observed;
	const unknown = t("suiteHealth.detail.unknown");
	const interval = difficulty.interval
		? t("suiteHealth.detail.intervalValue", {
				low: formatPercent(difficulty.interval[0], locale) ?? "",
				high: formatPercent(difficulty.interval[1], locale) ?? "",
			})
		: unknown;
	const match = ["match", "mismatch", "undeclared", "unknown"].includes(difficulty.match)
		? t(`suiteHealth.detail.match.${difficulty.match}`)
		: difficulty.match;
	const discrimination = ["discriminating", "always_pass", "always_fail", "single_config", "unknown"].includes(difficulty.discrimination)
		? t(`suiteHealth.detail.discrimination.${difficulty.discrimination}`)
		: difficulty.discrimination;
	return (
		<>
			<Sub
				title={t("suiteHealth.detail.difficultyTitle")}
				testId="suites-task-health-difficulty"
				className={difficulty.match === "mismatch" ? TONE_CLASS.warn : undefined}
			>
				<Facts
					rows={[
						{ key: "declared", label: t("suiteHealth.detail.declared"), value: difficulty.declared || t("suiteHealth.detail.unset") },
						{
							key: "observed",
							label: t("suiteHealth.detail.observed"),
							value: observed,
							className: difficulty.match === "mismatch" ? "text-chart-warning-ink font-semibold" : undefined,
						},
						{ key: "pass-rate", label: t("suiteHealth.detail.passRate"), value: formatPercent(difficulty.pass_rate, locale) ?? unknown },
						{
							key: "interval",
							label: t("suiteHealth.detail.interval", { level: formatPercent(level, locale) ?? "" }),
							value: interval,
						},
						{ key: "runs", label: t("suiteHealth.detail.runs"), value: difficulty.runs },
						{ key: "configs", label: t("suiteHealth.detail.configs"), value: difficulty.configs },
					]}
				/>
				<p className="text-xs" data-testid="suites-task-health-match" data-tone={tone}>
					<span className={cn(tone === "warn" ? "text-chart-warning-ink font-medium" : "text-muted-foreground")}>{match}</span>
				</p>
			</Sub>
			<Sub
				title={t("suiteHealth.detail.discriminationTitle")}
				testId="suites-task-health-discrimination"
				className={hasNoInformation(difficulty) ? TONE_CLASS.warn : undefined}
			>
				<p className="text-xs" data-state={difficulty.discrimination}>
					<span className={hasNoInformation(difficulty) ? "text-chart-warning-ink font-medium" : "text-foreground"}>{discrimination}</span>
				</p>
				{difficulty.spread !== null && (
					<Facts
						rows={[{ key: "spread", label: t("suiteHealth.detail.spread"), value: formatPercent(difficulty.spread, locale) ?? unknown }]}
					/>
				)}
			</Sub>
		</>
	);
}

function InfraDetail({ infra }: { infra: InfraHealth }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const infraCount = infra.aborted_infra + infra.infra_error + infra.invalid_evaluator;
	return (
		<Sub title={t("suiteHealth.detail.infraTitle")} testId="suites-task-health-infra">
			<Facts
				rows={[
					{
						key: "infra-rate",
						label: t("suiteHealth.detail.infraRate"),
						value: t("suiteHealth.detail.rateValue", {
							rate: formatPercent(infra.rate, locale) ?? "",
							count: infraCount,
							attempts: infra.attempts,
						}),
					},
					{
						key: "timeout-rate",
						label: t("suiteHealth.detail.timeoutRate"),
						value: t("suiteHealth.detail.rateValue", {
							rate: formatPercent(infra.timeout_rate, locale) ?? "",
							count: infra.timeouts,
							attempts: infra.attempts,
						}),
					},
				]}
			/>
			<p className="text-muted-foreground text-[11px]">
				{t("suiteHealth.detail.infraBreakdown", {
					aborted: infra.aborted_infra,
					errors: infra.infra_error,
					invalid: infra.invalid_evaluator,
				})}
			</p>
		</Sub>
	);
}

/** Agent variance: neutral styling and its own words, so it never reads as a verifier defect. */
function VarianceDetail({ variance }: { variance: OutcomeVariance }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	return (
		<Sub title={t("suiteHealth.detail.varianceTitle")} testId="suites-task-health-variance" className="bg-muted/30">
			<p className="text-muted-foreground text-xs">{t("suiteHealth.detail.varianceBody")}</p>
			{variance.per_config.length === 0 ? (
				<p className="text-muted-foreground text-xs">{t("suiteHealth.detail.varianceNone")}</p>
			) : (
				<>
					<p className="text-foreground text-xs">
						{t("suiteHealth.detail.varianceSummary", {
							unstable: variance.unstable,
							configs: variance.configs,
							mean: variance.mean_variance.toLocaleString(locale, { maximumFractionDigits: 3 }),
						})}
					</p>
					<ul className="flex min-w-0 flex-col gap-0.5 text-[11px]">
						{variance.per_config.map((config) => (
							<li
								key={config.config_hash || config.config}
								className="flex min-w-0 items-center justify-between gap-3"
								data-testid={`suites-task-health-config-${config.config}`}
							>
								<span className="text-foreground min-w-0 truncate font-mono" title={config.config_hash}>
									{config.config}
								</span>
								<span className="text-muted-foreground shrink-0 tabular-nums">
									{t("suiteHealth.detail.varianceConfig", { passes: config.passes, trials: config.trials })} (
									{formatPercent(config.pass_rate, locale)})
								</span>
							</li>
						))}
					</ul>
				</>
			)}
		</Sub>
	);
}