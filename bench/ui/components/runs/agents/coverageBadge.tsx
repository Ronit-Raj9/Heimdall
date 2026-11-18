import { ToneBadge } from "@/components/suites/suiteBadges";
import { formatCount } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { AgentCoverage } from "@/lib/types/bench";
import { clipText } from "@/components/runs/stepTimeline.utils";
import { Fingerprint } from "lucide-react";
import { useTranslation } from "react-i18next";
import { sourceLabel } from "./agentBadges";
import { SOURCES, STRENGTHS, coveragePercent, coverageTone, formatPercent, knownStrength, orderedCounts } from "./agents.utils";

const STRENGTH_FILL: Record<string, string> = {
	proven: "var(--chart-success)",
	declared: "var(--chart-info)",
	reported: "var(--chart-warning)",
};

/**
 * How much of the step timeline carries an agent id: attributed over steps as a percent, toned
 * (95% and up good, 60% and up a warning, else bad), with a meter split by strength and the
 * unknown rest. The badge's hint (title and accessible name) spells out every strength and source.
 */
export function CoverageBadge({ coverage, testId }: { coverage: AgentCoverage; testId: string }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const percent = coveragePercent(coverage);
	const tone = coverageTone(percent);
	const strengths = orderedCounts(coverage.by_strength, STRENGTHS);
	const sources = orderedCounts(coverage.by_source, SOURCES);
	const strengthText = (key: string) => {
		const known = knownStrength(key);
		return known ? t(`runs.agents.strength.${known}`) : clipText(key, 20);
	};
	const parts = [
		strengths.length > 0 &&
			`${t("runs.agents.coverage.byStrength")} ${strengths.map(([k, n]) => `${strengthText(k)} ${formatCount(n, locale)}`).join(", ")}`,
		sources.length > 0 &&
			`${t("runs.agents.coverage.bySource")} ${sources.map(([k, n]) => `${sourceLabel(t, k)} ${formatCount(n, locale)}`).join(", ")}`,
		t("runs.agents.coverage.unknownCount", { value: formatCount(coverage.unknown, locale) }),
	].filter((part): part is string => Boolean(part));
	const steps = coverage.steps > 0 ? coverage.steps : 0;

	return (
		<div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5" data-testid={testId} data-tone={tone}>
			<ToneBadge tone={tone} icon={Fingerprint} hint={parts.join(". ")} testId={`${testId}-badge`}>
				{percent === null
					? t("runs.agents.coverage.noSteps")
					: t("runs.agents.coverage.value", { percent: formatPercent(percent, locale) })}
			</ToneBadge>
			{steps > 0 && (
				<div
					className="bg-chart-track bench-hatched flex h-2 w-28 shrink-0 overflow-hidden rounded-full"
					role="img"
					aria-label={parts.join(". ")}
					data-testid={`${testId}-meter`}
				>
					{strengths.map(([key, n]) => (
						<span
							key={key}
							className="h-full"
							style={{ width: `${Math.min(100, (n / steps) * 100)}%`, background: STRENGTH_FILL[key] ?? "var(--chart-cat-other)" }}
							title={`${strengthText(key)} ${formatCount(n, locale)}`}
						/>
					))}
				</div>
			)}
			<span className="text-muted-foreground text-xs tabular-nums">
				{t("runs.agents.coverage.counts", {
					attributed: formatCount(coverage.attributed, locale),
					steps: formatCount(coverage.steps, locale),
				})}
				{coverage.unknown > 0 && (
					<span className="text-chart-warning-ink" data-testid={`${testId}-unknown`}>
						{" "}
						{t("runs.agents.coverage.unknownCount", { value: formatCount(coverage.unknown, locale) })}
					</span>
				)}
			</span>
			{strengths.length > 0 && (
				<ul
					className="text-muted-foreground flex flex-wrap items-center gap-x-2.5 text-[11px]"
					aria-label={t("runs.agents.coverage.byStrength")}
				>
					{strengths.map(([key, n]) => (
						<li key={key} className="inline-flex items-center gap-1" data-testid={`${testId}-strength-${key}`}>
							<span
								aria-hidden="true"
								className="size-2 rounded-full"
								style={{ background: STRENGTH_FILL[key] ?? "var(--chart-cat-other)" }}
							/>
							{strengthText(key)} <span className="text-foreground tabular-nums">{formatCount(n, locale)}</span>
						</li>
					))}
				</ul>
			)}
		</div>
	);
}