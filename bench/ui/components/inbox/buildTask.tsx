import { defaultJobModel, plainJobError } from "@/components/findings/jobModel";
import { GroupedModelPicker, type PickedModel } from "@/components/models/groupedModelPicker";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PaidButton } from "@/components/ui/paidButton";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import { formatUsd } from "@/lib/money";
import type { InboxSuite } from "@/lib/types/bench";
import { useEffect, useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "@tanstack/react-router";
import { useConfirm } from "@/components/platform/confirmDialog";
import {
	buildTaskBody,
	useDecideBuildTaskMutation,
	useGetBuildTaskQuery,
	useStartBuildTaskMutation,
	type BuildTaskFile,
	type BuildTaskState,
} from "./buildTaskApi";
import "./uft3I18n";

/** The cap the form starts with (handlers.TraceTaskDefaultCapUSD). */
export const BUILD_TASK_DEFAULT_CAP = 0.5;

const running = (s?: string) => s === "queued" || s === "running";

/** Round 7 lane T3: "Build a task" on a failure, and the result of the latest build. */
export function BuildTaskPanel({
	itemId,
	suites,
	disabled,
	onQuote,
}: {
	itemId: string;
	suites: readonly InboxSuite[];
	disabled?: boolean;
	/** Scrolls the session panel to a quoted call (seq n) or the turns (seq 0). */
	onQuote?: (seq: number) => void;
}) {
	const { t } = useTranslation("uft3");
	const [open, setOpen] = useState(false);
	const [polling, setPolling] = useState(false);
	const q = useGetBuildTaskQuery(itemId, { pollingInterval: polling ? 4000 : 0, skipPollingIfUnfocused: true });
	const state = q.data;
	const active = running(state?.status) || running(state?.finalize?.status);
	// Poll while the latest build is queued or running, also when the page opens on one.
	useEffect(() => {
		if (active) setPolling(true);
		else if (state?.job_id) setPolling(false);
	}, [active, state?.job_id]);
	return (
		<section className="flex flex-col gap-2" data-testid="build-task">
			<div className="flex flex-wrap items-center justify-between gap-2">
				<div className="flex min-w-0 flex-col gap-0.5">
					<h3 className="text-foreground text-sm font-semibold">{t("title")}</h3>
					<p className="text-muted-foreground text-xs">{t("hint")}</p>
				</div>
				{!open && !active && (
					<Button type="button" size="sm" variant="outline" disabled={disabled} onClick={() => setOpen(true)} dataTestId="build-task-open">
						{t("open")}
					</Button>
				)}
			</div>
			{open && (
				<BuildTaskForm
					itemId={itemId}
					suites={suites}
					onCancel={() => setOpen(false)}
					onStarted={() => {
						setOpen(false);
						setPolling(true);
						void q.refetch();
					}}
				/>
			)}
			{state?.job_id && <BuildTaskView state={state} onQuote={onQuote} />}
			{state?.job_id && !disabled && (
				<BuildTaskActions
					itemId={itemId}
					state={state}
					onChanged={() => {
						setPolling(true);
						void q.refetch();
					}}
				/>
			)}
		</section>
	);
}

function BuildTaskForm({
	itemId,
	suites,
	onCancel,
	onStarted,
}: {
	itemId: string;
	suites: readonly InboxSuite[];
	onCancel(): void;
	onStarted(): void;
}) {
	const { t } = useTranslation("uft3");
	const ids = useId();
	const models = useGetModelsQuery();
	const fallback = useMemo(() => defaultJobModel(models.data), [models.data]);
	const [picked, setPicked] = useState<PickedModel | null>(null);
	const chosen = picked ?? fallback;
	const model = chosen?.model ?? "";
	const [suite, setSuite] = useState(suites[0]?.id ?? "");
	const [cap, setCap] = useState(String(BUILD_TASK_DEFAULT_CAP));
	const [start, { isLoading }] = useStartBuildTaskMutation();
	const [error, setError] = useState("");
	const capN = Number(cap);
	if (suites.length === 0) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="build-task-no-suite">
				{t("noBenchmark")}
			</p>
		);
	}
	return (
		<div className="flex flex-col gap-2">
			<div className="flex flex-wrap items-end gap-3">
				<label className="flex flex-col gap-0.5 text-xs" htmlFor={`${ids}-suite`}>
					{t("benchmark")}
					<select
						id={`${ids}-suite`}
						className="border-input bg-background h-8 rounded-md border px-2 text-sm"
						value={suite}
						onChange={(e) => setSuite(e.target.value)}
						data-testid="build-task-suite"
					>
						{suites.map((s) => (
							<option key={s.id} value={s.id}>
								{s.id}
							</option>
						))}
					</select>
				</label>
				<GroupedModelPicker value={chosen} onChange={setPicked} label={t("model")} testId="build-task-model" />
				<label className="flex flex-col gap-0.5 text-xs" htmlFor={`${ids}-cap`}>
					{t("cap")}
					<Input
						id={`${ids}-cap`}
						className="h-8 w-24"
						type="number"
						min="0.01"
						max="5"
						step="0.01"
						value={cap}
						onChange={(e) => setCap(e.target.value)}
						data-testid="build-task-cap"
					/>
				</label>
				<PaidButton
					size="sm"
					verb={t("verb")}
					capUsd={capN > 0 ? capN : null}
					confirm="always"
					kind="form"
					confirmText={t("ask", { model, suite, cap: formatUsd(capN) })}
					disabled={isLoading || !model || !suite || !(capN > 0)}
					onConfirm={async () => {
						setError("");
						try {
							await start({ item: itemId, body: buildTaskBody(suite, model, capN, chosen?.provider ?? "") }).unwrap();
							onStarted();
						} catch (e) {
							setError(plainJobError(getBenchErrorMessage(e)));
						}
					}}
					testId="build-task-start"
				/>
				<Button type="button" size="sm" variant="ghost" onClick={onCancel} dataTestId="build-task-cancel">
					{t("cancel")}
				</Button>
			</div>
			{error && (
				<p className="text-destructive text-sm" role="alert" data-testid="build-task-error">
					{error}
				</p>
			)}
		</div>
	);
}

