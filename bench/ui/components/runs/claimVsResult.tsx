import type { Grade, Run } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { AlertTriangle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { OutcomeBadge } from "./outcomeBadge";
import { outcomeTone } from "./runs.utils";

export interface ClaimRow {
	arm: string;
	run: Run;
	grade: Grade | undefined;
	gradeState: "loading" | "error" | "ready";
}

/** Per arm: what the agent said (inert text) beside what the hidden verifier found. */
export function ClaimVsResult({ rows }: { rows: readonly ClaimRow[] }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex flex-col divide-y rounded-sm border" data-testid="claim-vs-result">
			<div className="text-muted-foreground hidden grid-cols-[5rem_1fr_1fr] gap-4 px-4 py-2 text-xs font-medium md:grid">
				<span />
				<span>{t("runs.claim.agentSaid")}</span>
				<span>{t("runs.claim.hiddenResult")}</span>
			</div>
			{rows.map((row) => (
				<ClaimRowView key={row.arm} row={row} />
			))}
		</div>
	);
}

function ClaimRowView({ row }: { row: ClaimRow }) {
	const { t } = useTranslation("bench");
	const { arm, run, grade, gradeState } = row;
	const claim = run.claim;
	const contradiction = Boolean(claim?.made) && outcomeTone(run.outcome) === "zero";

	return (
		<div className="grid gap-3 px-4 py-3 md:grid-cols-[8rem_1fr_1fr] md:gap-4" data-testid={`claim-row-${arm}`}>
			<div className="flex flex-col gap-1">
				<span className="text-sm font-semibold">{t("runs.arm", { arm })}</span>
				{contradiction && (
					<span
						className="text-chart-error-ink inline-flex items-start gap-1 text-[11px] leading-tight font-medium"
						data-testid={`claim-contradiction-${arm}`}
					>
						<AlertTriangle className="mt-px size-3 shrink-0" aria-hidden="true" />
						{t("runs.claim.contradiction")}
					</span>
				)}
			</div>

			<div className="flex min-w-0 flex-col gap-1.5">
				<span className="text-muted-foreground text-xs font-medium md:hidden">{t("runs.claim.agentSaid")}</span>
				{claim?.text ? (
					<blockquote
						className={cn(
							"bg-muted/40 border-l-2 px-3 py-2 text-sm break-words whitespace-pre-wrap",
							contradiction ? "border-l-chart-error" : "border-l-border",
						)}
						data-testid={`claim-text-${arm}`}
					>
						{claim.text}
					</blockquote>
				) : (
					<p className="text-muted-foreground text-sm italic">{t("runs.claim.noMessage")}</p>
				)}
				<p className="text-muted-foreground text-xs">
					{claim?.made
						? claim.matched
							? t("runs.claim.claimMade", { matched: claim.matched })
							: t("runs.claim.claimMadePlain")
						: t("runs.claim.noClaimMade")}
					{claim && <span className="ml-2 font-mono text-[10px]">{t("runs.claim.source", { source: claim.source })}</span>}
				</p>
			</div>

			<div className="flex min-w-0 flex-col gap-1.5" data-testid={`hidden-result-${arm}`}>
				<span className="text-muted-foreground text-xs font-medium md:hidden">{t("runs.claim.hiddenResult")}</span>
				<OutcomeBadge outcome={run.outcome} testId={`hidden-outcome-${arm}`} />
				<GradeDetail grade={grade} state={gradeState} />
			</div>
		</div>
	);
}

function GradeDetail({ grade, state }: { grade: Grade | undefined; state: ClaimRow["gradeState"] }) {
	const { t } = useTranslation("bench");
	if (state === "loading") return <span className="bg-accent h-4 w-40 animate-pulse rounded-sm" />;
	if (state === "error") return <p className="text-muted-foreground text-xs">{t("runs.claim.gradeError")}</p>;
	if (!grade) return <p className="text-muted-foreground text-xs">{t("runs.claim.noGrade")}</p>;
	return (
		<div className="flex flex-col gap-1.5 text-xs">
			<p className="text-muted-foreground">
				{t("runs.claim.tests", { ran: grade.tests_ran, failures: grade.failures, errors: grade.errors })}
			</p>
			<TestList title={t("runs.claim.failedTests")} tests={grade.failed_tests} />
			<TestList title={t("runs.claim.missingRequired")} tests={grade.missing_required} />
		</div>
	);
}

function TestList({ title, tests }: { title: string; tests: string[] | undefined }) {
	if (!tests || tests.length === 0) return null;
	return (
		<div className="flex flex-col gap-0.5">
			<span className="text-muted-foreground font-medium">{title}</span>
			<ul className="flex flex-col gap-0.5">
				{tests.map((test) => (
					<li key={test} className="text-chart-error-ink font-mono text-[11px] break-all">
						{test}
					</li>
				))}
			</ul>
		</div>
	);
}