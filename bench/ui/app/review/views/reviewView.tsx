import { filterQueue, isReviewReason } from "@/components/review/review.utils";
import { ReviewQueueTable } from "@/components/review/reviewQueueTable";
import "@/components/review/hw07I18n";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/shell";
import { ExperimentSelect, defaultPickerExperiment, useExperimentPicker } from "@/components/triage/experimentSelect";
import { useEvaluationNames } from "@/components/experiments/useEvaluationNames";
import { useGetReviewQueueQuery, useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { REVIEW_REASONS, type ReviewQueueResponse } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, Filter, Inbox, MousePointerClick } from "lucide-react";
import { EmptyState } from "@/components/shell/emptyState";
import { cn } from "@/lib/utils";
import { parseAsString, useQueryState } from "nuqs";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { ReviewRunPanel } from "./reviewRunPanel";

/** The reason filter's value for "every reason". Not snake_case, so never a real reason. */
const ALL_REASONS = "~all";

/**
 * The human review screen: /review?exp=&reason=&run=. The queue lists the experiment's runs a
 * person should look at; picking one opens its evidence, its review timeline and the form. The
 * reason filter is applied on the client, so the counts in the picker stay the whole queue's.
 */
export default function ReviewView() {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u14");
	const [exp, setExp] = useQueryState("exp", parseAsString);
	const [reason, setReason] = useQueryState("reason", parseAsString);
	const [runId, setRunId] = useQueryState("run", parseAsString);
	const [all, setAll] = useQueryState("all", parseAsString);
	const list = useListExperimentsQuery();
	const experiments = useMemo(() => list.data?.experiments ?? [], [list.data]);
	// Checks, practice runs and debug runs are listed only with all=1, so the page opens on a real comparison.
	const picker = useExperimentPicker(experiments, all === "1", useEvaluationNames());
	const selectedExp = defaultPickerExperiment(picker.options, exp);

	return (
		<section className="flex w-full min-w-0 flex-col gap-8" data-testid="review-screen">
			<PageHeader title={t("review.title")} subtitle={<span data-testid="review-explainer">{tu("review.subtitle")}</span>} />
			<div className="flex flex-wrap items-end gap-3">
				<ExperimentSelect
					picker={picker}
					value={selectedExp}
					onChange={(id) => {
						void setExp(id);
						void setRunId(null);
					}}
					showHidden={all === "1"}
					onShowHidden={(on) => void setAll(on ? "1" : null)}
					disabled={list.isLoading || experiments.length === 0}
					testId="review-experiment-picker"
				/>
				{selectedExp && (
					<Button variant="outline" size="sm" asChild>
						{/* U2 owns /experiments/$expId; grade=reviewed asks its verdict for grade source reviewed. */}
						<Link
							to={"/experiments/$expId" as never}
							params={{ expId: selectedExp } as never}
							search={{ grade: "reviewed" } as never}
							data-testid="review-verdict-link"
						>
							{t("review.verdictLink")}
						</Link>
					</Button>
				)}
			</div>
			{list.isError && (
				<LoadError
					title={t("review.picker.loadError")}
					error={list.error}
					onRetry={() => void list.refetch()}
					testId="review-picker-error"
				/>
			)}
			{selectedExp ? (
				<ExperimentQueue
					key={selectedExp}
					expId={selectedExp}
					reason={reason}
					onReason={(next) => void setReason(next)}
					runId={runId}
					onRun={(next) => void setRunId(next)}
				/>
			) : list.isLoading ? (
				<BlockSkeleton rows={6} testId="review-loading" />
			) : (
				!list.isError && <EmptyNote testId="review-no-experiments">{t("review.noExperiments")}</EmptyNote>
			)}
		</section>
	);
}

interface ExperimentQueueProps {
	expId: string;
	reason: string | null;
	onReason: (reason: string | null) => void;
	runId: string | null;
	onRun: (runId: string | null) => void;
}

function ExperimentQueue({ expId, reason, onReason, runId, onRun }: ExperimentQueueProps) {
	const { t } = useTranslation("bench");
	const queue = useGetReviewQueueQuery({ expId });

	if (queue.isLoading) return <BlockSkeleton rows={6} testId="review-queue-loading" />;
	if (queue.error || !queue.data) {
		return (
			<LoadError title={t("review.queue.loadError")} error={queue.error} onRetry={() => void queue.refetch()} testId="review-queue-error" />
		);
	}
	return <QueueBody data={queue.data} reason={reason} onReason={onReason} runId={runId} onRun={onRun} />;
}

