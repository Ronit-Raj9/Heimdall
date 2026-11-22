import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useGetSuitesQuery } from "@/lib/store/apis/benchApi";
import { apiErrorMessage } from "@/lib/store/apis/integrationsApi";
import {
	useAcceptMaximTaskMutation,
	useDraftMaximTasksMutation,
	useGetMaximBatchQuery,
	useListMaximBatchesQuery,
	usePullMaximFailuresMutation,
	useRejectMaximTaskMutation,
	useRerunMaximReadinessMutation,
} from "@/lib/store/apis/maximFailuresApi";
import { useGetMaximQuery } from "@/lib/store/apis/traceToolsApi";
import type { MaximFailureBatch, MaximTaskDraft } from "@/lib/types/maximFailures";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { parseAsString, useQueryState } from "nuqs";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	ClusterList,
	DEFAULT_FAILURE_FORM,
	FailureCounts,
	TaskReviewCard,
	draftedClusters,
	filterFromForm,
	type FailureForm,
} from "./maximFailuresParts";

/**
 * The Maxim showcase (/maxim?batch=): production failures from the org's Maxim log repository,
 * grouped by failure mode, become tasks a person reviews before they count.
 */
export default function MaximFailuresView() {
	const { t } = useTranslation("maximFailures");
	const conn = useGetMaximQuery();
	const list = useListMaximBatchesQuery();
	const [batchId, setBatchId] = useQueryState("batch", parseAsString.withDefault(""));
	const active = batchId || list.data?.batches[0]?.id || "";
	const steps = ["pull", "cluster", "draft", "review", "ready"] as const;
	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-6" data-testid="maxim-failures-screen">
			<PageHeader title={t("title")} subtitle={t("subtitle")} />
			<ol className="grid gap-2 text-sm sm:grid-cols-5" data-testid="maxim-steps">
				{steps.map((s, i) => (
					<li key={s} className="bg-muted/30 flex gap-2 rounded-lg border px-3 py-2">
						<span className="text-muted-foreground tabular-nums">{i + 1}</span>
						<span>{t(`steps.${s}`)}</span>
					</li>
				))}
			</ol>
			{conn.isLoading ? (
				<BlockSkeleton testId="maxim-conn-loading" rows={1} />
			) : conn.data?.connected ? (
				<>
					<p className="text-muted-foreground text-sm" data-testid="maxim-connected">
						{t("connectedTo", { repo: conn.data.repo_id, workspace: conn.data.workspace_id })}
					</p>
					<PullForm onPulled={(id) => void setBatchId(id)} />
				</>
			) : (
				<div className="flex flex-wrap items-center gap-3 rounded-xl border border-dashed p-4" data-testid="maxim-not-connected">
					<p className="text-muted-foreground text-sm">{t("notConnected")}</p>
					<Button asChild size="sm">
						<Link to="/settings" search={{ tab: "integrations" } as never}>
							{t("connect")}
						</Link>
					</Button>
				</div>
			)}
			<Section title={t("imports.title")} testId="maxim-imports">
				{list.isLoading ? (
					<BlockSkeleton testId="maxim-imports-loading" rows={2} />
				) : list.isError ? (
					<LoadError title={t("imports.title")} error={list.error} onRetry={() => void list.refetch()} testId="maxim-imports-error" />
				) : !list.data?.batches.length ? (
					<EmptyNote testId="maxim-imports-empty">{t("imports.empty")}</EmptyNote>
				) : (
					<ul className="flex flex-wrap gap-2">
						{list.data.batches.map((b) => (
							<li key={b.id}>
								<button
									type="button"
									className={cn("rounded-lg border px-3 py-2 text-left text-xs", b.id === active && "border-primary bg-primary/5")}
									onClick={() => void setBatchId(b.id)}
									data-testid={`maxim-import-${b.id}`}
								>
									<span className="block font-medium">{new Date(b.created).toLocaleString()}</span>
									<span className="text-muted-foreground block">{t("imports.row", { failed: b.counts.failed, clusters: b.clusters, tasks: b.tasks })}</span>
									{b.pending > 0 && <span className="block text-amber-700 dark:text-amber-400">{t("imports.pending", { count: b.pending })}</span>}
								</button>
							</li>
						))}
					</ul>
				)}
			</Section>
			{active && <BatchPanel id={active} />}
		</section>
	);
}

