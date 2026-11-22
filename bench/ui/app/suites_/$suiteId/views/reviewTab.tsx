import { JobRunButton } from "@/components/jobs/jobRunButton";
import { useConfirm } from "@/components/platform/confirmDialog";
import { Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	useGetGeneratedTasksQuery,
	useLazyGetSuiteQualityQuery,
	useSetTaskReviewMutation,
	useSplitHeldOutMutation,
} from "@/lib/store/apis/authoringApi";
import {
	ASSIST_KIND,
	ASSIST_OPS,
	type AssistOp,
	type AssistResult,
	type GeneratedTask,
	type HeldOutSplitResult,
	type QualityReport,
} from "@/lib/types/authoring";
import type { Job, SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		authoring: typeof import("@/locales/en/authoring.json");
	}
}

type T = ReturnType<typeof useTranslation<"authoring">>["t"];

const selectClass = "border-input bg-background h-9 rounded-md border px-2 text-sm";

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
	return (
		<label className="flex min-w-0 flex-col gap-1 text-sm">
			<span className="text-muted-foreground">{label}</span>
			{children}
			{hint && <span className="text-muted-foreground text-xs">{hint}</span>}
		</label>
	);
}

function ErrorLine({ error, testId }: { error: unknown; testId: string }) {
	if (!error) return null;
	return (
		<p role="alert" className="text-chart-error-ink text-xs" data-testid={testId}>
			{getBenchErrorMessage(error as never)}
		</p>
	);
}

const usd = (v: number | undefined) => (v ?? 0).toFixed(4);
const pct = (v: number) => Math.round(v * 100);

/** The assist job's params from the form; null while the model is missing. */
export function assistParams(suite: string, ops: AssistOp[], provider: string, model: string, budget: number, estimate: boolean) {
	if (!provider.trim() || !model.trim() || ops.length === 0) return null;
	return {
		suite,
		ops,
		provider: provider.trim(),
		model: model.trim(),
		budget_usd: budget > 0 ? budget : undefined,
		...(estimate ? { estimate: true } : { yes: true }),
	};
}

/** The rejection note is required; accepting needs none. */
export const canDecide = (decision: "accepted" | "rejected", note: string) => decision === "accepted" || note.trim().length > 0;

/**
 * Review tab of a team's own benchmark (lane Z19): draft rubric criteria, wrong answers and a
 * gaming attempt with a model on the team's key, review each generated task (it counts nowhere
 * until accepted), check for duplicates and public overlap, and hold out tasks automatically.
 */
export function ReviewTab({ suite }: { suite: SuiteSummary }) {
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="suite-review-tab">
			<DraftSection suite={suite} />
			<QueueSection suite={suite} />
			<QualitySection suite={suite} />
			<SplitSection suite={suite} />
		</div>
	);
}

