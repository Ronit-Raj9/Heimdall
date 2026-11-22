import { useGetEvaluationQuery, type Evaluation } from "@/lib/store/apis/evaluationsApi";
import "@/lib/types/agentFirstI18n";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { useTranslation } from "react-i18next";
import { faultName } from "./reportCard.utils";

export interface DroppedFault {
	fault: string;
	benchmark: string;
	reason: string;
}

/** Every chosen fault that did not apply to a benchmark of the evaluation, in child order. */
export function droppedFaults(evaluation: Pick<Evaluation, "children"> | undefined): DroppedFault[] {
	return (evaluation?.children ?? []).flatMap((c) =>
		(c.faults_skipped ?? []).map((s) => ({ fault: s.fault, benchmark: c.benchmark, reason: s.reason })),
	);
}

/** "Faults that did not run": the chosen faults a benchmark could not take, so they add no row above. */
export function DroppedFaults({ evaluationId }: { evaluationId: string }) {
	const { t } = useTranslation("agentFirst");
	const evaluation = useGetEvaluationQuery(evaluationId || skipToken);
	const rows = droppedFaults(evaluation.data);
	if (rows.length === 0) return null;
	return (
		<div className="border-border/80 flex flex-col gap-2 rounded-lg border px-4 py-3 text-sm" data-testid="report-dropped-faults">
			<p className="font-medium">{t("ft.droppedTitle")}</p>
			<ul className="text-muted-foreground flex flex-col gap-1 text-xs">
				{rows.map((r) => (
					<li key={`${r.benchmark}-${r.fault}`} data-testid={`dropped-fault-${r.benchmark}-${r.fault}`}>
						{t("ft.droppedRow", { fault: faultName(r.fault), benchmark: r.benchmark, reason: r.reason })}
					</li>
				))}
			</ul>
		</div>
	);
}