function PullForm({ onPulled }: { onPulled(id: string): void }) {
	const { t } = useTranslation("maximFailures");
	const [form, setForm] = useState<FailureForm>(DEFAULT_FAILURE_FORM);
	const [pull, state] = usePullMaximFailuresMutation();
	const [error, setError] = useState<string | null>(null);
	const filter = filterFromForm(form);
	const set = (patch: Partial<FailureForm>) => setForm({ ...form, ...patch });
	return (
		<Section title={t("filter.title")} description={t("filter.description")} testId="maxim-pull">
			<form
				className="bg-card flex flex-col gap-3 rounded-xl border p-4"
				onSubmit={(e) => {
					e.preventDefault();
					if (!filter) return;
					setError(null);
					pull(filter)
						.unwrap()
						.then((b) => onPulled(b.id))
						.catch((err: unknown) => setError(apiErrorMessage(err, t("filter.pull"))));
				}}
			>
				<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
					<label className="flex flex-col gap-1 text-sm">
						<span className="font-medium">{t("filter.evaluators")}</span>
						<Input value={form.evaluators} onChange={(e) => set({ evaluators: e.target.value })} data-testid="maxim-filter-evaluators" />
						<span className="text-muted-foreground text-xs">{t("filter.evaluatorsHint")}</span>
					</label>
					<label className="flex flex-col gap-1 text-sm">
						<span className="font-medium">{t("filter.scoreBelow")}</span>
						<Input inputMode="decimal" value={form.scoreBelow} onChange={(e) => set({ scoreBelow: e.target.value })} data-testid="maxim-filter-score" />
					</label>
					<label className="flex flex-col gap-1 text-sm">
						<span className="font-medium">{t("filter.scoreEvaluators")}</span>
						<Input value={form.scoreEvaluators} onChange={(e) => set({ scoreEvaluators: e.target.value })} />
						<span className="text-muted-foreground text-xs">{t("filter.scoreEvaluatorsHint")}</span>
					</label>
					<label className="flex flex-col gap-1 text-sm">
						<span className="font-medium">{t("filter.days")}</span>
						<Input inputMode="numeric" value={form.days} onChange={(e) => set({ days: e.target.value })} />
					</label>
					<label className="flex flex-col gap-1 text-sm">
						<span className="font-medium">{t("filter.limit")}</span>
						<Input inputMode="numeric" value={form.limit} onChange={(e) => set({ limit: e.target.value })} />
					</label>
					<label className="flex items-center gap-2 self-end pb-2 text-sm">
						<input type="checkbox" className="size-4" checked={form.errors} onChange={(e) => set({ errors: e.target.checked })} />
						<span>{t("filter.errors")}</span>
					</label>
				</div>
				{!filter && <p className="text-muted-foreground text-xs">{t("filter.needRule")}</p>}
				{error && (
					<p className="text-chart-error-ink text-sm break-words" role="alert" data-testid="maxim-pull-error">
						{error}
					</p>
				)}
				<div>
					<Button type="submit" disabled={!filter || state.isLoading} data-testid="maxim-pull-button">
						{state.isLoading ? t("filter.pulling") : t("filter.pull")}
					</Button>
				</div>
			</form>
		</Section>
	);
}

function BatchPanel({ id }: { id: string }) {
	const { t } = useTranslation("maximFailures");
	const q = useGetMaximBatchQuery(id, {
		pollingInterval: 10000,
		skipPollingIfUnfocused: true,
	});
	if (q.isLoading) return <BlockSkeleton testId="maxim-batch-loading" rows={4} />;
	if (q.isError || !q.data) return <LoadError title={t("clusters.title")} error={q.error} onRetry={() => void q.refetch()} testId="maxim-batch-error" />;
	return <BatchBody batch={q.data} />;
}

