import { DiffText } from "@/components/runs/diffText";
import { OutcomeBadge } from "@/components/runs/outcomeBadge";
import { Badge } from "@/components/ui/badge";
import type { RunDetail, RunDiff } from "@/lib/types/bench";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { shortReviewHash, tallyVotes } from "./review.utils";

export interface RunEvidenceSummaryProps {
	detail: RunDetail;
	/** The workspace diff, for a run that wrote one; absent while loading or for an answer task. */
	diff?: RunDiff;
}

/**
 * What a reviewer needs beside the form, all read from the sealed run: the grade and why, the
 * judge's votes when a judge decided, the gates that fired, the agent's claim, and the answer or
 * the diff. Every agent-written text is rendered inert.
 */
export function RunEvidenceSummary({ detail, diff }: RunEvidenceSummaryProps) {
	const { t } = useTranslation("bench");
	const { grade, run } = detail;
	const fired = (detail.gates ?? []).filter((gate) => gate.fired);
	const judge = grade?.judged ? grade.judge : undefined;
	const tally = tallyVotes(judge?.votes);

	return (
		<div className="flex flex-col gap-4 text-sm" data-testid="review-evidence">
			<dl className="grid grid-cols-[max-content_1fr] items-start gap-x-4 gap-y-2">
				<Term>{t("review.timeline.sealed")}</Term>
				<dd>
					<OutcomeBadge outcome={run.outcome} testId="review-evidence-outcome" />
				</dd>
				<Term>{t("review.panel.grade")}</Term>
				<dd className="flex flex-col gap-1" data-testid="review-evidence-grade">
					{grade ? (
						<span>
							{grade.invalid_evaluator ? t("review.panel.undecided") : grade.passed ? t("review.panel.passed") : t("review.panel.failed")}
							{grade.mode && <span className="text-muted-foreground ml-2 font-mono text-xs">{grade.mode}</span>}
						</span>
					) : (
						<span className="text-muted-foreground">-</span>
					)}
					{grade?.reason && (
						<span className="text-muted-foreground text-xs break-words">
							{t("review.panel.gradeReason")}: {grade.reason}
						</span>
					)}
					{grade?.failed_tests && grade.failed_tests.length > 0 && (
						<span className="text-muted-foreground text-xs break-all">
							{t("review.panel.failedTests")}: <span className="font-mono">{grade.failed_tests.join(", ")}</span>
						</span>
					)}
				</dd>
				<Term>{t("review.panel.judge")}</Term>
				<dd className="flex flex-col gap-1" data-testid="review-evidence-judge">
					{judge ? (
						<>
							<span className="font-mono text-xs">{judge.model}</span>
							<span className="flex flex-wrap items-center gap-1">
								<span className="text-muted-foreground text-xs">{t("review.panel.judgeVotes")}</span>
								{judge.votes.map((vote, i) => (
									<Badge
										key={`${vote}-${i}`}
										variant={vote === "correct" ? "success" : vote === "incorrect" ? "destructive" : "warning"}
										data-testid={`review-evidence-vote-${i}`}
									>
										{vote}
									</Badge>
								))}
								<span className="text-muted-foreground text-xs" data-testid="review-evidence-vote-tally">
									{tally.correct}/{tally.incorrect}/{tally.unparsable}
								</span>
							</span>
						</>
					) : (
						<span className="text-muted-foreground">{t("review.panel.notJudged")}</span>
					)}
				</dd>
				<Term>{t("review.panel.gates")}</Term>
				<dd className="flex flex-col gap-1" data-testid="review-evidence-gates">
					{fired.length === 0 ? (
						<span className="text-muted-foreground">{t("review.panel.noGates")}</span>
					) : (
						fired.map((gate) => (
							<span key={gate.gate} className="flex flex-wrap items-center gap-2">
								<Badge variant={gate.effect === "veto" ? "destructive" : "warning"}>{gate.gate}</Badge>
								{gate.detail && <span className="text-muted-foreground text-xs break-words">{gate.detail}</span>}
							</span>
						))
					)}
				</dd>
				<Term>{t("review.panel.claim")}</Term>
				<dd data-testid="review-evidence-claim">
					{run.claim ? (
						<span className="flex flex-col gap-0.5">
							<span>{run.claim.made ? t("review.panel.claimMade") : t("review.panel.claimNone")}</span>
							{run.claim.text && <span className="text-muted-foreground text-xs break-words whitespace-pre-wrap">{run.claim.text}</span>}
						</span>
					) : (
						<span className="text-muted-foreground">-</span>
					)}
				</dd>
				{detail.seal_hash && (
					<>
						<Term>{t("review.panel.sealHash")}</Term>
						<dd className="font-mono text-xs">{shortReviewHash(detail.seal_hash)}</dd>
					</>
				)}
			</dl>
			{detail.answer ? (
				<Block title={t("review.panel.answer")}>
					<pre
						className="bg-muted max-h-64 overflow-auto rounded-sm p-3 text-xs break-words whitespace-pre-wrap"
						data-testid="review-evidence-answer"
					>
						{detail.answer.text}
					</pre>
				</Block>
			) : diff ? (
				<Block title={t("review.panel.diff")}>
					<DiffText runDiff={diff} arm={detail.slot.arm} />
				</Block>
			) : null}
		</div>
	);
}

function Term({ children }: { children: ReactNode }) {
	return <dt className="text-muted-foreground text-xs leading-6">{children}</dt>;
}

function Block({ title, children }: { title: string; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-1.5">
			<p className="text-muted-foreground text-xs">{title}</p>
			{children}
		</div>
	);
}