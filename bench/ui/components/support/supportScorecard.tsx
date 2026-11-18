import { ToneBadge } from "@/components/suites/suiteBadges";
import { useGetExperimentSupportQuery } from "@/lib/store/apis/supportApi";
import type { SupportArm, SupportSummary } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

/**
 * W3 customer support (docs/customer-support.md). The verdict page's support scorecard, fetched by
 * experiment id. Renders nothing while loading, on an error, or for an experiment that is not a
 * support suite: there, verified success keeps its own name.
 */
export function ExperimentSupportScorecard({ expId }: { expId: string }) {
	const q = useGetExperimentSupportQuery(expId);
	if (!q.data?.support) return null;
	return <SupportScorecard summary={q.data} />;
}

/** Resolution, cost per resolution, escalation and policy violation rate cards for every arm. */
export function SupportScorecard({ summary }: { summary: SupportSummary }) {
	const { t } = useTranslation("bench");
	const llm = summary.simulator_mode === "llm";
	return (
		<section
			className="flex min-w-0 flex-col gap-3 rounded-md border p-3 text-xs"
			data-testid="support-scorecard"
			aria-label={t("support.title")}
		>
			<div className="flex flex-wrap items-center gap-2">
				<p className="text-sm font-medium">{t("support.title")}</p>
				<ToneBadge tone={llm ? "warn" : "neutral"}>
					<span data-testid="support-simulator-label">{summary.simulator_label}</span>
				</ToneBadge>
				{summary.synthetic ? <ToneBadge tone="warn">{t("support.synthetic")}</ToneBadge> : null}
			</div>
			<p className="text-muted-foreground">{t(llm ? "support.descriptionLLM" : "support.description", { condition: summary.condition })}</p>
			<div className="grid gap-3 md:grid-cols-2">
				{summary.arms.map((arm) => (
					<ArmCards key={arm.arm} arm={arm} />
				))}
			</div>
		</section>
	);
}

function ArmCards({ arm }: { arm: SupportArm }) {
	const { t } = useTranslation("bench");
	const cards: { key: string; label: string; value: string; hint?: string }[] = [
		{ key: "resolution", label: t("support.resolution"), value: formatRate(arm.resolution), hint: t("support.resolvedOf", { resolved: arm.resolved, decided: arm.decided }) },
		{ key: "cost", label: t("support.costPerResolution"), value: formatUSD(arm.cost_per_resolution) },
		{ key: "escalation", label: t("support.escalationRate"), value: formatRate(arm.escalation_rate), hint: arm.escalation_correct === null ? undefined : t("support.escalationCorrect", { rate: formatRate(arm.escalation_correct) }) },
		{ key: "policy", label: t("support.policyViolationRate"), value: formatRate(arm.policy_violation_rate) },
	];
	return (
		<div className="flex min-w-0 flex-col gap-2 rounded border p-2" data-testid={`support-arm-${arm.arm}`}>
			<p className="font-medium">
				{arm.arm} <span className="text-muted-foreground">{arm.label}</span>{" "}
				<span className="text-muted-foreground">{t("support.runs", { count: arm.runs })}</span>
			</p>
			<div className="grid grid-cols-2 gap-2">
				{cards.map((c) => (
					<div key={c.key} className="rounded border p-2" data-testid={`support-${arm.arm}-${c.key}`}>
						<p className="text-muted-foreground">{c.label}</p>
						<p className="text-base font-semibold tabular-nums">{c.value}</p>
						{c.hint ? <p className="text-muted-foreground">{c.hint}</p> : null}
					</div>
				))}
			</div>
			<p className="text-muted-foreground">
				{t("support.extra", { turns: arm.user_turns === null ? "unknown" : arm.user_turns.toFixed(1), cost: formatUSD(arm.simulator_cost_usd) })}
			</p>
		</div>
	);
}

/** A share as a whole percent; "unknown" for null. */
export function formatRate(v: number | null): string {
	return v === null ? "unknown" : `${Math.round(v * 100)}%`;
}

/** US dollars with four decimals below a dollar; "unknown" for null. */
export function formatUSD(v: number | null): string {
	if (v === null) return "unknown";
	return v < 1 ? `$${v.toFixed(4)}` : `$${v.toFixed(2)}`;
}
