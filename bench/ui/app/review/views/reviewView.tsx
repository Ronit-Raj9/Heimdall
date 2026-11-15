import { ModeBadge } from "@/components/modeBadge";
import { filterQueue, isReviewReason } from "@/components/review/review.utils";
import { ReviewQueueTable } from "@/components/review/reviewQueueTable";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { sortExperimentsNewestFirst } from "@/components/verdict/verdict.utils";
import { useGetReviewQueueQuery, useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { REVIEW_REASONS, type ReviewQueueResponse } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { AlertTriangle } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useId, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { ExperimentPicker } from "../../verdict/views/experimentPicker";
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
	const [exp, setExp] = useQueryState("exp", parseAsString);
	const [reason, setReason] = useQueryState("reason", parseAsString);
	const [runId, setRunId] = useQueryState("run", parseAsString);
	const list = useListExperimentsQuery();
	const experiments = useMemo(() => sortExperimentsNewestFirst(list.data?.experiments ?? []), [list.data]);
	const selectedExp = exp ?? experiments[0]?.exp_id;

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="review-screen">
			<header className="flex flex-col gap-1">
				<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("review.title")}</h1>
				<p className="text-muted-foreground max-w-3xl text-sm" data-testid="review-explainer">
					{t("review.subtitle")}
				</p>
			</header>
			<div className="flex flex-wrap items-end gap-4">
				<ExperimentPicker
					experiments={experiments}
					value={selectedExp}
					onChange={(id) => {
						void setExp(id);
						void setRunId(null);
					}}
					disabled={list.isLoading || experiments.length === 0}
				/>
				{selectedExp && (
					<Button variant="outline" size="sm" asChild>
						<Link to="/verdict" search={{ exp: selectedExp, grade: "reviewed" }} data-testid="review-verdict-link">
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
		<div className="grid min-w-0 grid-cols-1 items-start gap-6 2xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
			<Section
				title={t("review.queue.title")}
				description={t("review.queue.description")}
				testId="review-queue"
				actions={
					<div className="flex items-center gap-3">
						<ModeBadge mode={data.mode} />
						<ReasonFilter reasons={reasons} value={reason} onChange={onReason} counts={countReasons(data)} />
					</div>
				}
			>
				<div className="flex flex-col gap-4">
					{items.length === 0 ? (
						<EmptyNote testId={reason ? "review-queue-empty-filtered" : "review-queue-empty"}>
							{reason ? t("review.queue.emptyFiltered") : t("review.queue.empty")}
						</EmptyNote>
					) : (
						<div className="min-w-0 overflow-x-auto rounded-sm border">
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
			</Section>
			{selected ? <ReviewRunPanel item={selected} /> : <EmptyNote testId="review-select-run">{t("review.panel.select")}</EmptyNote>}
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
}: {
	reasons: readonly string[];
	value: string | null;
	onChange: (reason: string | null) => void;
	counts: Record<string, number>;
}) {
	const { t } = useTranslation("bench");
	const id = useId();
	return (
		<div className="flex items-center gap-2">
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{t("review.filter.label")}
			</label>
			<Select value={value ?? ALL_REASONS} onValueChange={(next) => onChange(next === ALL_REASONS ? null : next)}>
				<SelectTrigger id={id} className="min-w-44 text-xs" data-testid="review-reason-filter">
					<SelectValue />
				</SelectTrigger>
				<SelectContent>
					<SelectItem value={ALL_REASONS}>{t("review.filter.all")}</SelectItem>
					{reasons.map((r) => (
						<SelectItem key={r} value={r} data-testid={`review-reason-option-${r}`}>
							{isReviewReason(r) ? t(`review.reason.${r}`) : r} ({counts[r] ?? 0})
						</SelectItem>
					))}
				</SelectContent>
			</Select>
		</div>
	);
}