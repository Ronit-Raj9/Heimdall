import { type DestructiveConfirmOptions, useConfirm } from "@/components/platform/confirmDialog";
import { NativeSelect } from "@/components/ui/select";
import { pageWidthClass } from "@/components/shell/pageWidth";
import { notify } from "@/lib/notify";
import "@/components/suites/uf6bI18n";
import { queueSourceLine } from "./queueSource";
import { Field, LabellerPicker, perItemProblem } from "@/app/annotation/views/annotationView";
import { humanExperimentName, isCheckPracticeOrDebug, suiteName } from "@/components/experiments/experiments.utils";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { PageHeader, RouteTabs, type RouteTab } from "@/components/shell";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { StatCard, StatGrid } from "@/components/ui/statCard";
import { StatusPill, type StatusTone } from "@/components/ui/statusPill";
import { canUse, useSession } from "@/lib/session/sessionContext";
import {
	useAddAnnotationItemsMutation,
	useAssignAnnotationItemsMutation,
	useDeleteAnnotationQueueMutation,
	useGetAnnotationAgreementQuery,
	useGetAnnotationItemsQuery,
	useGetAnnotationQueueQuery,
	useLabelAnnotationItemMutation,
	useNextAnnotationItemMutation,
	useResolveAnnotationItemMutation,
	useUpdateAnnotationQueueMutation,
} from "@/lib/store/apis/annotationApi";
import { getBenchErrorMessage, useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import type {
	AnnotationAgreement,
	AnnotationItem,
	AnnotationLabel,
	AnnotationLabelResult,
	AnnotationQueue,
	AnnotationState,
} from "@/lib/types/annotation";
import type { ExperimentStatus } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link, useNavigate } from "@tanstack/react-router";
import { Check, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

type T = ReturnType<typeof useTranslation<"annotation">>["t"];

const pct = (v: number | null | undefined) => (v === null || v === undefined ? "-" : `${Math.round(v * 100)}%`);
const kappa = (v: number | null | undefined) => (v === null || v === undefined ? "-" : v.toFixed(2));

/** One queue: /annotation/$queueId?tab=. Label (keyboard first), Items, Agreement, Settings. */
export default function QueueView({ queueId }: { queueId: string }) {
	const { t } = useTranslation("annotation");
	const queue = useGetAnnotationQueueQuery(queueId);
	if (queue.isLoading) return <BlockSkeleton rows={6} testId="annotation-queue-loading" />;
	if (queue.error || !queue.data)
		return (
			<LoadError title={t("queue.notFound")} error={queue.error} onRetry={() => void queue.refetch()} testId="annotation-queue-error" />
		);
	return <QueueScreen queue={queue.data} />;
}

export function QueueScreen({ queue }: { queue: AnnotationQueue }) {
	const { t } = useTranslation("annotation");
	const { t: tu } = useTranslation("uf6b");
	const source = queueSourceLine(queue);
	const sourceText = tu(source.key, { benchmark: source.benchmark });
	const tabs: RouteTab[] = [
		{ id: "label", label: t("queue.tabs.label"), hidden: !queue.can_label },
		{ id: "items", label: t("queue.tabs.items"), badge: queue.counts.total },
		{ id: "agreement", label: t("queue.tabs.agreement"), hidden: queue.reviewers_per_item < 2 && !queue.benchmark },
		{ id: "settings", label: t("queue.tabs.settings") },
	];
	return (
		<section className={cn(pageWidthClass("dash"), "flex flex-col gap-8")} data-page-width="dash" data-testid="annotation-queue-screen">
			<PageHeader
				title={queue.name}
				subtitle={[sourceText, queue.description].filter(Boolean).join(" ")}
				breadcrumbs={[{ label: t("queue.back"), to: "/annotation" }, { label: queue.name }]}
			/>
			{/* Round 3: five numbers in one row on a wide screen, not four and one alone. */}
			<StatGrid className={cn("grid-cols-2", queue.can_label && "lg:grid-cols-5")}>
				<StatCard label={t("queue.stats.total")} value={queue.counts.total} testId="annotation-stat-total" />
				<StatCard label={t("queue.stats.done")} value={queue.counts.done} testId="annotation-stat-done" />
				<StatCard label={t("queue.stats.disputed")} value={queue.counts.disputed} testId="annotation-stat-disputed" />
				<StatCard label={t("queue.stats.calibrated")} value={queue.counts.calibrated} testId="annotation-stat-calibrated" />
				{queue.can_label && <StatCard label={t("queue.stats.mine")} value={queue.mine} testId="annotation-stat-mine" />}
			</StatGrid>
			<RouteTabs tabs={tabs} defaultTab={queue.can_label ? "label" : "items"} label={queue.name} testId="annotation-queue-tabs">
				{(active) =>
					active === "label" ? (
						<Labeller queue={queue} />
					) : active === "agreement" ? (
						<AgreementTab queueId={queue.id} />
					) : active === "settings" ? (
						<SettingsTab queue={queue} />
					) : (
						<ItemsTab queue={queue} />
					)
				}
			</RouteTabs>
		</section>
	);
}

// Label

/** The two choices of a queue's kind: is the answer right, or was the judge right. */
export function choiceLabels(queue: Pick<AnnotationQueue, "kind">, t: T): Record<AnnotationLabel, string> {
	return queue.kind === "judge"
		? { correct: t("label.judgeRight"), incorrect: t("label.judgeWrong") }
		: { correct: t("label.correct"), incorrect: t("label.incorrect") };
}

/** Which key does what in the labeller; null when the key is not one of ours. */
export function keyAction(key: string): AnnotationLabel | "note" | "next" | null {
	switch (key.toLowerCase()) {
		case "1":
		case "c":
			return "correct";
		case "2":
		case "x":
			return "incorrect";
		case "n":
			return "note";
		case "j":
			return "next";
		default:
			return null;
	}
}

/** What Enter in the note does: save with the last choice, or show the hint when no choice was made yet (wave 4, p12-4). */
export function enterAction(lastChoice: AnnotationLabel | null): "save" | "hint" {
	return lastChoice ? "save" : "hint";
}

/** True when Next item skipped an item and got the same one back or none: nothing else waits (wave 4, p12-3). */
export function skipNote(skipped: string | null, got: string | null): boolean {
	return skipped !== null && (got === null || got === skipped);
}

/** Decide is offered only on a row with at least one label that is not resolved yet (wave 4, p12-7). */
export function decideShown(it: Pick<AnnotationItem, "label_count" | "resolution">): boolean {
	return it.label_count > 0 && !it.resolution;
}

/**
 * The labeller: asks the server for the next item reserved for the caller, shows the input and
 * answer, and takes a label from the keyboard (1 correct, 2 incorrect, N note, J next). After a
 * label the next item loads by itself.
 */
export function Labeller({ queue }: { queue: AnnotationQueue }) {
	const { t } = useTranslation("annotation");
	const { t: w } = useTranslation("w4m7");
	const session = useSession();
	const writable = canUse(session, "member");
	const [next, nextState] = useNextAnnotationItemMutation();
	const [label, labelState] = useLabelAnnotationItemMutation();
	const [item, setItem] = useState<AnnotationItem | null>(null);
	const [finished, setFinished] = useState(false);
	const [note, setNote] = useState("");
	const [last, setLast] = useState<AnnotationLabelResult | null>(null);
	const [lastChoice, setLastChoice] = useState<AnnotationLabel | null>(null);
	const [enterHint, setEnterHint] = useState(false);
	const [noOther, setNoOther] = useState(false);
	const noteRef = useRef<HTMLInputElement>(null);

	const load = useCallback(() => {
		void next(queue.id)
			.unwrap()
			.then((r) => {
				setItem(r.item);
				setFinished(!r.item);
				setNote("");
				setNoOther(false);
			})
			.catch(() => {});
	}, [next, queue.id]);

	// Next item (and J): pass on the item on screen; when nothing else waits, it stays and a note says so (wave 4, p12-3).
	const skip = useCallback(() => {
		const current = item?.id ?? null;
		if (!current) return load();
		void next({ id: queue.id, skip: [current] })
			.unwrap()
			.then((r) => {
				const got = r.item?.id ?? null;
				setNoOther(skipNote(current, got));
				if (got && got !== current) {
					setItem(r.item);
					setNote("");
				}
			})
			.catch(() => {});
	}, [item, load, next, queue.id]);

	const submit = useCallback(
		(choice: AnnotationLabel) => {
			if (!item || labelState.isLoading) return;
			setLastChoice(choice);
			setEnterHint(false);
			void label({ id: queue.id, item: item.id, label: choice, note })
				.unwrap()
				.then((r) => {
					setLast(r);
					load();
				})
				.catch(() => {});
		},
		[item, label, labelState.isLoading, load, note, queue.id],
	);

	useEffect(() => {
		if (!item) return;
		const onKey = (e: KeyboardEvent) => {
			if (e.metaKey || e.ctrlKey || e.altKey) return;
			const target = e.target as HTMLElement | null;
			if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.tagName === "SELECT")) return;
			const action = keyAction(e.key);
			if (!action) return;
			e.preventDefault();
			if (action === "note") noteRef.current?.focus();
			else if (action === "next") skip();
			else submit(action);
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [item, skip, submit]);

	if (!writable || !queue.can_label) return <EmptyNote testId="annotation-label-cannot">{t("label.cannotLabel")}</EmptyNote>;
	if (queue.counts.total === 0)
		return (
			<div className="flex min-w-0 flex-col items-center gap-3">
				<EmptyNote testId="annotation-label-no-items" className="w-full">
					{t("label.noItems")}
					{!queue.can_manage && ` ${t("label.askOwner")}`}
				</EmptyNote>
				{queue.can_manage && (
					<Link
						to="/annotation/$queueId"
						params={{ queueId: queue.id }}
						search={{ tab: "items" } as never}
						className={buttonVariants({ size: "sm" })}
						data-testid="annotation-label-add-runs"
					>
						{t("label.addRuns")}
					</Link>
				)}
			</div>
		);
	const choices = choiceLabels(queue, t);

	return (
		<div className="flex min-w-0 flex-col gap-4" data-testid="annotation-labeller">
			{queue.instructions && (
				<p className="text-muted-foreground max-w-3xl text-sm whitespace-pre-line" data-testid="annotation-instructions">
					{queue.instructions}
				</p>
			)}
			{last && <LastResult result={last} t={t} k={queue.reviewers_per_item} />}
			{nextState.error ? (
				<LoadError title={t("label.error")} error={nextState.error} onRetry={load} testId="annotation-next-error" />
			) : null}
			{!item ? (
				finished ? (
					<EmptyNote testId="annotation-label-done">{t("label.done")}</EmptyNote>
				) : (
					<div>
						<Button onClick={load} disabled={nextState.isLoading} data-testid="annotation-start">
							{t("label.start")}
						</Button>
					</div>
				)
			) : (
				<div className="bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-5" data-testid="annotation-item">
					<div className="text-muted-foreground flex flex-wrap items-center gap-3 text-xs">
						<span className="font-mono">{t("label.task", { task: item.task })}</span>
						{item.agent && <span className="font-mono">{item.agent_version ? `${item.agent} v${item.agent_version}` : item.agent}</span>}
						{item.hidden && item.hidden.length > 0 && (
							<span data-testid="annotation-hidden">{t("label.hiddenNote", { fields: item.hidden.join(", ") })}</span>
						)}
					</div>
					{item.question && (
						<div className="flex flex-col gap-1">
							<span className="text-muted-foreground text-xs font-medium">{t("label.question")}</span>
							<pre
								className="bg-muted/40 max-h-64 overflow-auto rounded-md p-3 text-sm whitespace-pre-wrap"
								data-testid="annotation-question"
							>
								{item.question}
							</pre>
						</div>
					)}
					<AnswerBlock item={item} />
					{queue.kind === "judge" && (
						<p className="text-muted-foreground text-xs" data-testid="annotation-judge">
							{item.judge_label ? t("label.judgeSaid", { label: item.judge_label }) : t("label.judgeHidden")}
							{item.judge_reason ? `: ${item.judge_reason}` : ""}
						</p>
					)}
					<Field label={t("label.note")}>
						<Input
							ref={noteRef}
							value={note}
							placeholder={t("label.notePlaceholder")}
							onChange={(e) => setNote(e.target.value)}
							onKeyDown={(e) => {
								if (e.key === "Escape") (e.target as HTMLInputElement).blur();
								if (e.key === "Enter" && !e.shiftKey) {
									e.preventDefault();
									if (enterAction(lastChoice) === "save" && lastChoice) submit(lastChoice);
									else setEnterHint(true);
								}
							}}
							data-testid="annotation-note"
						/>
					</Field>
					{enterHint && (
						<p className="text-muted-foreground text-xs" role="status" data-testid="annotation-enter-hint">
							{w("labelling.enterHint")}
						</p>
					)}
					<div className="flex flex-wrap items-center gap-2">
						<Button onClick={() => submit("correct")} disabled={labelState.isLoading} data-testid="annotation-correct">
							<Check className="size-4" aria-hidden="true" />
							{choices.correct}
							<kbd className="ml-1 rounded border px-1 text-[11px] opacity-70">1</kbd>
						</Button>
						<Button
							variant="outline"
							onClick={() => submit("incorrect")}
							disabled={labelState.isLoading}
							data-testid="annotation-incorrect"
						>
							<X className="size-4" aria-hidden="true" />
							{choices.incorrect}
							<kbd className="ml-1 rounded border px-1 text-[11px] opacity-70">2</kbd>
						</Button>
						<Button variant="ghost" onClick={skip} disabled={nextState.isLoading} data-testid="annotation-skip">
							{t("label.next")}
							<kbd className="ml-1 rounded border px-1 text-[11px] opacity-70">J</kbd>
						</Button>
					</div>
					{noOther && (
						<p className="text-muted-foreground text-xs" role="status" data-testid="annotation-no-other">
							{w("labelling.noOtherItem")}
						</p>
					)}
					<p className="text-muted-foreground text-xs" data-testid="annotation-keys">
						{t("label.keys")}
					</p>
					{labelState.error ? (
						<p role="alert" className="text-chart-error-ink text-xs" data-testid="annotation-label-error">
							{getBenchErrorMessage(labelState.error)}
						</p>
					) : null}
				</div>
			)}
		</div>
	);
}

/** The answer to label with a caption saying where it came from (N72): the answer file, the files the agent wrote (in a
 * monospace block) or its final message. Items added directly have no source and keep the plain caption. */
export function AnswerBlock({ item }: { item: Pick<AnnotationItem, "answer" | "answer_from"> }) {
	const { t } = useTranslation("annotation");
	const from = item.answer_from;
	return (
		<div className="flex flex-col gap-1">
			<span className="text-muted-foreground text-xs font-medium" data-testid="annotation-answer-from">
				{from ? t(`label.answerFrom.${from}`) : t("label.answer")}
			</span>
			<pre
				className={cn(
					"bg-muted/40 max-h-96 overflow-auto rounded-md p-3 text-sm whitespace-pre-wrap",
					from === "files" && "font-mono text-xs",
				)}
				data-testid="annotation-answer"
			>
				{item.answer}
			</pre>
		</div>
	);
}

function LastResult({ result, t, k }: { result: AnnotationLabelResult; t: T; k: number }) {
	return (
		<div
			className="text-muted-foreground flex flex-col gap-0.5 rounded-md border px-4 py-2 text-xs"
			role="status"
			data-testid="annotation-last"
		>
			<span>
				{result.resolved && result.item.resolution
					? t("label.resolved", { label: result.item.resolution.label })
					: t("label.saved", { count: result.item.label_count, k })}
			</span>
			{result.calibration && <span>{t("label.calibration")}</span>}
			{result.calibration_error && (
				<span className="text-chart-error-ink">{t("label.calibrationError", { error: result.calibration_error })}</span>
			)}
		</div>
	);
}

// Items

const STATES: AnnotationState[] = ["open", "labelling", "disputed", "done"];
const stateTone: Record<AnnotationState, StatusTone> = { open: "neutral", labelling: "running", disputed: "warn", done: "ok" };

export function ItemsTab({ queue }: { queue: AnnotationQueue }) {
	const { t } = useTranslation("annotation");
	const [state, setState] = useState<AnnotationState | "">(queue.counts.disputed > 0 ? "disputed" : "");
	const items = useGetAnnotationItemsQuery({ id: queue.id, state: state || undefined });
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="annotation-items-tab">
			{queue.can_manage && <AddItems queue={queue} />}
			<Section
				title={t("items.title")}
				testId="annotation-items"
				actions={
					<NativeSelect
						value={state}
						onChange={(e) => setState(e.target.value as AnnotationState | "")}
						data-testid="annotation-items-filter"
					>
						<option value="">{t("items.filterAll")}</option>
						{STATES.map((s) => (
							<option key={s} value={s}>
								{t(`items.state.${s}`)}
							</option>
						))}
					</NativeSelect>
				}
			>
				{items.isLoading ? (
					<BlockSkeleton rows={4} testId="annotation-items-loading" />
				) : items.error ? (
					<LoadError title={t("loadError")} error={items.error} onRetry={() => void items.refetch()} testId="annotation-items-error" />
				) : (
					<ItemTable queue={queue} items={items.data?.items ?? []} />
				)}
			</Section>
		</div>
	);
}

