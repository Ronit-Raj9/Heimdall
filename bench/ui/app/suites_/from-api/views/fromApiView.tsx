import { JobRunButton } from "@/components/jobs/jobRunButton";
import type { PickedModel } from "@/components/models/groupedModelPicker";
import { isPicked, LabeledModelPicker, usePickedKeyState } from "@/components/models/labeledModelPicker";
import { ConfirmButton } from "@/components/platform/badges";
import { useConfirm } from "@/components/platform/confirmDialog";
import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import {
	useApproveAppTaskRulesMutation,
	useCreateAppTaskDraftMutation,
	useGetAppTaskDraftQuery,
	useGetAppTaskDraftsQuery,
	useReviewAppTaskMutation,
	useWriteAppTasksMutation,
} from "@/lib/store/apis/appTasksApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import type { AppPlan, AppRule, AppTasksDraft, AppTasksDraftSummary, ListedAppTasksDraft, RuleKind } from "@/lib/types/appTasks";
import { Link } from "@tanstack/react-router";
import { useEffect, useMemo, useState, type ChangeEvent, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import "@/app/gateway/views/w4intI18n";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		appTasks: typeof import("@/locales/en/appTasks.json");
	}
}

const ID_RE = /^[a-z0-9][a-z0-9._-]{0,63}$/;
const APP_RE = /^[a-z][a-z0-9_-]{0,31}$/;
const KINDS: RuleKind[] = ["must", "must_not", "escalate", "info"];
const field = "border-input bg-card rounded-md border px-3 py-2 text-sm";

/** /suites/from-api: an OpenAPI file and a policy become a benchmark of support tasks against a copy of the app. */
export default function FromApiView() {
	const { t } = useTranslation("appTasks");
	const session = useSession();
	const writable = canUse(session, "member");
	const list = useGetAppTaskDraftsQuery();
	const [open, setOpen] = useState<string | null>(null);
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="from-api-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				breadcrumbs={[{ label: "Benchmarks", to: "/suites" }, { label: t("breadcrumb") }]}
			/>
			{!writable && (
				<p role="note" className="text-muted-foreground text-sm" data-testid="from-api-readonly">
					{t("readOnly")}
				</p>
			)}
			{open ? (
				<DraftPanel id={open} writable={writable} onBack={() => setOpen(null)} />
			) : (
				<>
					{writable && <NewDraftForm onCreated={setOpen} />}
					<Section title={t("list.title")} testId="from-api-drafts">
						{list.isLoading ? (
							<BlockSkeleton rows={3} testId="from-api-drafts-loading" />
						) : list.isError ? (
							<LoadError title={t("listError")} error={list.error} onRetry={() => void list.refetch()} testId="from-api-drafts-error" />
						) : (
							<DraftList drafts={list.data?.drafts ?? []} onOpen={setOpen} />
						)}
					</Section>
				</>
			)}
		</section>
	);
}

export function DraftList({ drafts, onOpen }: { drafts: AppTasksDraftSummary[]; onOpen(id: string): void }) {
	const { t } = useTranslation("appTasks");
	if (drafts.length === 0) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="from-api-drafts-empty">
				{t("list.empty")}
			</p>
		);
	}
	return (
		<ul className="flex flex-col gap-2">
			{drafts.map((d) => (
				<li key={d.id} className="flex flex-wrap items-center gap-3 rounded-md border px-3 py-2" data-testid={`from-api-draft-${d.id}`}>
					<span className="font-medium">{d.title || d.app}</span>
					<span className="text-muted-foreground font-mono text-xs">{d.suite}</span>
					<Badge variant="outline">{t(`status.${d.status}`)}</Badge>
					<span className="text-muted-foreground text-xs">{t("list.counts", { rules: d.rules, tasks: d.tasks, pending: d.pending })}</span>
					<Button variant="outline" size="sm" onClick={() => onOpen(d.id)} dataTestId={`from-api-open-${d.id}`}>
						{t("list.open")}
					</Button>
				</li>
			))}
		</ul>
	);
}

async function readFile(e: ChangeEvent<HTMLInputElement>, set: (text: string, name: string) => void) {
	const f = e.target.files?.[0];
	if (f) set(await f.text(), f.name);
}

