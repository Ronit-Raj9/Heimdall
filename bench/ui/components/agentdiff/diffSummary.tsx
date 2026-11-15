import { ToneBadge } from "@/components/suites/suiteBadges";
import { TONE_TEXT, formatPValue, shortHash } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import { AGENT_DIFF_CLASSES, type AgentDiffReport, type AgentDiffVersion } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { CLASS_TONE, CONCLUSION_TONE, formatRate } from "./agentDiff.utils";

/**
 * The answer in plain words: the server's summary sentence, the test's conclusion with its p value
 * (or why there is none), the net change and the class counts.
 */
export function DiffSummary({ report }: { report: AgentDiffReport }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const { outcome } = report;
	const test = outcome.test;
	const tone = CONCLUSION_TONE[test.conclusion] ?? "neutral";
	const testLine =
		test.p === null
			? t("agentDiff.test.noP")
			: t("agentDiff.test.p", {
					p: formatPValue(test.p, locale),
					alpha: formatPValue(test.alpha, locale),
					changed: test.n_nonzero,
					total: test.n_tasks,
				});
	// With few changed tasks no p can reach alpha; say so rather than leave "cannot tell" unexplained.
	const floorLine =
		test.conclusion === "cannot_tell" && test.n_nonzero > 0 && test.min_possible_p > test.alpha
			? t("agentDiff.test.minP", {
					count: test.n_nonzero,
					p: formatPValue(test.min_possible_p, locale),
					alpha: formatPValue(test.alpha, locale),
				})
			: null;
	const netTone = outcome.net > 0 ? "good" : outcome.net < 0 ? "bad" : "neutral";
	return (
		<section className="bg-card flex min-w-0 flex-col gap-3 rounded-sm border p-4" data-testid="agentdiff-summary">
			<p className="text-foreground text-base leading-relaxed font-medium" data-testid="agentdiff-summary-text">
				{report.summary}
			</p>
			<div className="flex flex-wrap items-center gap-2">
				<ToneBadge tone={tone} testId="agentdiff-conclusion" className="text-sm">
					{t(`agentDiff.conclusion.${test.conclusion}`, { defaultValue: test.conclusion })}
				</ToneBadge>
				<span className={cn("text-sm font-semibold tabular-nums", TONE_TEXT[netTone])} data-testid="agentdiff-net">
					{t("agentDiff.summary.net", { value: new Intl.NumberFormat(locale, { signDisplay: "exceptZero" }).format(outcome.net) })}
				</span>
				<span className="text-muted-foreground text-sm tabular-nums" data-testid="agentdiff-pass-rates">
					{t("agentDiff.summary.passRate", {
						a: formatRate(outcome.pass_rate_a, locale),
						b: formatRate(outcome.pass_rate_b, locale),
						count: outcome.common_tasks,
					})}
				</span>
			</div>
			<p className="text-muted-foreground text-xs" data-testid="agentdiff-test">
				{testLine}
				{!test.exact && test.p !== null && <span> {t("agentDiff.test.approx")}</span>}
				{floorLine && <span className="block">{floorLine}</span>}
			</p>
			<ul className="flex flex-wrap gap-1.5" aria-label={t("agentDiff.summary.counts")} data-testid="agentdiff-counts">
				{AGENT_DIFF_CLASSES.map((cls) => {
					const count = outcome.counts[cls] ?? 0;
					return (
						<li key={cls}>
							<ToneBadge
								tone={count > 0 ? CLASS_TONE[cls] : "neutral"}
								hint={t(`agentDiff.classHint.${cls}`)}
								testId={`agentdiff-count-${cls}`}
								className={cn(count === 0 && "opacity-60")}
							>
								<span className="tabular-nums">{count}</span> {t(`agentDiff.class.${cls}`)}
							</ToneBadge>
						</li>
					);
				})}
			</ul>
		</section>
	);
}

/** A and B side by side: label, hash, agent, arm, verified runs, experiments and the runs left out. */
export function VersionCards({ report }: { report: AgentDiffReport }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid="agentdiff-versions">
			<p className="text-muted-foreground text-xs" data-testid="agentdiff-scope">
				{t("agentDiff.version.scope", { suite: report.suite, condition: report.condition })}
				{report.synthetic && <span> {t("agentDiff.version.syntheticIncluded")}</span>}
			</p>
			<div className="grid min-w-0 grid-cols-1 gap-3 md:grid-cols-2">
				<VersionCard side="a" version={report.a} />
				<VersionCard side="b" version={report.b} />
			</div>
		</div>
	);
}

function VersionCard({ side, version }: { side: "a" | "b"; version: AgentDiffVersion }) {
	const { t } = useTranslation("bench");
	const excluded = version.excluded ?? [];
	const experiments = version.experiments ?? [];
	return (
		<article className="bg-card flex min-w-0 flex-col gap-2 rounded-sm border p-3" data-testid={`agentdiff-version-${side}`}>
			<header className="flex min-w-0 flex-wrap items-center gap-2">
				<span className="bg-muted text-foreground rounded-sm px-1.5 py-0.5 text-xs font-semibold">{t(`agentDiff.version.${side}`)}</span>
				<h2 className="text-foreground truncate text-sm font-semibold">{version.label || version.agent_id}</h2>
				<span className="text-muted-foreground font-mono text-[11px]" title={version.config_hash}>
					{shortHash(version.config_hash)}
				</span>
			</header>
			<dl className="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
				<dt className="text-muted-foreground">{t("agentDiff.version.agent")}</dt>
				<dd className="text-foreground font-mono">{version.agent_id}</dd>
				{version.exp_id && (
					<>
						<dt className="text-muted-foreground">{t("agentDiff.version.arm")}</dt>
						<dd className="text-foreground font-mono">
							{t("agentDiff.version.armValue", { arm: version.arm ?? "", exp: version.exp_id })}
						</dd>
					</>
				)}
				<dt className="text-muted-foreground">{t("agentDiff.version.runs")}</dt>
				<dd className="text-foreground tabular-nums">{version.runs}</dd>
				<dt className="text-muted-foreground">{t("agentDiff.version.experiments")}</dt>
				<dd className="text-foreground min-w-0 font-mono break-all">
					{experiments.length > 0 ? experiments.join(", ") : t("agentDiff.version.noExperiments")}
				</dd>
				<dt className="text-muted-foreground">{t("agentDiff.version.excluded")}</dt>
				<dd className="text-foreground min-w-0" data-testid={`agentdiff-version-${side}-excluded`}>
					{excluded.length === 0 ? (
						<span className="text-muted-foreground">{t("agentDiff.version.noneExcluded")}</span>
					) : (
						<ul className="flex flex-col gap-0.5">
							{excluded.map((item) => (
								<li key={item.reason}>{t("agentDiff.version.excludedItem", { reason: item.reason, count: item.runs })}</li>
							))}
						</ul>
					)}
				</dd>
			</dl>
		</article>
	);
}