export function ItemTable({ queue, items }: { queue: AnnotationQueue; items: AnnotationItem[] }) {
	const { t } = useTranslation("annotation");
	const [resolve, resolving] = useResolveAnnotationItemMutation();
	if (items.length === 0) return <EmptyNote testId="annotation-items-empty">{t("items.empty")}</EmptyNote>;
	return (
		<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
			<table className="w-full text-sm">
				<thead className="text-muted-foreground border-b text-left text-xs">
					<tr>
						<th className="px-4 py-2 font-medium">{t("items.task")}</th>
						<th className="px-4 py-2 font-medium">{t("items.labels")}</th>
						<th className="px-4 py-2 font-medium">{t("items.result")}</th>
						{queue.can_manage && <th className="px-4 py-2 text-right font-medium">{t("items.decide")}</th>}
					</tr>
				</thead>
				<tbody>
					{items.map((it) => (
						<tr key={it.id} className="border-b align-top last:border-0" data-testid={`annotation-row-${it.id}`}>
							<td className="px-4 py-2.5">
								<div className="font-mono text-xs">{it.task}</div>
								<div className="text-muted-foreground line-clamp-2 max-w-md text-xs">{it.answer}</div>
							</td>
							<td className="px-4 py-2.5 text-xs">
								{it.labels && it.labels.length > 0 ? (
									<ul className="flex flex-col gap-0.5">
										{it.labels.map((l) => (
											<li key={l.user_id + l.at}>
												<span className={cn(l.label === "correct" ? "text-chart-success-ink" : "text-chart-error-ink")}>{l.label}</span>
												<span className="text-muted-foreground" title={l.email || l.user_id} data-testid="annotation-label-who">
													{" "}
													{l.name || l.email || l.user_id}
												</span>
												{l.note && <span className="text-muted-foreground">: {l.note}</span>}
											</li>
										))}
									</ul>
								) : it.label_count > 0 ? (
									<span className="text-muted-foreground">
										{it.label_count} ({t("items.hiddenUntil")})
									</span>
								) : (
									<span className="text-muted-foreground">0</span>
								)}
							</td>
							<td className="px-4 py-2.5 text-xs">
								<StatusPill tone={stateTone[it.state]} testId={`annotation-state-${it.id}`}>
									{t(`items.state.${it.state}`)}
								</StatusPill>
								{it.resolution && (
									<div className="text-muted-foreground mt-1">
										{it.resolution.label},{" "}
										{it.resolution.method === "decided"
											? t("items.method.decided", { who: it.resolution.name || it.resolution.email || it.resolution.user_id || "" })
											: t("items.method.consensus")}
									</div>
								)}
							</td>
							{queue.can_manage && (
								<td className="px-4 py-2.5 text-right whitespace-nowrap">
									{decideShown(it) && (
										<div className="inline-flex gap-1">
											<Button
												size="sm"
												variant="outline"
												disabled={resolving.isLoading}
												onClick={() => void resolve({ id: queue.id, item: it.id, label: "correct" })}
												data-testid={`annotation-decide-correct-${it.id}`}
											>
												{t("items.decideCorrect")}
											</Button>
											<Button
												size="sm"
												variant="outline"
												disabled={resolving.isLoading}
												onClick={() => void resolve({ id: queue.id, item: it.id, label: "incorrect" })}
												data-testid={`annotation-decide-incorrect-${it.id}`}
											>
												{t("items.decideIncorrect")}
											</Button>
										</div>
									)}
								</td>
							)}
						</tr>
					))}
				</tbody>
			</table>
			{resolving.error ? (
				<p role="alert" className="text-chart-error-ink px-4 py-2 text-xs">
					{getBenchErrorMessage(resolving.error)}
				</p>
			) : null}
		</div>
	);
}