function NewDraftForm({ onCreated }: { onCreated(id: string): void }) {
	const { t } = useTranslation("appTasks");
	const [create, { isLoading, error }] = useCreateAppTaskDraftMutation();
	const [suite, setSuite] = useState("");
	const [app, setApp] = useState("");
	const [title, setTitle] = useState("");
	const [spec, setSpec] = useState("");
	const [specName, setSpecName] = useState("");
	const [policy, setPolicy] = useState("");
	const [policyName, setPolicyName] = useState("");
	const idsOk = ID_RE.test(suite) && APP_RE.test(app) && app !== "customer";
	const submit = async (e: FormEvent) => {
		e.preventDefault();
		if (!idsOk) return;
		const res = await create({
			suite,
			app,
			title: title.trim() || undefined,
			spec,
			spec_name: specName || undefined,
			policy,
			policy_name: policyName || undefined,
		});
		if ("data" in res && res.data) onCreated(res.data.id);
	};
	return (
		<Section title={t("new.title")} testId="from-api-new">
			<form className="flex max-w-3xl flex-col gap-4" onSubmit={(e) => void submit(e)} data-testid="from-api-new-form">
				<div className="grid gap-4 sm:grid-cols-3">
					<label className="flex flex-col gap-1 text-sm">
						{t("new.benchmark")}
						<Input
							value={suite}
							onChange={(e) => setSuite(e.target.value.trim())}
							placeholder="shop-support"
							data-testid="from-api-suite"
						/>
						<span className="text-muted-foreground text-xs">{t("new.benchmarkHelp")}</span>
					</label>
					<label className="flex flex-col gap-1 text-sm">
						{t("new.app")}
						<Input value={app} onChange={(e) => setApp(e.target.value.trim())} placeholder="shop" data-testid="from-api-app" />
						<span className="text-muted-foreground text-xs">{t("new.appHelp")}</span>
					</label>
					<label className="flex flex-col gap-1 text-sm">
						{t("new.appTitle")}
						<Input value={title} onChange={(e) => setTitle(e.target.value)} data-testid="from-api-title" />
					</label>
				</div>
				<label className="flex flex-col gap-1 text-sm">
					{t("new.spec")}
					<span className="text-muted-foreground text-xs">{t("new.pasteOrPick")}</span>
					<input
						type="file"
						accept=".yaml,.yml,.json"
						onChange={(e) => void readFile(e, (s, n) => (setSpec(s), setSpecName(n)))}
						data-testid="from-api-spec-file"
					/>
					<textarea
						className={`${field} h-40 font-mono text-xs`}
						value={spec}
						onChange={(e) => setSpec(e.target.value)}
						data-testid="from-api-spec"
					/>
				</label>
				<label className="flex flex-col gap-1 text-sm">
					{t("new.policy")}
					<span className="text-muted-foreground text-xs">{t("new.pasteOrPick")}</span>
					<input
						type="file"
						accept=".md,.txt,.markdown"
						onChange={(e) => void readFile(e, (s, n) => (setPolicy(s), setPolicyName(n)))}
						data-testid="from-api-policy-file"
					/>
					<textarea
						className={`${field} h-32 text-sm`}
						value={policy}
						onChange={(e) => setPolicy(e.target.value)}
						data-testid="from-api-policy"
					/>
				</label>
				{(suite || app) && !idsOk && <p className="text-chart-error-ink text-xs">{t("new.invalidIds")}</p>}
				{error !== undefined && (
					<p role="alert" className="text-chart-error-ink text-xs" data-testid="from-api-new-error">
						{getBenchErrorMessage(error)}
					</p>
				)}
				<div>
					<Button type="submit" disabled={!idsOk || !spec.trim() || !policy.trim()} isLoading={isLoading} dataTestId="from-api-new-submit">
						{t("new.submit")}
					</Button>
				</div>
			</form>
		</Section>
	);
}

function DraftPanel({ id, writable, onBack }: { id: string; writable: boolean; onBack(): void }) {
	const { t } = useTranslation("appTasks");
	const q = useGetAppTaskDraftQuery(id);
	if (q.isLoading) return <BlockSkeleton rows={5} testId="from-api-draft-loading" />;
	if (q.isError || !q.data)
		return <LoadError title={t("loadError")} error={q.error} onRetry={() => void q.refetch()} testId="from-api-draft-error" />;
	return <DraftDetail draft={q.data} writable={writable} onBack={onBack} onChanged={() => void q.refetch()} />;
}