/** Admission's result for the control that carries a variant, or "". */
function variantGot(r: { admission?: { controls: { name: string; got: string }[] } }, control?: string): string {
	if (!control) return "";
	return r.admission?.controls.find((c) => c.name === control)?.got ?? "";
}

const isInstruction = (f: BuildTaskFile) => /(^|\/)instruction\.md$/.test(f.path);
const isCheck = (f: BuildTaskFile) => /^(tests|verifier)\//.test(f.path) || /check/i.test(f.path);

/** The state of the latest build: running, failed, or the result with admission's verdict. */
export function BuildTaskView({ state, onQuote }: { state: BuildTaskState; onQuote?: (seq: number) => void }) {
	const { t } = useTranslation("uft3");
	if (running(state.status)) {
		return (
			<p className="text-muted-foreground text-sm" role="status" data-testid="build-task-running">
				{state.status === "queued" ? t("queued") : state.step ? t("running", { step: state.step }) : t("runningNoStep")}
			</p>
		);
	}
	const r = state.result;
	if (!r) {
		return state.error ? (
			<p className="text-destructive text-sm" role="alert" data-testid="build-task-failed">
				{t("failed", { error: plainJobError(state.error) })}
			</p>
		) : null;
	}
	const instruction = r.files.filter(isInstruction);
	const checks = r.files.filter((f) => !isInstruction(f) && isCheck(f));
	const rest = r.files.filter((f) => !isInstruction(f) && !isCheck(f));
	const failing = state.failing ?? [];
	const kindLabel = t(`kind.${r.failure.kind}`, { defaultValue: r.failure.kind });
	return (
		<div className="flex max-w-3xl flex-col gap-3" data-testid="build-task-result">
			<Verdict state={state} />
			<div className="flex flex-col gap-1">
				<h4 className="text-sm font-medium">{t("wrong")}</h4>
				<p className="text-sm">
					<span className="font-medium" data-testid="build-task-kind">
						{kindLabel}
					</span>
					{": "}
					{r.failure.summary}
				</p>
				<ul className="flex flex-col gap-1">
					{r.failure.quotes.map((q, i) => (
						<li key={i} className="border-border border-l-2 pl-2 text-sm" data-testid="build-task-quote">
							{onQuote ? (
								<button
									type="button"
									className="text-muted-foreground mr-2 text-xs hover:underline"
									title={t("quoteOpen")}
									onClick={() => onQuote(q.seq)}
									data-testid="build-task-quote-open"
								>
									{q.seq > 0 ? t("quoteSeq", { seq: q.seq }) : t("quoteAnswer")}
								</button>
							) : (
								<span className="text-muted-foreground mr-2 text-xs">{q.seq > 0 ? t("quoteSeq", { seq: q.seq }) : t("quoteAnswer")}</span>
							)}
							<q>{q.text}</q>
						</li>
					))}
				</ul>
				{(r.failure.dropped_quotes ?? 0) > 0 && (
					<p className="text-muted-foreground text-xs">{t("dropped", { count: r.failure.dropped_quotes })}</p>
				)}
			</div>
			<p className="text-sm">
				<span className="font-medium">{t("shape", { shape: r.shape })}</span>. {r.shape_reason}
			</p>
			{instruction.map((f) => (
				<FileBlock key={f.path} title={t("instruction")} file={f} />
			))}
			{checks.map((f) => (
				<FileBlock key={f.path} title={t("checks")} file={f} />
			))}
			{rest.length > 0 && (
				<details className="text-sm">
					<summary className="cursor-pointer">{t("otherFiles", { count: rest.length })}</summary>
					{rest.map((f) => (
						<FileBlock key={f.path} title={f.path} file={f} />
					))}
				</details>
			)}
			{r.mutants.length > 0 && (
				<div className="flex flex-col gap-1">
					<h4 className="text-sm font-medium">{t("variants")}</h4>
					<ul className="list-disc pl-5 text-sm">
						{r.mutants.map((m) => (
							<li key={m.name}>
								<span className="font-mono text-xs">{m.name}</span>: {m.what_it_does_wrong}
								{variantGot(r, m.control) && (
									<span className="text-muted-foreground ml-1 text-xs" data-testid={`build-task-variant-${m.name}`}>
										({t("got")} {variantGot(r, m.control)})
									</span>
								)}
							</li>
						))}
					</ul>
				</div>
			)}
			{r.standin && <p className="text-sm">{t("standin", { why: r.standin.why })}</p>}
			{r.admission?.status === "not_run" && (
				<p className="text-muted-foreground text-sm" data-testid="build-task-not-run">
					{t("notRun", { reason: r.admission.reason ?? "" })}
				</p>
			)}
			{r.admission && r.admission.status !== "not_run" && (
				<div className="flex flex-col gap-1">
					<h4 className="text-sm font-medium">{t("admission")}</h4>
					<table className="w-full max-w-lg text-sm" data-testid="build-task-admission">
						<thead>
							<tr className="text-muted-foreground text-left text-xs">
								<th className="py-1 font-normal">{t("control")}</th>
								<th className="py-1 font-normal">{t("want")}</th>
								<th className="py-1 font-normal">{t("got")}</th>
							</tr>
						</thead>
						<tbody>
							{[...r.admission.controls, ...r.admission.guards].map((c) => {
								const bad = c.want ? c.got !== c.want : c.got !== "pass" && c.name !== "guard_instruction_clear";
								return (
									<tr key={c.name} className="border-border h-8 border-t" data-testid={`build-task-control-${c.name}`}>
										<td className="font-mono text-xs">{c.name}</td>
										<td>{c.want ?? "pass"}</td>
										<td className={bad ? "text-destructive font-medium" : undefined}>{c.got}</td>
									</tr>
								);
							})}
						</tbody>
					</table>
				</div>
			)}
			{(r.notes ?? []).length > 0 && (
				<details className="text-sm">
					<summary className="cursor-pointer">{t("notes")}</summary>
					<ul className="list-disc pl-5">
						{(r.notes ?? []).map((n, i) => (
							<li key={i}>{n}</li>
						))}
					</ul>
				</details>
			)}
			<p className="text-muted-foreground text-xs tabular-nums" data-testid="build-task-cost">
				{t("cost", { usd: formatUsd(r.spent_usd), calls: r.model_calls })}
			</p>
			{failing.length === 0 && !state.can_approve && !state.decision && state.blocked && (
				<p className="text-muted-foreground text-xs">{t("blocked", { reason: state.blocked })}</p>
			)}
		</div>
	);
}