/** When an experiment ended, for sorting: finished, else started, else frozen. */
const endedAt = (e: ExperimentStatus) => e.finished_at || e.started_at || e.frozen_at || "";

/**
 * The experiments whose runs can fill a queue: finished ones on the queue's benchmark (any
 * version), newest first. A queue with no benchmark takes every finished experiment. Pure.
 */
export function finishedRunsOf(experiments: readonly ExperimentStatus[], benchmark: string | undefined): ExperimentStatus[] {
	const want = suiteName(benchmark);
	return experiments
		.filter((e) => e.state === "done" && (!want || suiteName(e.suite) === want) && !isCheckPracticeOrDebug(e))
		.sort((a, b) => endedAt(b).localeCompare(endedAt(a)));
}

/** The body of POST /annotation/queues/{id}/items for one experiment. Pure. */
export function addItemsBody(id: string, experiment: string, sample: number, stratify: boolean) {
	return { id, experiment, sample: Math.max(0, Math.trunc(sample) || 0), stratify };
}

const dateOf = (iso: string, locale: string) => {
	const d = new Date(iso);
	return Number.isNaN(d.getTime()) ? "" : new Intl.DateTimeFormat(locale, { dateStyle: "medium" }).format(d);
};

export function AddItems({ queue }: { queue: AnnotationQueue }) {
	const { t, i18n } = useTranslation("annotation");
	const { t: tx } = useTranslation("experimentsUx");
	const list = useListExperimentsQuery();
	const [add, added] = useAddAnnotationItemsMutation();
	const [assign, assigned] = useAssignAnnotationItemsMutation();
	const [picked, setPicked] = useState("");
	const [sample, setSample] = useState(0);
	const [stratify, setStratify] = useState(true);
	const options = useMemo(() => finishedRunsOf(list.data?.experiments ?? [], queue.benchmark), [list.data, queue.benchmark]);
	const experiment = options.some((e) => e.exp_id === picked) ? picked : (options[0]?.exp_id ?? "");
	const benchmark = suiteName(queue.benchmark);
	return (
		<Section title={t("add.title")} testId="annotation-add">
			{list.isLoading ? (
				<BlockSkeleton rows={2} testId="annotation-add-loading" />
			) : list.error ? (
				<LoadError title={t("add.loadError")} error={list.error} onRetry={() => void list.refetch()} testId="annotation-add-load-error" />
			) : options.length === 0 ? (
				<EmptyNote testId="annotation-add-none">
					{benchmark ? t("add.none", { benchmark }) : t("add.noneAny")}{" "}
					<Link
						to="/evaluate"
						search={(benchmark ? { suite: benchmark } : {}) as never}
						className="text-primary hover:underline"
						data-testid="annotation-add-evaluate"
					>
						{t("add.noneLink")}
					</Link>
				</EmptyNote>
			) : null}
			<form
				className="bg-card flex min-w-0 flex-wrap items-end gap-3 rounded-xl border p-4"
				onSubmit={(e) => {
					e.preventDefault();
					if (!experiment) return;
					void add(addItemsBody(queue.id, experiment, sample, stratify));
				}}
			>
				{options.length > 0 && (
					<>
						<Field label={t("add.experimentPick")}>
							<NativeSelect wrapperClassName="w-80 max-w-full"
								value={experiment}
								onChange={(e) => setPicked(e.target.value)}
								data-testid="annotation-add-experiment"
							>
								{options.map((e) => {
									const name = humanExperimentName(e, (k, o) => String(tx(k as never, (o ?? {}) as never)));
									const date = dateOf(endedAt(e), i18n.language);
									return (
										<option key={e.exp_id} value={e.exp_id}>
											{date ? t("add.experimentOption", { name, date }) : name}
										</option>
									);
								})}
							</NativeSelect>
						</Field>
						<Field label={t("add.sample")}>
							<Input
								type="number"
								min={0}
								value={sample}
								onChange={(e) => setSample(Number(e.target.value))}
								className="w-28"
								data-testid="annotation-add-sample"
							/>
						</Field>
						<label className="flex h-9 items-center gap-2 text-sm">
							<input
								type="checkbox"
								checked={stratify}
								onChange={(e) => setStratify(e.target.checked)}
								data-testid="annotation-add-stratify"
							/>
							{t("add.stratify")}
						</label>
						<Button type="submit" disabled={added.isLoading || !experiment} data-testid="annotation-add-submit">
							{t("add.submit")}
						</Button>
					</>
				)}
				{queue.assignment === "fixed" && (
					<Button
						type="button"
						variant="outline"
						disabled={assigned.isLoading}
						onClick={() => void assign(queue.id)}
						data-testid="annotation-assign"
					>
						{t("add.assign")}
					</Button>
				)}
			</form>
			{added.data && (
				<p className="text-muted-foreground text-xs" role="status" data-testid="annotation-add-result">
					{t("add.result", { added: added.data.added, duplicate: added.data.duplicate })}
					{added.data.skipped && added.data.skipped.length > 0 ? ` ${t("add.skipped", { list: added.data.skipped.join(", ") })}` : ""}
				</p>
			)}
			{assigned.data && (
				<p className="text-muted-foreground text-xs" role="status">
					{t("add.assigned", { count: assigned.data.assigned })}
				</p>
			)}
			{added.error || assigned.error ? (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="annotation-add-error">
					{getBenchErrorMessage(added.error ?? assigned.error)}
				</p>
			) : null}
		</Section>
	);
}