function DraftSection({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("authoring");
	const answer = suite.output === "answer";
	const judged = suite.grader === "judge";
	const [ops, setOps] = useState<AssistOp[]>(
		judged ? ["rubric", "criterion_wrong", "near_miss_wrong", "exploit"] : ["near_miss_wrong", "exploit"],
	);
	const [provider, setProvider] = useState("");
	const [model, setModel] = useState("");
	const [budget, setBudget] = useState(1);
	const [result, setResult] = useState<AssistResult | null>(null);
	const onDone = useCallback((job: Job) => setResult((job.result as unknown as AssistResult) ?? null), []);
	if (!answer) {
		return (
			<Section title={t("draft.title")} description={t("draft.description")} testId="suite-review-draft">
				<p className="text-muted-foreground text-sm" data-testid="suite-review-draft-answer-only">
					{t("draft.answerOnly")}
				</p>
			</Section>
		);
	}
	const estimate = assistParams(suite.id, ops, provider, model, budget, true);
	const run = assistParams(suite.id, ops, provider, model, budget, false);
	const toggle = (op: AssistOp) =>
		setOps((cur) => (cur.includes(op) ? cur.filter((o) => o !== op) : ASSIST_OPS.filter((o) => o === op || cur.includes(o))));
	return (
		<Section title={t("draft.title")} description={t("draft.description")} testId="suite-review-draft">
			<fieldset className="flex flex-col gap-2">
				<legend className="text-muted-foreground mb-1 text-sm">{t("draft.ops")}</legend>
				{ASSIST_OPS.map((op) => {
					const off = !judged && (op === "rubric" || op === "criterion_wrong");
					return (
						<label key={op} className={cn("flex items-start gap-2 text-sm", off && "opacity-50")}>
							<input
								type="checkbox"
								className="mt-1"
								checked={ops.includes(op) && !off}
								disabled={off}
								onChange={() => toggle(op)}
								data-testid={`suite-review-op-${op}`}
							/>
							<span className="flex flex-col">
								<span>{t(`draft.op.${op}`)}</span>
								<span className="text-muted-foreground text-xs">{t(`draft.opHint.${op}`)}</span>
							</span>
						</label>
					);
				})}
			</fieldset>
			<div className="grid gap-3 sm:grid-cols-3">
				<Field label={t("draft.provider")}>
					<Input value={provider} onChange={(e) => setProvider(e.target.value)} placeholder="openai" data-testid="suite-review-provider" />
				</Field>
				<Field label={t("draft.model")} hint={t("draft.modelHint")}>
					<Input value={model} onChange={(e) => setModel(e.target.value)} placeholder="gpt-4.1-mini" data-testid="suite-review-model" />
				</Field>
				<Field label={t("draft.budget")} hint={t("draft.budgetHint")}>
					<Input
						type="number"
						min={0.05}
						step={0.05}
						value={budget}
						onChange={(e) => setBudget(Number(e.target.value))}
						data-testid="suite-review-budget"
					/>
				</Field>
			</div>
			{estimate && run ? (
				<div className="flex flex-wrap items-start gap-3">
					<JobRunButton kind={ASSIST_KIND} params={estimate} label={t("draft.estimate")} variant="outline" onDone={onDone} />
					<JobRunButton
						kind={ASSIST_KIND}
						params={run}
						label={t("draft.run")}
						confirm={t("draft.confirm", { model: `${provider.trim()}/${model.trim()}`, budget: budget.toFixed(2) })}
						onDone={onDone}
					/>
				</div>
			) : (
				<p className="text-muted-foreground text-xs" data-testid="suite-review-need-model">
					{t("draft.needModel")}
				</p>
			)}
			{result && <AssistSummary result={result} t={t} />}
		</Section>
	);
}

function AssistSummary({ result, t }: { result: AssistResult; t: T }) {
	return (
		<div className="bg-muted/40 flex flex-col gap-1 rounded-md p-3 text-sm" data-testid="suite-review-assist-result">
			{result.estimate ? (
				<span>{t("draft.forecast", { calls: result.calls, usd: usd(result.forecast_usd) })}</span>
			) : (
				<span>{t("draft.spent", { usd: usd(result.spent_usd), budget: result.budget_usd.toFixed(2) })}</span>
			)}
			<span className="text-muted-foreground">{result.next}</span>
		</div>
	);
}

function QueueSection({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("authoring");
	const { t: tp } = useTranslation("policyDocs");
	const q = useGetGeneratedTasksQuery(suite.id);
	return (
		<Section title={t("queue.title")} description={t("queue.description")} testId="suite-review-queue">
			{q.isError ? (
				<p role="alert" className="text-chart-error-ink text-sm">
					{t("queue.loadError")}: {getBenchErrorMessage(q.error as never)}
				</p>
			) : !q.data || q.data.tasks.length === 0 ? (
				<p className="text-muted-foreground text-sm" data-testid="suite-review-queue-empty">
					{tp("reviewEmpty")}{" "}
					<Link
						to="/suites/$suiteId"
						params={{ suiteId: suite.id }}
						search={{ tab: "policy" } as never}
						className="text-primary underline"
						data-testid="suite-review-queue-policy-link"
					>
						{tp("reviewEmptyLink")}
					</Link>
				</p>
			) : (
				<>
					<p className="text-sm" data-testid="suite-review-counts">
						{t("queue.counts", { pending: q.data.pending, accepted: q.data.accepted, rejected: q.data.rejected })}
					</p>
					<ul className="flex flex-col gap-3">
						{q.data.tasks.map((g) => (
							<GeneratedRow key={g.task} suite={suite.id} task={g} t={t} />
						))}
					</ul>
				</>
			)}
		</Section>
	);
}

const stateTone: Record<string, string> = {
	pending: "border-chart-warning/50 bg-chart-warning/10",
	accepted: "border-chart-success/40 bg-chart-success/10",
	rejected: "border-chart-error/40 bg-chart-error/10",
};

function GeneratedRow({ suite, task, t }: { suite: string; task: GeneratedTask; t: T }) {
	const session = useSession();
	const member = canUse(session, "member");
	const [note, setNote] = useState("");
	const [decide, { isLoading, error }] = useSetTaskReviewMutation();
	const send = (decision: "accepted" | "rejected") => void decide({ suite, task: task.task, decision, note: note.trim() || undefined });
	return (
		<li className="bg-card flex min-w-0 flex-col gap-2 rounded-lg border p-3" data-testid={`suite-review-task-${task.task}`}>
			<div className="flex flex-wrap items-center gap-2">
				<span className="font-mono text-sm">{task.task}</span>
				{task.title && <span className="text-muted-foreground truncate text-sm">{task.title}</span>}
				<span
					className={cn("rounded-full border px-2 py-0.5 text-xs", stateTone[task.review])}
					data-testid={`suite-review-state-${task.task}`}
				>
					{t(`queue.state.${task.review}`)}
				</span>
				{task.stale && <span className="text-muted-foreground text-xs">{t("queue.stale")}</span>}
				<span className="text-muted-foreground ml-auto text-xs">{t("queue.cost", { usd: usd(task.cost_usd) })}</span>
			</div>
			<div className="flex flex-wrap gap-1" aria-label={t("queue.fields")}>
				{task.generated.map((f) => (
					<span key={f.field} className="bg-muted rounded px-1.5 py-0.5 font-mono text-[11px]" title={`${f.op} ${f.model ?? ""}`}>
						{f.field}
					</span>
				))}
			</div>
			{task.decision && (
				<p className="text-muted-foreground text-xs" title={task.decision.by || undefined}>
					{t("queue.decidedBy", { decision: t(`queue.state.${task.decision.decision}`), by: task.decision.by_name || task.decision.by || "?" })}
					{task.decision.note ? `: ${task.decision.note}` : ""}
				</p>
			)}
			{member && (
				<div className="flex flex-wrap items-end gap-2">
					<Input
						className="max-w-md"
						value={note}
						onChange={(e) => setNote(e.target.value)}
						placeholder={t("queue.note")}
						data-testid={`suite-review-note-${task.task}`}
					/>
					<Button
						size="sm"
						disabled={isLoading || task.review === "accepted"}
						onClick={() => send("accepted")}
						dataTestId={`suite-review-accept-${task.task}`}
					>
						{t("queue.accept")}
					</Button>
					<Button
						size="sm"
						variant="outline"
						disabled={isLoading || !canDecide("rejected", note) || task.review === "rejected"}
						onClick={() => send("rejected")}
						dataTestId={`suite-review-reject-${task.task}`}
					>
						{t("queue.reject")}
					</Button>
				</div>
			)}
			<ErrorLine error={error} testId={`suite-review-error-${task.task}`} />
		</li>
	);
}

function QualitySection({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("authoring");
	const [check, q] = useLazyGetSuiteQualityQuery();
	return (
		<Section
			title={t("quality.title")}
			description={t("quality.description")}
			testId="suite-review-quality"
			actions={
				<Button
					size="sm"
					variant="outline"
					isLoading={q.isFetching}
					onClick={() => void check(suite.id)}
					dataTestId="suite-review-quality-run"
				>
					{t("quality.run")}
				</Button>
			}
		>
			<ErrorLine error={q.error} testId="suite-review-quality-error" />
			{q.data && <QualityBody report={q.data} t={t} />}
		</Section>
	);
}

export function QualityBody({ report, t }: { report: QualityReport; t: T }) {
	const dups = report.duplicates ?? [];
	const hits = report.contamination ?? [];
	if (dups.length === 0 && hits.length === 0) {
		return (
			<p className="text-sm" data-testid="suite-review-quality-clean">
				{t("quality.clean", { count: report.library_tasks })}
			</p>
		);
	}
	return (
		<div className="flex flex-col gap-3 text-sm" data-testid="suite-review-quality-body">
			{dups.length > 0 && (
				<div>
					<h4 className="mb-1 font-medium">{t("quality.duplicates")}</h4>
					<ul className="flex flex-col gap-1">
						{dups.map((d) => (
							<li key={`${d.a}|${d.b}`} className="font-mono text-xs">
								{d.a} , {d.b}{" "}
								<span className="text-muted-foreground font-sans">
									({d.kind === "exact" ? t("quality.exact") : t("quality.near", { pct: pct(d.jaccard) })})
								</span>
							</li>
						))}
					</ul>
				</div>
			)}
			{hits.length > 0 && (
				<div>
					<h4 className="mb-1 font-medium">{t("quality.public")}</h4>
					<ul className="flex flex-col gap-1">
						{hits.map((h) => (
							<li key={h.task} className={cn("text-xs", h.severity === "error" && "text-chart-error-ink")}>
								<span className="font-mono">{h.task}</span> {t("quality.overlap", { pct: pct(h.overlap), matches: h.matches.join(", ") })}
								{h.severity === "error" && ` (${t("quality.heldOutError")})`}
							</li>
						))}
					</ul>
				</div>
			)}
		</div>
	);
}

function SplitSection({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("authoring");
	const session = useSession();
	const askConfirm = useConfirm();
	const [share, setShare] = useState(25);
	const [seed, setSeed] = useState(1);
	const [stratify, setStratify] = useState("");
	const [confirmPublic, setConfirmPublic] = useState(false);
	const [result, setResult] = useState<HeldOutSplitResult | null>(null);
	const [split, { isLoading, error, reset }] = useSplitHeldOutMutation();
	if (!canUse(session, "member")) return null;
	const plan = { suite: suite.id, held_out_pct: share, seed, stratify_by: stratify || undefined, confirm: confirmPublic || undefined };
	const go = async (dry: boolean) => {
		reset();
		if (!dry && result && !(await askConfirm(t("split.applyConfirm", { count: result.held_out })))) return;
		const res = await split({ ...plan, dry_run: dry });
		if ("data" in res && res.data) setResult(res.data);
		else if ("error" in res && res.error && typeof res.error === "object" && "data" in res.error) {
			const body = (res.error as { data?: unknown }).data as HeldOutSplitResult | undefined;
			if (body && Array.isArray(body.refused)) setResult(body);
		}
	};
	return (
		<Section title={t("split.title")} description={t("split.description")} testId="suite-review-split">
			<div className="grid gap-3 sm:grid-cols-3">
				<Field label={t("split.pct")}>
					<Input
						type="number"
						min={1}
						max={90}
						value={share}
						onChange={(e) => setShare(Number(e.target.value))}
						data-testid="suite-review-split-pct"
					/>
				</Field>
				<Field label={t("split.seed")}>
					<Input type="number" value={seed} onChange={(e) => setSeed(Number(e.target.value))} data-testid="suite-review-split-seed" />
				</Field>
				<Field label={t("split.stratify")}>
					<select
						className={selectClass}
						value={stratify}
						onChange={(e) => setStratify(e.target.value)}
						data-testid="suite-review-split-stratify"
					>
						<option value="">{t("split.stratifyNone")}</option>
						{(["difficulty", "kind", "project", "capability"] as const).map((k) => (
							<option key={k} value={k}>
								{t(`split.stratifyOpt.${k}`)}
							</option>
						))}
					</select>
				</Field>
			</div>
			<label className="flex items-center gap-2 text-sm">
				<input
					type="checkbox"
					checked={confirmPublic}
					onChange={(e) => setConfirmPublic(e.target.checked)}
					data-testid="suite-review-split-confirm"
				/>
				{t("split.confirmPublic")}
			</label>
			<div className="flex flex-wrap gap-2">
				<Button size="sm" variant="outline" isLoading={isLoading} onClick={() => void go(true)} dataTestId="suite-review-split-preview">
					{t("split.preview")}
				</Button>
				<Button
					size="sm"
					disabled={isLoading || !result || result.held_out === 0 || (result.refused?.length ?? 0) > 0}
					onClick={() => void go(false)}
					dataTestId="suite-review-split-apply"
				>
					{t("split.apply")}
				</Button>
			</div>
			{result && (
				<div className="flex flex-col gap-1 text-sm" data-testid="suite-review-split-result">
					<span>{t("split.result", { held: result.held_out, public: result.public })}</span>
					{(result.refused?.length ?? 0) > 0 && (
						<span className="text-chart-warning-ink">{t("split.refused", { tasks: result.refused!.join(", ") })}</span>
					)}
					<span className="text-muted-foreground">{result.next}</span>
				</div>
			)}
			{!(error && typeof error === "object" && "status" in error && error.status === 409) && (
				<ErrorLine error={error} testId="suite-review-split-error" />
			)}
		</Section>
	);
}

export default ReviewTab;