/** The draft with every list present: a draft saved with no plans used to carry "plans": null (N237). */
export function listedDraft(d: AppTasksDraft): ListedAppTasksDraft {
	return {
		...d,
		rules: (d.rules ?? []).map((r) => ({ ...r, operations: r.operations ?? [] })),
		plans: d.plans ?? [],
		dropped: d.dropped ?? [],
		written: d.written ?? [],
		calls: d.calls ?? [],
		warnings: d.warnings ?? [],
		operations: d.operations ?? [],
		collections: d.collections ?? [],
	};
}

/** One draft, step by step: model, rules, tasks, write, review. */
export function DraftDetail({
	draft: raw,
	writable,
	onBack,
	onChanged,
}: {
	draft: AppTasksDraft;
	writable: boolean;
	onBack(): void;
	onChanged(): void;
}) {
	const { t } = useTranslation("appTasks");
	const draft = useMemo(() => listedDraft(raw), [raw]);
	// N238: one shared picker, the draft's pinned model when it has one, else nothing picked (no "openai" prefill).
	const [picked, setPicked] = useState<PickedModel | null>(draft.model ?? null);
	const keyState = usePickedKeyState(picked);
	const [maxUsd, setMaxUsd] = useState("");
	const usd = Number(maxUsd);
	const modelParams = {
		draft: draft.id,
		yes: true,
		provider: picked?.provider ?? "",
		model: picked?.model ?? "",
		...(maxUsd && usd > 0 ? { max_usd: usd } : {}),
	};
	const modelOk = isPicked(picked) && keyState === "ok";
	const calls = draft.calls.reduce((n, c) => n + c.attempts, 0);
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="from-api-draft">
			<div className="flex flex-wrap items-center gap-3">
				<Button variant="outline" size="sm" onClick={onBack} dataTestId="from-api-back">
					{t("list.title")}
				</Button>
				<span className="font-medium">{draft.title || draft.app}</span>
				<span className="text-muted-foreground font-mono text-xs">{draft.suite}</span>
				<Badge variant="outline" data-testid="from-api-status">
					{t(`status.${draft.status}`)}
				</Badge>
				<span className="text-muted-foreground text-xs" data-testid="from-api-spend">
					{t("spend", { calls, usd: draft.spent_usd.toFixed(4) })}
				</span>
			</div>
			<p className="text-sm" data-testid="from-api-next">
				{draft.next}
			</p>
			<Section title={t("app.title")} testId="from-api-app-copy">
				<p className="text-muted-foreground text-sm">
					{t("app.operations", { count: draft.operations.length, collections: draft.collections.join(", ") || "-" })}
				</p>
				<ul className="flex flex-wrap gap-1.5">
					{draft.operations.map((op) => (
						<li key={op.name} className="rounded border px-2 py-0.5 font-mono text-xs">
							{op.name}{" "}
							<span className="text-muted-foreground">
								{op.method} {op.path}
							</span>
						</li>
					))}
				</ul>
			</Section>
			{writable && draft.status !== "written" && (
				<Section title={t("model.title")} description={t("model.description")} testId="from-api-model">
					<div className="grid max-w-2xl gap-3 sm:grid-cols-3">
						<div className="sm:col-span-2">
							<LabeledModelPicker
								label={t("model.model")}
								hint={modelOk ? undefined : t("model.pick")}
								value={picked}
								onChange={setPicked}
								testId="from-api-model-pick"
							/>
						</div>
						<label className="flex flex-col gap-1 text-sm">
							{t("model.maxUsd")}
							<Input value={maxUsd} inputMode="decimal" onChange={(e) => setMaxUsd(e.target.value.trim())} data-testid="from-api-max-usd" />
							<span className="text-muted-foreground text-xs">{t("model.maxUsdHelp")}</span>
						</label>
					</div>
				</Section>
			)}
			<RulesSection draft={draft} writable={writable} modelOk={modelOk} params={modelParams} onChanged={onChanged} />
			{(draft.status === "rules_approved" || draft.status === "generated" || draft.status === "written") && (
				<TasksSection draft={draft} writable={writable} modelOk={modelOk} params={modelParams} onChanged={onChanged} />
			)}
			{draft.status === "generated" && draft.plans.length > 0 && writable && <WriteSection draft={draft} onChanged={onChanged} />}
			{draft.written.length > 0 && <ReviewSection draft={draft} writable={writable} onChanged={onChanged} />}
		</div>
	);
}

