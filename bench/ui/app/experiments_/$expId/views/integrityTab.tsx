import { CompliancePackLinks } from "@/components/integrity/compliancePack";
import { AwarenessSection, ExperimentMonitorsSection } from "@/components/integrity/integritySections";
import "@/components/integrity/gw06I18n";
import { VarianceCard } from "@/components/integrity/varianceCard";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { GradeSourceTag } from "@/components/verdict/gradeSourceTag";
import { armOrder } from "@/components/verdict/verdict.utils";
import { useGetGridQuery, useGetVerdictQuery } from "@/lib/store/apis/benchApi";
import type { Grid } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { ShieldAlert } from "lucide-react";
import "@/components/verdict/uf4aI18n";
import { useTranslation } from "react-i18next";
import { gradeQuery, useGradeChoice } from "./gradeChoice";
import { Section } from "./verdictTab";
import { SlowSkeleton } from "./slowSkeleton";

/** The Integrity tab (eval integrity area, S2): the noise floor, eval awareness, monitor certifications and the compliance pack. */
export function IntegrityTab({ expId }: { expId: string }) {
	const { t } = useTranslation("bench");
	// The same request as the Verdict tab (the grade source the page shows), so it is read from the cache.
	const { grade } = useGradeChoice(expId);
	const verdict = useGetVerdictQuery(gradeQuery(expId, grade));
	const arms = verdict.data ? armOrder(verdict.data) : [];
	return (
		<div className="flex flex-col gap-6" data-testid="integrity-screen">
			<FlaggedRunsCard expId={expId} />
			{verdict.isLoading ? (
				<SlowSkeleton rows={3} testId="integrity-variance-loading" onRetry={() => void verdict.refetch()} />
			) : (
				verdict.data && (
					<div className="flex flex-col gap-2">
						<div className="flex justify-end">
							<GradeSourceTag
								source={verdict.data.grade_source}
								judged={Boolean(verdict.data.judge_calibration)}
								testId="integrity-source"
							/>
						</div>
						{verdict.data.verdict.decision === "score" ? <OneSetupNoiseNote /> : <VarianceCard verdict={verdict.data.verdict} />}
					</div>
				)
			)}
			<Section title={t("integrity.awareness.title")} description={t("integrity.awareness.description")} testId="verdict-awareness">
				<AwarenessSection expId={expId} arms={arms} />
			</Section>
			<div className="grid grid-cols-1 items-start gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
				<Section title={t("integrity.monitors.title")} testId="verdict-monitors">
					<ExperimentMonitorsSection expId={expId} />
				</Section>
				<Section title={t("integrity.compliance.title")} description={t("integrity.compliance.description")} testId="verdict-compliance">
					<CompliancePackLinks expId={expId} />
				</Section>
			</div>
		</div>
	);
}

/**
 * In place of the noise floor card on a one-setup score run (N68): the noise floor is about the
 * difference between two setups, so there is nothing to measure it against.
 */
function OneSetupNoiseNote() {
	const { t } = useTranslation("gw06");
	return (
		<Card className="gap-2" data-testid="variance-one-setup">
			<CardHeader>
				<CardTitle role="heading" aria-level={2}>
					{t("integrity.noiseFloorTitle")}
				</CardTitle>
				<CardDescription>{t("integrity.noiseFloorOneSetup")}</CardDescription>
			</CardHeader>
			<CardContent />
		</Card>
	);
}
/** Runs thrown out for cheating (a run's outcome integrity_violation), grouped by task with the setups it hit (M4-15). */
export function flaggedRuns(grid: Grid | undefined): {
	count: number;
	tasks: { task: string; arms: string[] }[];
} {
	const byTask = new Map<string, Set<string>>();
	let count = 0;
	for (const row of grid?.rows ?? []) {
		for (const cell of row.cells) {
			if (cell.outcome !== "integrity_violation") continue;
			count++;
			const arms = byTask.get(row.task) ?? new Set<string>();
			arms.add(cell.arm);
			byTask.set(row.task, arms);
		}
	}
	return {
		count,
		tasks: [...byTask].map(([task, arms]) => ({
			task,
			arms: [...arms].sort(),
		})),
	};
}

/** The Checks tab's first card: how many runs were flagged for cheating, on which tasks and setups, linking the runs. */
function FlaggedRunsCard({ expId }: { expId: string }) {
	const { t } = useTranslation("uf4a");
	const grid = useGetGridQuery(expId);
	if (grid.isLoading || !grid.data) return null;
	const flagged = flaggedRuns(grid.data);
	const all = grid.data.arms.length;
	const where = flagged.tasks
		.map(({ task, arms }) =>
			arms.length === all && all > 1 ? t("checks.onAll", { task }) : t("checks.onSome", { task, setups: arms.join(", ") }),
		)
		.join("; ");
	return (
		<Card className="gap-2" data-testid="integrity-flagged">
			<CardHeader>
				<CardTitle role="heading" aria-level={2} className="flex items-center gap-2">
					<ShieldAlert className="text-muted-foreground size-4" aria-hidden="true" />
					{t("checks.flaggedTitle", { count: flagged.count })}
				</CardTitle>
				<CardDescription>
					{flagged.count === 0 ? (
						t("checks.noneFlagged")
					) : (
						<>
							{t("checks.flaggedWhere", { where })}{" "}
							<Link
								to="/experiments/$expId"
								params={{ expId }}
								search={{ tab: "runs", outcome: "integrity_violation" } as never}
								className="text-primary font-medium hover:underline"
								data-testid="integrity-flagged-link"
							>
								{t("checks.openRuns")}
							</Link>
						</>
					)}
				</CardDescription>
			</CardHeader>
		</Card>
	);
}