function Verdict({ state }: { state: BuildTaskState }) {
	const { t } = useTranslation("uft3");
	const d = state.decision;
	const f = state.finalize;
	const suite = state.result?.task?.suite ?? "";
	if (d?.decision === "approved" && f) {
		if (running(f.status)) {
			return (
				<p className="text-sm font-medium" role="status" data-testid="build-task-checking">
					{f.step ? t("checkingStep", { step: f.step }) : t("checking")}
				</p>
			);
		}
		if (f.admitted) {
			return (
				<p className="text-sm font-medium" role="status" data-testid="build-task-admitted">
					{t("readyIn", { suite })}{" "}
					<Link
						to="/suites/$suiteId"
						params={{ suiteId: suite }}
						search={{ tab: "edit" } as never}
						className="hover:underline"
						data-testid="build-task-open-task"
					>
						{t("openTask")}
					</Link>
				</p>
			);
		}
		if (f.error && (f.failing ?? []).length === 0) {
			return (
				<p className="text-destructive text-sm font-medium" role="alert" data-testid="build-task-final-failed">
					{t("finalFailed", { error: plainJobError(f.error) })}
				</p>
			);
		}
		return (
			<p className="text-destructive text-sm font-medium" role="status" data-testid="build-task-final-refused">
				{t("finalRefused", { failing: (f.failing ?? []).join("; ") })}
			</p>
		);
	}
	if (d) {
		return (
			<p className="text-sm font-medium" role="status" data-testid="build-task-decision">
				{d.decision === "approved" ? t("approved", { by: d.by, suite: state.result?.task?.suite ?? "" }) : t("rejected", { by: d.by })}
			</p>
		);
	}
	if ((state.failing ?? []).length > 0) {
		return (
			<p className="text-destructive text-sm font-medium" role="status" data-testid="build-task-refused">
				{t("refused", { failing: (state.failing ?? []).join("; ") })}
			</p>
		);
	}
	if (state.can_approve) {
		return (
			<p className="text-sm font-medium" role="status" data-testid="build-task-ready">
				{t("ready")}
			</p>
		);
	}
	return null;
}