function RulesSection({
	draft,
	writable,
	modelOk,
	params,
	onChanged,
}: {
	draft: ListedAppTasksDraft;
	writable: boolean;
	modelOk: boolean;
	params: object;
	onChanged(): void;
}) {
	const { t } = useTranslation("appTasks");
	// For members the server leaves approved_by empty and sends only the display name (N33).
	const approvedByName = (draft as ListedAppTasksDraft & { approved_by_name?: string }).approved_by_name;
	const [rules, setRules] = useState<AppRule[]>(draft.rules);
	const [approve, { isLoading, error }] = useApproveAppTaskRulesMutation();
	const confirm = useConfirm();
	// Approving again discards drafted tasks (ApproveRules resets the plans): ask first when there are any.
	const onApprove = async () => {
		const n = draft.plans.length;
		if (n > 0) {
			const ok = await confirm(t("rules.approveAgain", { count: n }), {
				title: t("rules.approveAgainTitle"),
				confirmLabel: t("rules.approveAgainConfirm"),
				cancelLabel: t("rules.keep"),
				destructive: true,
			});
			if (!ok) return;
		}
		void approve({ id: draft.id, rules });
	};
	useEffect(() => setRules(draft.rules), [draft.rules]);
	const edit = (i: number, patch: Partial<AppRule>) => setRules((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)));
	const canEdit = writable && draft.status !== "written";
	return (
		<Section title={t("rules.title")} description={t("rules.description")} testId="from-api-rules">
			{canEdit && (draft.status === "new" || draft.status === "rules_drafted") && (
				<div>
					<JobRunButton
						kind="apptasks.generate"
						disabled={!modelOk}
						params={{ ...params, stage: "rules" }}
						label={draft.rules.length ? t("rules.redraft") : t("rules.draft")}
						confirm={t("rules.confirm")}
						variant={draft.rules.length ? "outline" : "default"}
						onDone={onChanged}
					/>
				</div>
			)}
			{rules.length > 0 && (
				<ol className="flex flex-col gap-3" data-testid="from-api-rule-list">
					{rules.map((r, i) => (
						<li key={r.id} className="flex flex-col gap-1.5 rounded-md border p-3" data-testid={`from-api-rule-${r.id}`}>
							<div className="flex flex-wrap items-center gap-2">
								<span className="font-mono text-xs">{r.id}</span>
								{canEdit ? (
									<select
										className={field}
										value={r.kind}
										aria-label={t("rules.kind")}
										onChange={(e) => edit(i, { kind: e.target.value as RuleKind })}
									>
										{KINDS.map((k) => (
											<option key={k} value={k}>
												{t(`rules.kinds.${k}`)}
											</option>
										))}
									</select>
								) : (
									<Badge variant="outline">{t(`rules.kinds.${r.kind}`)}</Badge>
								)}
							</div>
							{canEdit ? (
								<>
									<textarea className={`${field} h-16`} value={r.text} onChange={(e) => edit(i, { text: e.target.value })} />
									<Input
										aria-label={t("rules.operations")}
										placeholder={t("rules.operations")}
										value={r.operations.join(", ")}
										onChange={(e) =>
											edit(i, {
												operations: e.target.value
													.split(",")
													.map((s) => s.trim())
													.filter(Boolean),
											})
										}
									/>
								</>
							) : (
								<p className="text-sm">{r.text}</p>
							)}
						</li>
					))}
				</ol>
			)}
			{(approvedByName || draft.approved_by) && (
				<p className="text-muted-foreground text-xs" title={draft.approved_by || undefined} data-testid="from-api-rules-approved">
					{t("rules.approved", { who: approvedByName || draft.approved_by })}
				</p>
			)}
			{error !== undefined && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="from-api-rules-error">
					{getBenchErrorMessage(error)}
				</p>
			)}
			{canEdit && rules.length > 0 && (
				<div>
					<Button
						variant={draft.status === "rules_drafted" ? "default" : "outline"}
						isLoading={isLoading}
						onClick={() => void onApprove()}
						dataTestId="from-api-approve"
					>
						{t("rules.approve")}
					</Button>
				</div>
			)}
		</Section>
	);
}

