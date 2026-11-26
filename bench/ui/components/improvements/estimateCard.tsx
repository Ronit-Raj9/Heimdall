import type { ImprovementEstimate } from "@/lib/types/improve";
import { useTranslation } from "react-i18next";
import { detectablePoints, minutes, money } from "./improve.utils";
import "./w45impI18n";

/** The first refusal of an estimate: the reason Create is disabled. undefined when it allows a run. */
export function estimateBlock(e: ImprovementEstimate | undefined): string | undefined {
	return e?.refusals?.[0]?.message;
}

export interface EstimateCardProps {
	estimate?: ImprovementEstimate;
	loading?: boolean;
	/** The estimate route's error text. */
	error?: string;
	/** The model the improver runs on: the agent's own model. */
	model: string;
	/** A teacher model, when the run sets one. */
	teacher?: string;
	/** True while the form is not complete enough to estimate. */
	waiting?: boolean;
}

/** What the run will cost and can confirm (A2.9), refreshed by the form after each change. */
export function EstimateCard({ estimate: e, loading, error, model, teacher, waiting }: EstimateCardProps) {
	const { t } = useTranslation("w45imp");
	let body;
	if (loading) {
		body = <p className="text-muted-foreground">{t("estimate.loading")}</p>;
	} else if (error) {
		body = <p className="text-muted-foreground">{t("estimate.unavailable", { error })}</p>;
	} else if (!e || waiting) {
		body = <p className="text-muted-foreground">{t("estimate.waiting")}</p>;
	} else {
		const held = detectablePoints(e.detectable.held_out_points / 100, e.splits.held_out);
		body = (
			<>
				<p>{t("estimate.splits", { pool: e.pool, practice: e.splits.practice, gate: e.splits.gate, held_out: e.splits.held_out })}</p>
				<p>{held ? t("estimate.detectable", { held_out_points: held }) : t("estimate.notEnough")}</p>
				<p>
					{t("estimate.cost", {
						low: money(e.usd.low),
						high: money(e.usd.high),
						minutes_low: minutes(e.minutes.low),
						minutes_high: minutes(e.minutes.high),
						limit: money(e.usd.limit),
					})}
				</p>
				<p>{teacher ? t("estimate.improverTeacher", { teacher }) : t("estimate.improver", { model: model || "-" })}</p>
				{(e.warnings ?? []).map((w) => (
					<p key={w.code} className="text-chart-warning-ink" data-testid="estimate-warning">
						{w.message}
					</p>
				))}
				{(e.refusals ?? []).map((r) => (
					<p key={r.code} className="text-chart-error-ink" role="alert" data-testid="estimate-refusal">
						{r.message}
					</p>
				))}
			</>
		);
	}
	return (
		<section
			className="bg-muted/30 flex flex-col gap-1 rounded-md border p-3 text-xs"
			aria-live="polite"
			data-testid="new-improvement-estimate"
		>
			<h3 className="text-foreground font-medium">{t("estimate.title")}</h3>
			{body}
		</section>
	);
}