function BatchBody({ batch }: { batch: MaximFailureBatch }) {
	const { t } = useTranslation("maximFailures");
	const suites = useGetSuitesQuery();
	const drafted = useMemo(() => draftedClusters(batch.tasks), [batch.tasks]);
	const [selected, setSelected] = useState<Set<string>>(new Set());
	const [suite, setSuite] = useState("");
	const [perCluster, setPerCluster] = useState("1");
	const [draft, draftState] = useDraftMaximTasksMutation();
	const [note, setNote] = useState<string | null>(null);
	const toggle = (cid: string) => {
		const next = new Set(selected);
		if (next.has(cid)) next.delete(cid);
		else next.add(cid);
		setSelected(next);
	};
	return (
		<>
			<FailureCounts counts={batch.counts} />
			{batch.truncated && <p className="text-xs text-amber-700 dark:text-amber-400">{t("truncated")}</p>}
			{batch.warnings?.map((w) => (
				<p key={w} className="text-muted-foreground text-xs break-words">
					{w}
				</p>
			))}
			<Section title={t("clusters.title")} description={t("clusters.description")} testId="maxim-clusters-section">
				<ClusterList clusters={batch.clusters} selected={selected} drafted={drafted} onToggle={toggle} />
				{batch.clusters.length > 0 && (
					<div className="flex flex-wrap items-end gap-3">
						<label className="flex flex-col gap-1 text-sm">
							<span className="font-medium">{t("draft.benchmark")}</span>
							<select
								className="border-input bg-background h-9 rounded-md border px-2 text-sm"
								value={suite}
								onChange={(e) => setSuite(e.target.value)}
								data-testid="maxim-draft-suite"
							>
								<option value="">{t("draft.pickBenchmark")}</option>
								{suites.data?.suites.map((s) => (
									<option key={s.id} value={s.id}>
										{s.id}
									</option>
								))}
							</select>
						</label>
						<label className="flex flex-col gap-1 text-sm">
							<span className="font-medium">{t("draft.perCluster")}</span>
							<Input className="w-20" inputMode="numeric" value={perCluster} onChange={(e) => setPerCluster(e.target.value)} />
						</label>
						<Button
							type="button"
							disabled={!suite || selected.size === 0 || draftState.isLoading}
							data-testid="maxim-draft-button"
							onClick={() => {
								setNote(null);
								draft({ batch: batch.id, suite, clusters: [...selected], per_cluster: Number.parseInt(perCluster, 10) || 1 })
									.unwrap()
									.then((r) => {
										setSelected(new Set());
										setNote([t("draft.done", { count: r.tasks.length }), ...r.warnings].join(" "));
									})
									.catch((err: unknown) => setNote(apiErrorMessage(err, t("draft.button", { count: selected.size }))));
							}}
						>
							{t("draft.button", { count: selected.size })}
						</Button>
					</div>
				)}
				{note && (
					<p className="text-sm break-words" role="status" data-testid="maxim-draft-note">
						{note}
					</p>
				)}
			</Section>
			<Section title={t("tasks.title")} description={t("tasks.description")} testId="maxim-tasks-section">
				{batch.tasks.length === 0 ? (
					<EmptyNote testId="maxim-tasks-empty">{t("tasks.empty")}</EmptyNote>
				) : (
					<div className="flex flex-col gap-3">
						{batch.tasks.map((task) => (
							<TaskRow key={`${task.suite}/${task.id}`} batch={batch.id} task={task} />
						))}
					</div>
				)}
			</Section>
		</>
	);
}

function TaskRow({ batch, task }: { batch: string; task: MaximTaskDraft }) {
	const [accept, a] = useAcceptMaximTaskMutation();
	const [reject, r] = useRejectMaximTaskMutation();
	const [rerun, rr] = useRerunMaximReadinessMutation();
	const [message, setMessage] = useState<string | null>(null);
	const ref = { batch, suite: task.suite, task: task.id };
	const done = (p: Promise<{ message?: string }>) =>
		p.then((res) => setMessage(res.message ?? null)).catch((err: unknown) => setMessage(apiErrorMessage(err, "")));
	return (
		<TaskReviewCard
			task={task}
			busy={a.isLoading || r.isLoading || rr.isLoading}
			message={message}
			onAccept={(review) => void done(accept({ ...ref, ...review }).unwrap())}
			onReject={(reason) => void done(reject({ ...ref, reason }).unwrap())}
			onRerun={() => void done(rerun(ref).unwrap())}
		/>
	);
}