/** The loaded queue: filter, table, problems and the open run. Exported for tests. */
export function QueueBody({
	data,
	reason,
	onReason,
	runId,
	onRun,
}: {
	data: ReviewQueueResponse;
	reason: string | null;
	onReason: (reason: string | null) => void;
	runId: string | null;
	onRun: (runId: string | null) => void;
}) {
	const { t } = useTranslation("bench");
	const items = useMemo(() => filterQueue(data.items ?? [], reason), [data.items, reason]);
	const selected = (data.items ?? []).find((item) => item.run_id === runId);
	const reasons = data.reasons && data.reasons.length > 0 ? data.reasons : [...REVIEW_REASONS];
	const problems = data.problems ?? [];

	return (
		<div className="grid min-w-0 grid-cols-1 items-start gap-6 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
			<div className="bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-4 sm:p-5" data-testid="review-queue">
				<div className="flex flex-wrap items-center justify-between gap-3">
					<div className="flex items-center gap-2">
						<Inbox className="text-muted-foreground size-4" aria-hidden="true" />
						<h2 className="text-sm font-semibold">{t("review.queue.title")}</h2>
						<span className="bg-muted text-muted-foreground rounded-full px-2 py-0.5 text-xs tabular-nums">{items.length}</span>
					</div>
				</div>
				<ReasonFilter reasons={reasons} value={reason} onChange={onReason} counts={countReasons(data)} total={(data.items ?? []).length} />
				<div className="flex flex-col gap-4">
					{items.length === 0 ? (
						<EmptyNote testId={reason ? "review-queue-empty-filtered" : "review-queue-empty"}>
							{reason ? t("review.queue.emptyFiltered") : t("review.queue.empty")}
						</EmptyNote>
					) : (
						<div className="min-w-0 overflow-x-auto rounded-lg border">
							<ReviewQueueTable items={items} selectedRunId={runId} onSelect={(item) => onRun(item.run_id)} />
						</div>
					)}
					{problems.length > 0 && (
						<div
							className="border-destructive/40 bg-destructive/5 flex flex-col gap-1 rounded-sm border px-3 py-2 text-sm"
							data-testid="review-queue-problems"
						>
							<p className="flex items-center gap-2 font-medium">
								<AlertTriangle className="text-chart-error-ink size-4" aria-hidden="true" />
								{t("review.queue.problems")}
							</p>
							<p className="text-muted-foreground text-xs">{t("review.queue.problemsHint")}</p>
							<ul className="text-muted-foreground list-disc pl-5 break-words">
								{problems.map((problem) => (
									<li key={problem}>{problem}</li>
								))}
							</ul>
						</div>
					)}
				</div>
			</div>
			{selected ? (
				<ReviewRunPanel item={selected} />
			) : (
				<EmptyState icon={MousePointerClick} message={t("review.panel.select")} testId="review-select-run" />
			)}
		</div>
	);
}

/** Queue items per reason, for the filter. */
function countReasons(data: ReviewQueueResponse): Record<string, number> {
	const counts: Record<string, number> = {};
	for (const item of data.items ?? []) for (const r of item.reasons) counts[r] = (counts[r] ?? 0) + 1;
	return counts;
}

function ReasonFilter({
	reasons,
	value,
	onChange,
	counts,
	total,
}: {
	total: number;
	reasons: readonly string[];
	value: string | null;
	onChange: (reason: string | null) => void;
	counts: Record<string, number>;
}) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u14");
	const { t: tHw07 } = useTranslation("hw07");
	const chip = (key: string, label: string, count: number | undefined, active: boolean, onClick: () => void, testId?: string) => (
		<button
			key={key}
			type="button"
			role="radio"
			aria-checked={active}
			onClick={onClick}
			className={cn(
				"inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium transition",
				active
					? "border-primary bg-primary text-primary-foreground"
					: "bg-background text-muted-foreground hover:text-foreground hover:bg-muted",
			)}
			data-testid={testId}
		>
			{label}
			{count !== undefined && (
				<span className={cn("rounded-full px-1.5 tabular-nums", active ? "bg-primary-foreground/20" : "bg-muted")}>{count}</span>
			)}
		</button>
	);
	return (
		<div
			role="radiogroup"
			aria-label={t("review.filter.label")}
			className="flex flex-wrap items-center gap-1.5"
			data-testid="review-reason-filter"
		>
			<Filter className="text-muted-foreground mr-1 size-3.5" aria-hidden="true" />
			{chip(ALL_REASONS, tu("review.all"), total, value === null, () => onChange(null))}
			{reasons.map((r) =>
				chip(
					r,
					r === "invalidated"
						? tHw07("review.reason.invalidated")
						: isReviewReason(r)
							? t(`review.reason.${r as Exclude<typeof r, "invalidated">}`)
							: r,
					counts[r] ?? 0,
					value === r,
					() => onChange(r),
					`review-reason-option-${r}`,
				),
			)}
		</div>
	);
}