// Agreement

function AgreementTab({ queueId }: { queueId: string }) {
	const { t } = useTranslation("annotation");
	const agreement = useGetAnnotationAgreementQuery(queueId);
	if (agreement.isLoading) return <BlockSkeleton rows={4} testId="annotation-agreement-loading" />;
	if (agreement.error || !agreement.data)
		return (
			<LoadError
				title={t("loadError")}
				error={agreement.error}
				onRetry={() => void agreement.refetch()}
				testId="annotation-agreement-error"
			/>
		);
	return <AgreementPanel data={agreement.data} />;
}

export function AgreementPanel({ data }: { data: AnnotationAgreement }) {
	const { t } = useTranslation("annotation");
	const a = data.agreement;
	const who = (id: string) => (
		<span title={data.labeller_emails?.[id] || id} data-testid="annotation-agreement-who">
			{data.labellers?.[id] || id}
		</span>
	);
	const scale = a.interpretation
		? ` (${t(`agreement.scale.${a.interpretation}` as "agreement.scale.poor", { defaultValue: a.interpretation })})`
		: "";
	return (
		<Section title={t("agreement.title")} testId="annotation-agreement">
			{a.multi_labelled === 0 ? (
				<EmptyNote testId="annotation-agreement-none">{t("agreement.none")}</EmptyNote>
			) : (
				<StatGrid>
					<StatCard
						label={t("agreement.percent")}
						value={pct(a.percent_agreement)}
						hint={t("agreement.items", { count: a.multi_labelled })}
						testId="annotation-agreement-percent"
					/>
					<StatCard
						label={t("agreement.fleiss")}
						value={kappa(a.fleiss)}
						hint={`${t("agreement.items", { count: a.fleiss_items })}${a.fleiss !== null ? scale : ""}`}
						testId="annotation-agreement-fleiss"
					/>
					<StatCard
						label={t("agreement.cohen")}
						value={kappa(a.cohen_mean)}
						hint={a.fleiss === null ? scale.trim() : undefined}
						testId="annotation-agreement-cohen"
					/>
				</StatGrid>
			)}
			{a.cohen && a.cohen.length > 0 && (
				<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
					<table className="w-full text-sm" data-testid="annotation-agreement-pairs">
						<caption className="text-muted-foreground px-4 pt-3 text-left text-xs">{t("agreement.pairs")}</caption>
						<thead className="text-muted-foreground border-b text-left text-xs">
							<tr>
								<th className="px-4 py-2 font-medium">{t("agreement.pairA")}</th>
								<th className="px-4 py-2 font-medium">{t("agreement.pairB")}</th>
								<th className="px-4 py-2 text-right font-medium">{t("agreement.pairItems")}</th>
								<th className="px-4 py-2 text-right font-medium">{t("agreement.pairKappa")}</th>
							</tr>
						</thead>
						<tbody>
							{a.cohen.map((p) => (
								<tr key={`${p.a}-${p.b}`} className="border-b last:border-0">
									<td className="px-4 py-2">{who(p.a)}</td>
									<td className="px-4 py-2">{who(p.b)}</td>
									<td className="px-4 py-2 text-right tabular-nums">{p.items}</td>
									<td className="px-4 py-2 text-right tabular-nums">{kappa(p.kappa)}</td>
								</tr>
							))}
						</tbody>
					</table>
				</div>
			)}
			{a.judge && a.judge.items > 0 && (
				<div className="flex flex-col gap-2" data-testid="annotation-agreement-judge">
					<h3 className="text-sm font-medium">{t("agreement.judge")}</h3>
					<StatGrid>
						<StatCard label={t("agreement.judgeRate")} value={pct(a.judge.rate)} hint={t("agreement.items", { count: a.judge.items })} />
						<StatCard label={t("agreement.judgeKappa")} value={kappa(a.judge.kappa)} />
						<StatCard label={t("agreement.falsePasses")} value={a.judge.false_passes} />
						<StatCard label={t("agreement.falseFails")} value={a.judge.false_fails} />
					</StatGrid>
				</div>
			)}
		</Section>
	);
}

