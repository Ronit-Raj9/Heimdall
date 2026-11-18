import { ToneBadge } from "@/components/suites/suiteBadges";
import { useGetExperimentResearchQuery } from "@/lib/store/apis/researchApi";
import type { ResearchArm, ResearchSummary } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

/**
 * W4 research with Tavily (docs/research-tasks.md). The verdict page's research scorecard, fetched by
 * experiment id. Renders nothing while loading, on an error, or for an experiment whose runs made no
 * Tavily searches.
 */
export function ExperimentResearchScorecard({ expId }: { expId: string }) {
	const q = useGetExperimentResearchQuery(expId);
	if (!q.data?.research) return null;
	return <ResearchScorecard summary={q.data} />;
}

/** Verified share, searches, citations, grounded citations and citation precision for every arm. */
export function ResearchScorecard({ summary }: { summary: ResearchSummary }) {
	const { t } = useTranslation("bench");
	return (
		<section
			className="flex min-w-0 flex-col gap-3 rounded-md border p-3 text-xs"
			data-testid="research-scorecard"
			aria-label={t("research.title")}
		>
			<div className="flex flex-wrap items-center gap-2">
				<p className="text-sm font-medium">{t("research.title")}</p>
				<ToneBadge tone={summary.replay ? "neutral" : "warn"}>
					<span data-testid="research-search-mode">{t(summary.replay ? "research.replay" : "research.live")}</span>
				</ToneBadge>
				{summary.synthetic ? <ToneBadge tone="warn">{t("research.synthetic")}</ToneBadge> : null}
			</div>
			<p className="text-muted-foreground">{t("research.description", { condition: summary.condition })}</p>
			<div className="grid gap-3 md:grid-cols-2">
				{summary.arms.map((arm) => (
					<ArmCards key={arm.arm} arm={arm} />
				))}
			</div>
		</section>
	);
}

function ArmCards({ arm }: { arm: ResearchArm }) {
	const { t } = useTranslation("bench");
	const cards: { key: string; label: string; value: string; hint?: string }[] = [
		{ key: "verified", label: t("research.verified"), value: formatRate(arm.verified), hint: t("research.passedOf", { passed: arm.passed, decided: arm.decided }) },
		{ key: "grounded", label: t("research.grounded"), value: formatRate(arm.citations_grounded), hint: t("research.groundedHint") },
		{ key: "precision", label: t("research.precision"), value: formatRate(arm.citation_precision) },
		{ key: "searches", label: t("research.searches"), value: formatMean(arm.searches), hint: t("research.cited", { cited: formatMean(arm.sources_cited) }) },
	];
	return (
		<div className="flex min-w-0 flex-col gap-2 rounded border p-2" data-testid={`research-arm-${arm.arm}`}>
			<p className="font-medium">
				{arm.arm} <span className="text-muted-foreground">{arm.label}</span>{" "}
				<span className="text-muted-foreground">{t("research.runs", { count: arm.runs })}</span>
			</p>
			<div className="grid grid-cols-2 gap-2">
				{cards.map((c) => (
					<div key={c.key} className="rounded border p-2" data-testid={`research-${arm.arm}-${c.key}`}>
						<p className="text-muted-foreground">{c.label}</p>
						<p className="text-base font-semibold tabular-nums">{c.value}</p>
						{c.hint ? <p className="text-muted-foreground">{c.hint}</p> : null}
					</div>
				))}
			</div>
			{arm.external_unmatched !== null && arm.external_unmatched > 0 ? (
				<p className="text-muted-foreground" data-testid={`research-${arm.arm}-unmatched`}>
					{t("research.unmatched", { mean: formatMean(arm.external_unmatched) })}
				</p>
			) : null}
		</div>
	);
}

/** A share as a whole percent; "unknown" for null. */
export function formatRate(v: number | null): string {
	return v === null ? "unknown" : `${Math.round(v * 100)}%`;
}

/** A mean per run with one decimal; "unknown" for null. */
export function formatMean(v: number | null): string {
	return v === null ? "unknown" : v.toFixed(1);
}