function TasksSection({
	draft,
	writable,
	modelOk,
	params,
	onChanged,
}: {
	draft: ListedAppTasksDraft;
	writable: boolean;
	modelOk: boolean;
	params: object;
	onChanged(): void;
}) {
	const { t } = useTranslation("appTasks");
	const [perRule, setPerRule] = useState(2);
	const [judged, setJudged] = useState(draft.judged);
	return (
		<Section title={t("tasks.title")} description={t("tasks.description")} testId="from-api-tasks">
			{writable && draft.status !== "written" && (
				<div className="flex flex-wrap items-end gap-4">
					<label className="flex flex-col gap-1 text-sm">
						{t("tasks.perRule")}
						<select className={field} value={perRule} onChange={(e) => setPerRule(Number(e.target.value))} data-testid="from-api-per-rule">
							{[1, 2, 3, 4, 5].map((n) => (
								<option key={n} value={n}>
									{n}
								</option>
							))}
						</select>
					</label>
					<label className="flex items-center gap-2 text-sm">
						<input type="checkbox" checked={judged} onChange={(e) => setJudged(e.target.checked)} data-testid="from-api-judged" />
						{t("tasks.judged")}
					</label>
					<div>
						<JobRunButton
							kind="apptasks.generate"
							disabled={!modelOk}
							params={{ ...params, stage: "tasks", per_rule: perRule, judged }}
							label={t("tasks.draft")}
							confirm={t("tasks.confirm")}
							onDone={onChanged}
						/>
					</div>
				</div>
			)}
			<p className="text-sm" data-testid="from-api-task-counts">
				{t("tasks.drafted", { count: draft.plans.length })}, {t("tasks.dropped", { count: draft.dropped.length })}
			</p>
			{draft.plans.length === 0 ? <p className="text-muted-foreground text-sm">{t("tasks.none")}</p> : <PlanTable plans={draft.plans} />}
			{draft.dropped.length > 0 && (
				<details className="text-sm" data-testid="from-api-dropped">
					<summary className="cursor-pointer">{t("tasks.droppedTitle")}</summary>
					<ul className="mt-2 flex flex-col gap-1">
						{draft.dropped.map((d, i) => (
							<li key={`${d.rule_id}-${i}`} className="text-muted-foreground text-xs">
								<span className="font-mono">{d.rule_id}</span> {d.title}: {d.reason}
							</li>
						))}
					</ul>
				</details>
			)}
		</Section>
	);
}