function FileBlock({ title, file }: { title: string; file: BuildTaskFile }) {
	return (
		<div className="flex flex-col gap-1">
			<h4 className="text-sm font-medium">
				{title} <span className="text-muted-foreground font-mono text-xs">{file.path}</span>
			</h4>
			<pre className="bg-muted max-h-64 overflow-auto rounded-sm p-2 text-xs whitespace-pre-wrap">{file.content}</pre>
		</div>
	);
}

/**
 * Approve, Reject and Try again. Approve shows only when the server offers it (every control and
 * guard holds and the caller may edit the benchmark); the server checks both again.
 */
export function BuildTaskActions({ itemId, state, onChanged }: { itemId: string; state: BuildTaskState; onChanged(): void }) {
	const { t } = useTranslation("uft3");
	const confirm = useConfirm();
	const [decide, { isLoading }] = useDecideBuildTaskMutation();
	const [start, retrying] = useStartBuildTaskMutation();
	const [error, setError] = useState("");
	const r = state.result;
	if (!r || !state.job_id || (state.decision && !state.can_retry)) return null;
	const jobId = state.job_id;
	const suite = r.task?.suite ?? "";
	const left = Math.max(0, (state.budget_usd ?? 0) - r.spent_usd);
	const run = async (fn: () => Promise<unknown>) => {
		setError("");
		try {
			await fn();
			onChanged();
		} catch (e) {
			setError(plainJobError(getBenchErrorMessage(e)));
		}
	};
	return (
		<div className="flex flex-col gap-2" data-testid="build-task-actions">
			{state.can_approve && <p className="text-sm">{t("approveMeans", { suite })}</p>}
			<div className="flex flex-wrap items-center gap-2">
				{state.can_approve && (
					<Button
						type="button"
						size="sm"
						disabled={isLoading}
						onClick={() => void run(() => decide({ item: itemId, jobId, decision: "approve" }).unwrap())}
						dataTestId="build-task-approve"
					>
						{t("approve")}
					</Button>
				)}
				{state.can_retry && (
					<PaidButton
						size="sm"
						variant="outline"
						verb={t("retry")}
						capUsd={left}
						kind="retry"
						confirmText={t("retryAsk")}
						disabled={retrying.isLoading}
						onConfirm={() => run(() => start({ item: itemId, body: { suite, model: "", budget_usd: 0, retry: true } }).unwrap())}
						testId="build-task-retry"
					/>
				)}
				{!state.decision && (
					<Button
						type="button"
						size="sm"
						variant="ghost"
						disabled={isLoading}
						onClick={async () => {
							if (!(await confirm(t("rejectAsk"), { title: t("reject"), confirmLabel: t("reject"), destructive: true }))) return;
							await run(() => decide({ item: itemId, jobId, decision: "reject" }).unwrap());
						}}
						dataTestId="build-task-reject"
					>
						{t("reject")}
					</Button>
				)}
			</div>
			{error && (
				<p className="text-destructive text-sm" role="alert" data-testid="build-task-decide-error">
					{error}
				</p>
			)}
		</div>
	);
}