// Settings

export function SettingsTab({ queue }: { queue: AnnotationQueue }) {
	const { t } = useTranslation("annotation");
	const { t: tu } = useTranslation("uf6b");
	const navigate = useNavigate();
	const [update, updated] = useUpdateAnnotationQueueMutation();
	const [remove, removed] = useDeleteAnnotationQueueMutation();
	const [name, setName] = useState(queue.name);
	const [instructions, setInstructions] = useState(queue.instructions ?? "");
	const [reviewers, setReviewers] = useState(queue.reviewers_per_item);
	const [blind, setBlind] = useState(queue.blind);
	const [labellers, setLabellers] = useState<string[]>(queue.reviewers ?? []);
	const disabled = !queue.can_manage;
	const badPerItem = perItemProblem(reviewers);
	// M6-73: a queue keeps a name; Save stays off while the name is blank.
	const noName = name.trim() === "";
	const confirm = useConfirm();
	return (
		<Section title={t("settings.title")} description={disabled ? t("settings.readOnly") : undefined} testId="annotation-settings">
			<form
				className="bg-card grid min-w-0 grid-cols-1 gap-4 rounded-xl border p-5 md:grid-cols-2"
				onSubmit={(e) => {
					e.preventDefault();
					if (badPerItem || noName) return;
					void update({
						id: queue.id,
						name: name.trim(),
						instructions,
						reviewers_per_item: reviewers,
						blind,
						reviewers: labellers,
					});
				}}
			>
				<Field label={t("create.name")}>
					<Input
						value={name}
						onChange={(e) => setName(e.target.value)}
						disabled={disabled}
						required
						aria-invalid={noName || undefined}
						data-testid="annotation-settings-name"
					/>
				</Field>
				<Field label={t("create.reviewers")} hint={badPerItem ? t("create.reviewersRange") : t("create.reviewersHint")}>
					<Input
						type="number"
						min={1}
						max={5}
						value={Number.isNaN(reviewers) ? "" : reviewers}
						onChange={(e) => setReviewers(e.target.value === "" ? Number.NaN : Number(e.target.value))}
						aria-invalid={badPerItem || undefined}
						disabled={disabled}
						data-testid="annotation-settings-reviewers"
					/>
				</Field>
				<LabellerPicker value={labellers} onChange={setLabellers} disabled={disabled} testId="annotation-settings-labellers" />
				<label className="flex items-center gap-2 text-sm">
					<input
						type="checkbox"
						checked={blind}
						onChange={(e) => setBlind(e.target.checked)}
						disabled={disabled}
						data-testid="annotation-settings-blind"
					/>
					{t("create.blind")}
				</label>
				<Field label={t("create.instructions")}>
					<textarea
						className="border-input bg-background min-h-20 rounded-md border px-3 py-2 text-sm"
						value={instructions}
						onChange={(e) => setInstructions(e.target.value)}
						disabled={disabled}
					/>
				</Field>
				{!disabled && (
					<div className="flex flex-wrap items-end justify-end gap-2 md:col-span-2">
						{updated.isSuccess && <span className="text-muted-foreground mr-auto text-xs">{t("settings.saved")}</span>}
						{updated.error || removed.error ? (
							<p role="alert" className="text-chart-error-ink mr-auto text-xs">
								{getBenchErrorMessage(updated.error ?? removed.error)}
							</p>
						) : null}
						<Button
							type="button"
							variant="ghost"
							onClick={() => void update({ id: queue.id, archived: !queue.archived })}
							data-testid="annotation-settings-archive"
						>
							{queue.archived ? t("settings.unarchive") : t("settings.archive")}
						</Button>
						<Button
							type="button"
							variant="destructive"
							disabled={removed.isLoading}
							onClick={() =>
								void (async () => {
									const opts: DestructiveConfirmOptions = {
										destructive: true,
										title: tu("queue.deleteTitle", { name: queue.name }),
										confirmLabel: tu("queue.deleteYes"),
									};
									if (!(await confirm(t("settings.deleteConfirm"), opts))) return;
									await remove(queue.id)
										.unwrap()
										.then(() => {
											notify({ text: tu("queue.deleted", { name: queue.name }) });
											void navigate({ to: "/annotation" });
										})
										.catch(() => {});
								})()
							}
							data-testid="annotation-settings-delete"
						>
							{t("settings.delete")}
						</Button>
						<Button type="submit" disabled={updated.isLoading || badPerItem || noName} data-testid="annotation-settings-save">
							{t("settings.save")}
						</Button>
					</div>
				)}
			</form>
		</Section>
	);
}