export function PlanTable({ plans }: { plans: AppPlan[] }) {
	const { t } = useTranslation("appTasks");
	return (
		<div className="overflow-x-auto">
			<table className="w-full min-w-[40rem] text-sm" data-testid="from-api-plans">
				<thead>
					<tr className="text-muted-foreground text-left text-xs">
						<th className="py-1 pr-3">{t("tasks.rule")}</th>
						<th className="py-1 pr-3">{t("tasks.title")}</th>
						<th className="py-1 pr-3">{t("tasks.checks")}</th>
						<th className="py-1">{t("tasks.caught")}</th>
					</tr>
				</thead>
				<tbody>
					{plans.map((p, i) => (
						<tr key={`${p.scenario.rule_id}-${i}`} className="border-t align-top">
							<td className="py-1.5 pr-3 font-mono text-xs">{p.scenario.rule_id}</td>
							<td className="py-1.5 pr-3">
								<div>{p.scenario.title}</div>
								<div className="text-muted-foreground text-xs">{p.scenario.opening_message}</div>
							</td>
							<td className="py-1.5 pr-3 text-xs">
								{p.checks}: {p.summary.join("; ")}
							</td>
							<td className="py-1.5 text-xs">{Object.keys(p.proof.controls).join(", ")}</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

function WriteSection({ draft, onChanged }: { draft: ListedAppTasksDraft; onChanged(): void }) {
	const { t } = useTranslation("appTasks");
	const [write, { isLoading, error, data }] = useWriteAppTasksMutation();
	const confirm = useConfirm();
	const [judge, setJudge] = useState<PickedModel | null>(draft.model ?? null);
	const judgeKey = usePickedKeyState(judge);
	const judgeOk = !draft.judged || (isPicked(judge) && judgeKey === "ok");
	const [heldOut, setHeldOut] = useState(false);
	const submit = async () => {
		const n = draft.plans.length;
		const ok = await confirm(t("write.confirm", { count: n, suite: draft.suite }), {
			title: t("write.confirmTitle"),
			confirmLabel: t("write.confirmLabel", { count: n }),
			cancelLabel: t("write.cancel"),
		});
		if (!ok) return;
		const res = await write({
			id: draft.id,
			held_out: heldOut,
			...(draft.judged && judge ? { judge: { provider: judge.provider, model: judge.model } } : {}),
		});
		if ("data" in res) onChanged();
	};
	return (
		<Section title={t("write.title")} description={t("write.description")} testId="from-api-write">
			{draft.judged && (
				<LabeledModelPicker
					label={t("write.judgeModel")}
					hint={judgeOk ? undefined : t("model.pick")}
					value={judge}
					onChange={setJudge}
					testId="from-api-judge-pick"
				/>
			)}
			<label className="flex items-center gap-2 text-sm">
				<input type="checkbox" checked={heldOut} onChange={(e) => setHeldOut(e.target.checked)} data-testid="from-api-held-out" />
				{t("write.heldOut")}
			</label>
			{error !== undefined && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="from-api-write-error">
					{getBenchErrorMessage(error)}
				</p>
			)}
			{data && <p className="text-sm">{t("write.done", { suite: data.suite })}</p>}
			{data?.readiness_job && <ReadinessLine jobId={data.readiness_job} />}
			<div>
				<Button isLoading={isLoading} disabled={!judgeOk} onClick={() => void submit()} dataTestId="from-api-write-submit">
					{t("write.submit", { count: draft.plans.length })}
				</Button>
			</div>
		</Section>
	);
}

/** After the write, the readiness check that proves the tasks on a worker, with a link to its job (D4 9 gap 1). */
export function ReadinessLine({ jobId }: { jobId: string }) {
	const { t } = useTranslation("w4int");
	return (
		<p className="text-sm" data-testid="from-api-readiness">
			{t("appTasks.proving")}{" "}
			<Link to="/jobs/$jobId" params={{ jobId }} className="text-primary underline-offset-2 hover:underline">
				{t("appTasks.openJob")}
			</Link>
		</p>
	);
}

function ReviewSection({ draft, writable, onChanged }: { draft: ListedAppTasksDraft; writable: boolean; onChanged(): void }) {
	const { t } = useTranslation("appTasks");
	const [review, { isLoading, error }] = useReviewAppTaskMutation();
	const decide = async (task: string, decision: "accepted" | "rejected") => {
		const res = await review({ id: draft.id, task, decision });
		if ("data" in res) onChanged();
	};
	return (
		<Section
			title={t("review.title")}
			description={t("review.description")}
			testId="from-api-review"
			actions={
				<Link
					to="/suites/$suiteId"
					params={{ suiteId: draft.suite }}
					search={{ tab: "build" } as never}
					className="text-sm underline"
					data-testid="from-api-open-benchmark"
				>
					{t("write.openBenchmark")}
				</Link>
			}
		>
			{error !== undefined && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="from-api-review-error">
					{getBenchErrorMessage(error)}
				</p>
			)}
			<ul className="flex flex-col gap-2">
				{draft.written.map((w) => (
					<li key={w.id} className="flex flex-wrap items-center gap-3 rounded-md border px-3 py-2" data-testid={`from-api-written-${w.id}`}>
						<span className="font-mono text-xs">{w.id}</span>
						<span className="text-sm">{w.title}</span>
						<Badge variant="outline">{t(`review.state.${w.review}`)}</Badge>
						<span className="text-muted-foreground text-xs">{w.summary}</span>
						{writable && w.review === "pending" && (
							<span className="flex gap-2">
								<Button
									size="sm"
									isLoading={isLoading}
									onClick={() => void decide(w.id, "accepted")}
									dataTestId={`from-api-accept-${w.id}`}
								>
									{t("review.accept")}
								</Button>
								<ConfirmButton
									label={t("review.reject")}
									question={t("review.rejectQuestion", { task: w.title || w.id })}
									confirmLabel={t("review.rejectConfirm")}
									cancelLabel={t("review.rejectCancel")}
									isLoading={isLoading}
									onConfirm={() => void decide(w.id, "rejected")}
									testId={`from-api-reject-${w.id}`}
								/>
							</span>
						)}
					</li>
				))}
			</ul>
		</Section>
	);
}