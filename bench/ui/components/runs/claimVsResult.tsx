import "@/components/runs/ufc4I18n";
import type { Grade, Run } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { AlertTriangle } from "lucide-react";
import { useTranslation } from "react-i18next";
import "@/components/reportcard/hw08I18n";
import { Markdown } from "@/components/docs/markdown";
import { gradeModeWhat } from "./runFailure.utils";
import "./uf5aI18n";
import { OutcomeBadge } from "./outcomeBadge";
import { outcomeTone } from "./runs.utils";

export interface ClaimRow {
	arm: string;
	run: Run;
	grade: Grade | undefined;
	gradeState: "loading" | "error" | "ready";
}

/** One column grid for the header and every row, so header labels sit over their columns. */
const CLAIM_GRID = "md:grid-cols-[8rem_1fr_1fr]";

/** The answer pretty printed when it is JSON (an object or array), else null. */
export function jsonAnswer(text: string): string | null {
	const trimmed = text.trim();
	if (!/^[[{]/.test(trimmed)) return null;
	try {
		return JSON.stringify(JSON.parse(trimmed), null, 2);
	} catch {
		return null;
	}
}

/** Per arm: what the agent said (inert text) beside what the hidden verifier found. */
export function ClaimVsResult({ rows }: { rows: readonly ClaimRow[] }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex flex-col divide-y rounded-sm border" data-testid="claim-vs-result">
			<div className={cn("text-muted-foreground hidden gap-4 px-4 py-2 text-xs font-medium md:grid", CLAIM_GRID)}>
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
		<div className={cn("grid gap-3 px-4 py-3 md:gap-4", CLAIM_GRID)} data-testid={`claim-row-${arm}`}>
			<div className="flex flex-col gap-1">
				<span className="text-sm font-semibold">{t("runs.arm", { arm })}</span>
				{contradiction && (
					<span
						className="text-chart-error-ink inline-flex items-start gap-1 text-xs leading-tight font-medium"
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
					<ClaimText text={claim.text} arm={arm} contradiction={contradiction} />
				) : (
					<p className="text-muted-foreground text-sm italic">{t("runs.claim.noMessage")}</p>
				)}
				<p className="text-muted-foreground text-xs">
					{claim?.made
						? claim.matched
							? t("runs.claim.claimMade", { matched: claim.matched })
							: t("runs.claim.claimMadePlain")
						: t("runs.claim.noClaimMade")}
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
	const { t: t08 } = useTranslation("hw08");
	const { t: tu } = useTranslation("uf5a");
	const { t: tc } = useTranslation("ufc4");
	if (state === "loading") return <span className="bg-accent h-4 w-40 animate-pulse rounded-sm" />;
	if (state === "error") return <p className="text-muted-foreground text-xs">{t("runs.claim.gradeError")}</p>;
	if (!grade) return <p className="text-muted-foreground text-xs">{t("runs.claim.noGrade")}</p>;
	return (
		<div className="flex flex-col gap-1.5 text-xs">
			{grade.invalid_evaluator && grade.reason ? (
				<p className="text-chart-error-ink break-words" data-testid="grade-invalid-reason">
					{t08("graderCouldNot", { reason: grade.reason })}
				</p>
			) : grade.reason ? (
				<p className="text-muted-foreground break-words" data-testid="grade-reason">
					{tu("claim.reason", { reason: grade.reason })}
				</p>
			) : null}
			{grade.mode && (
				<p className="text-muted-foreground" data-testid="grade-mode-compare">
					{tu("claim.modeCompare", {
						what: tu(`reason.what.${gradeModeWhat(grade.mode)}`),
						result: grade.passed ? tu("claim.matched") : tu("claim.notMatched"),
					})}
				</p>
			)}
			{/* Solved with no check and no reason (a practice run): say what passed it (M4-55). */}
			{grade.passed && grade.tests_ran === 0 && !grade.reason && !grade.mode && !grade.judged && (
				<p className="text-muted-foreground" data-testid="grade-no-checks">
					{tc("claim.passedNoChecks")}
				</p>
			)}
			{grade.tests_ran > 0 && (
				<p className="text-muted-foreground" data-testid="grade-tests">
					{t("runs.claim.tests", { ran: grade.tests_ran, failures: grade.failures, errors: grade.errors })}
				</p>
			)}
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
					<li key={test} className="text-danger-ink font-mono text-xs break-all">
						{test}
					</li>
				))}
			</ul>
		</div>
	);
}
/** The agent's final message: pretty printed JSON in mono, or rendered markdown. No inner border (no nested card). */
function ClaimText({ text, arm, contradiction }: { text: string; arm: string; contradiction: boolean }) {
	const json = jsonAnswer(text);
	return (
		<blockquote
			className={cn("bg-muted/40 rounded-sm border-0 px-3 py-2 text-sm", contradiction && "ring-chart-error/40 ring-1")}
			data-testid={`claim-text-${arm}`}
		>
			{json ? (
				<pre className="font-mono text-xs whitespace-pre-wrap [overflow-wrap:anywhere]">{json}</pre>
			) : (
				<Markdown source={text} className="break-words" testId={`claim-markdown-${arm}`} />
			)}
		</blockquote>
	);
}
