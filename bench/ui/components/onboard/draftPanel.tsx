import { ConfirmButton } from "@/components/platform/badges";
import { useConfirm } from "@/components/platform/confirmDialog";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { WhyDisabled } from "@/components/ui/whyDisabled";
import { w4m5Text } from "@/components/onboard/w4m5I18n";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useEditOnboardTaskMutation, usePublishOnboardDraftMutation, useStartOnboardReadinessMutation } from "@/lib/store/apis/onboardApi";
import type { OnboardCheck, OnboardDraftSet, OnboardDraftTask } from "@/lib/types/onboard";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, CheckCircle2, ChevronRight, MinusCircle, XCircle } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

type TX = (k: string, o?: Record<string, unknown>) => string;

function useTx(): TX {
	const { t } = useTranslation("onboard");
	return (k, o) => t(k as never, o as never) as unknown as string;
}

/** The matrix column a readiness check belongs to. */
export function checkGroup(name: string): string {
	if (/^reference(_|$)|^oracle_control$|^honest_original/.test(name)) return "reference";
	if (/^empty/.test(name)) return "empty";
	if (/^(mutant|wrong_answer|honest_mutated|generated_wrong)/.test(name)) return "wrong";
	if (/^exploit/.test(name)) return "exploit";
	if (name === "static_validation") return "static";
	if (name === "noop_control") return "noop";
	return name;
}

const GROUP_ORDER = ["static", "reference", "empty", "wrong", "exploit", "noop"];

/** Columns of the task by check matrix: the known groups first, then any other check. */
export function matrixColumns(tasks: OnboardDraftTask[]): string[] {
	const seen = new Set<string>();
	for (const t of tasks) for (const c of t.readiness?.checks ?? []) seen.add(checkGroup(c.name));
	const known = GROUP_ORDER.filter((g) => seen.has(g));
	const other = [...seen].filter((g) => !GROUP_ORDER.includes(g)).sort();
	return [...known, ...other];
}

/** The cell of one task and one column: passed only when every check of the group passed. */
export function cellState(checks: OnboardCheck[] | undefined, group: string): "pass" | "fail" | "none" {
	const mine = (checks ?? []).filter((c) => checkGroup(c.name) === group);
	if (mine.length === 0) return "none";
	return mine.every((c) => c.passed) ? "pass" : "fail";
}

/** The line of a draft with no task: why nothing was drafted, or that every task was dropped (wave 4, p10-5). */
export function emptyDraftText(tx: TX, set: Pick<OnboardDraftSet, "problems">): string {
	return (set.problems?.length ?? 0) > 0 ? tx("preview.empty") : w4m5Text("preview.nothingLeft");
}

/** The readiness error without the job layer's prefix (wave 4, p10-6). */
export function plainReadinessError(text: string): string {
	return text.replace(/^(create job: )?invalid job parameters: /, "");
}

/** Step 3: the draft tasks, each with its instruction, checks and controls. */
export function DraftPreview({ set }: { set: OnboardDraftSet }) {
	const tx = useTx();
	const [open, setOpen] = useState<string | null>(null);
	const [edit, editState] = useEditOnboardTaskMutation();
	const locked = !!set.published || set.readiness?.status === "running" || set.readiness?.status === "queued";
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="onboard-preview">
			<p className="text-sm">
				{tx(set.new_suite ? "preview.countNew" : "preview.count", { count: set.tasks.length, id: set.id, suite: set.suite })}
			</p>
			{set.public && <p className="text-muted-foreground text-xs">{tx("preview.public")}</p>}
			{set.tasks.length === 0 && <p className="text-sm">{emptyDraftText(tx, set)}</p>}
			{editState.error !== undefined && (
				<p role="alert" className="text-chart-error-ink text-xs">
					{getBenchErrorMessage(editState.error)}
				</p>
			)}
			<ul className="flex flex-col divide-y rounded-lg border">
				{set.tasks.map((t) => {
					const warn = t.gates.filter((g) => !g.ok);
					const isOpen = open === t.id;
					return (
						<li key={t.id} className="flex flex-col gap-2 px-3 py-2" data-testid={`onboard-task-${t.id}`}>
							<div className="flex flex-wrap items-center gap-2">
								<button
									type="button"
									className="flex min-w-0 flex-1 cursor-pointer items-center gap-1 text-left text-sm"
									onClick={() => setOpen(isOpen ? null : t.id)}
									aria-expanded={isOpen}
								>
									<ChevronRight className={cn("size-4 shrink-0 transition-transform", isOpen && "rotate-90")} aria-hidden="true" />
									<span className="truncate font-medium">{t.title}</span>
									<span className="text-muted-foreground truncate text-xs">{t.origin.ref}</span>
								</button>
								<Badge variant={t.review === "accepted" ? "default" : "outline"}>{tx(`preview.review.${t.review}`)}</Badge>
								{t.held_out && <Badge variant="outline">{tx("preview.heldOut")}</Badge>}
								{warn.map((g) => (
									<span key={g.gate} title={g.message} className="text-chart-warning-ink flex items-center gap-1 text-xs">
										<AlertTriangle className="size-3.5" aria-hidden="true" />
										{tx(`preview.gate.${g.gate}`)}
									</span>
								))}
								{!locked && (
									<span className="flex gap-1">
										<Button
											size="sm"
											variant="outline"
											disabled={editState.isLoading}
											onClick={() => void edit({ set: set.id, task: t.id, review: t.review === "rejected" ? "accepted" : "rejected" })}
											dataTestId={`onboard-review-${t.id}`}
										>
											{t.review === "rejected" ? tx("preview.accept") : tx("preview.reject")}
										</Button>
										<Button
											size="sm"
											variant="outline"
											disabled={editState.isLoading}
											onClick={() => void edit({ set: set.id, task: t.id, held_out: !t.held_out })}
											dataTestId={`onboard-heldout-${t.id}`}
										>
											{tx("preview.heldOut")}
										</Button>
										<ConfirmButton
											label={tx("preview.drop")}
											question={tx("preview.dropAsk", { task: t.title || t.id })}
											confirmLabel={tx("preview.dropYes")}
											cancelLabel={tx("preview.keep")}
											onConfirm={() => void edit({ set: set.id, task: t.id, drop: true })}
											disabled={editState.isLoading}
											variant="ghost"
											testId={`onboard-drop-${t.id}`}
										/>
									</span>
								)}
							</div>
							{isOpen && (
								<div className="grid gap-3 pl-5 text-xs md:grid-cols-2" data-testid={`onboard-card-${t.id}`}>
									<div className="flex min-w-0 flex-col gap-1">
										<span className="font-medium">{tx("preview.instruction")}</span>
										<pre className="bg-muted max-h-64 overflow-auto rounded-md p-2 whitespace-pre-wrap">{t.instruction}</pre>
									</div>
									<div className="flex min-w-0 flex-col gap-2">
										<span className="font-medium">{tx("preview.checks")}</span>
										<ul className="list-disc pl-4">
											{t.checks.map((c) => (
												<li key={c}>{c}</li>
											))}
										</ul>
										<span className="font-medium">{tx("preview.controls")}</span>
										<ul className="flex flex-wrap gap-1">
											{t.controls.map((c) => (
												<li key={c.path}>
													<Badge variant="outline" title={c.path}>
														{tx(`preview.control.${c.kind}`)}: {tx(`preview.origin.${c.origin}`)}
													</Badge>
												</li>
											))}
										</ul>
										{warn.map((g) => (
											<p key={g.gate} className="text-chart-warning-ink">
												{tx(`preview.gate.${g.gate}`)}: {g.message}
											</p>
										))}
									</div>
								</div>
							)}
						</li>
					);
				})}
			</ul>
			{set.problems.length > 0 && (
				<details className="text-xs" data-testid="onboard-problems">
					<summary className="cursor-pointer">
						{tx("preview.problems")} ({set.problems.length})
					</summary>
					<ul className="mt-1 flex flex-col gap-0.5 pl-4">
						{set.problems.map((p) => (
							<li key={p.ref + p.message}>
								{p.ref}: {p.message}
							</li>
						))}
					</ul>
				</details>
			)}
			{(set.notes?.length ?? 0) > 0 && (
				<ul className="text-muted-foreground flex flex-col gap-0.5 text-xs">
					{set.notes?.map((n) => (
						<li key={n}>{n}</li>
					))}
				</ul>
			)}
		</div>
	);
}

function Cell({ state, tx }: { state: "pass" | "fail" | "none"; tx: TX }) {
	if (state === "pass") return <CheckCircle2 className="text-chart-success-ink size-4" aria-label="pass" />;
	if (state === "fail") return <XCircle className="text-chart-error-ink size-4" aria-label="fail" />;
	return <span className="text-muted-foreground text-[11px]">{tx("ready.notRunCell")}</span>;
}

/** Step 4: start the readiness checks and watch the task by check matrix fill in. */
export function ReadinessPanel({ set }: { set: OnboardDraftSet }) {
	const tx = useTx();
	const confirm = useConfirm();
	const [start, startState] = useStartOnboardReadinessMutation();
	const r = set.readiness;
	const running = r?.status === "queued" || r?.status === "running";
	const cols = matrixColumns(set.tasks);
	// p10-6: a judge-graded draft needs the consent the readiness job asks for.
	const judged = set.grader === "judge";
	const [consent, setConsent] = useState(false);
	const why = judged && !consent ? w4m5Text("ready.needConsent") : null;
	const label = (g: string) => (GROUP_ORDER.includes(g) ? tx(`ready.check.${g}`) : tx("ready.check.other", { name: g }));
	const run = async () => {
		if (!(await confirm(tx("ready.confirm", { count: set.tasks.length })))) return;
		void start(judged ? { set: set.id, judge_consent: true } : { set: set.id });
	};
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="onboard-readiness">
			<div className="flex flex-wrap items-center gap-3">
				{!set.published && (
					<Button
						onClick={() => void run()}
						disabled={running || startState.isLoading || set.tasks.length === 0 || why !== null}
						aria-describedby={why ? "onboard-ready-why" : undefined}
						isLoading={startState.isLoading}
						dataTestId="onboard-ready-run"
					>
						{r ? tx("ready.rerun") : tx("ready.run")}
					</Button>
				)}
				{r && (
					<span className="text-sm" data-testid="onboard-ready-status">
						{tx("ready.status", {
							status: tx(`ready.statusLabel.${r.status}`),
							where: r.where || "-",
							done: r.done,
							total: r.total,
							admitted: r.admitted,
						})}
					</span>
				)}
			</div>
			{judged && !set.published && (
				<div className="flex flex-col gap-1">
					<label className="flex items-start gap-2 text-sm" data-testid="onboard-ready-consent">
						<input type="checkbox" className="mt-1" checked={consent} onChange={(e) => setConsent(e.target.checked)} />
						<span>{w4m5Text("ready.consent", { provider: set.plan.judge?.provider ?? "", model: set.plan.judge?.model ?? "" })}</span>
					</label>
					<WhyDisabled reason={running || startState.isLoading ? null : why} testId="onboard-ready-why" />
				</div>
			)}
			{startState.error !== undefined && (
				<p role="alert" className="text-chart-error-ink text-xs">
					{plainReadinessError(getBenchErrorMessage(startState.error))}
				</p>
			)}
			{r?.error && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="onboard-ready-error">
					{plainReadinessError(r.error)}
				</p>
			)}
			{(r?.problems?.length ?? 0) > 0 && (
				<div className="text-xs">
					<p className="font-medium">{tx("ready.problems")}</p>
					<ul className="pl-4">
						{r?.problems?.map((p) => (
							<li key={p.ref + p.message}>
								{p.ref}: {p.message}
							</li>
						))}
					</ul>
				</div>
			)}
			{(r || set.tasks.some((t) => t.readiness)) && (
				<div className="overflow-x-auto">
					<table className="w-full min-w-[560px] text-left text-xs" data-testid="onboard-matrix">
						<caption className="text-muted-foreground mb-1 text-left">{tx("ready.matrix")}</caption>
						<thead>
							<tr className="border-b">
								<th className="py-1 pr-3 font-medium">{tx("ready.task")}</th>
								{cols.map((c) => (
									<th key={c} className="py-1 pr-3 font-medium">
										{label(c)}
									</th>
								))}
								<th className="py-1 pr-3 font-medium">{tx("ready.catch")}</th>
								<th className="py-1 pr-3 font-medium">{tx("ready.result")}</th>
							</tr>
						</thead>
						<tbody>
							{set.tasks.map((t) => (
								<tr key={t.id} className="border-b align-top last:border-0" data-testid={`onboard-matrix-${t.id}`}>
									<td className="max-w-[240px] truncate py-1 pr-3" title={t.title}>
										{t.id}
									</td>
									{cols.map((c) => (
										<td key={c} className="py-1 pr-3">
											<Cell state={cellState(t.readiness?.checks, c)} tx={tx} />
										</td>
									))}
									<td className="py-1 pr-3">{t.catch_rate ? `${t.catch_rate.caught}/${t.catch_rate.total}` : "-"}</td>
									<td className="py-1 pr-3">
										{!t.readiness ? (
											<span className="text-muted-foreground flex items-center gap-1">
												<MinusCircle className="size-3.5" aria-hidden="true" />
												{tx("ready.notRun")}
											</span>
										) : t.readiness.admitted ? (
											<span className="text-chart-success-ink font-medium">{tx("ready.isReady")}</span>
										) : (
											<span className="text-chart-error-ink" title={t.reason}>
												{tx("ready.notReady")}
												{t.reason ? `: ${t.reason}` : ""}
											</span>
										)}
									</td>
								</tr>
							))}
						</tbody>
					</table>
				</div>
			)}
		</div>
	);
}

/** The tasks a publish copies: the accepted ones, only the ready ones unless unready is set. */
function publishTasks(set: OnboardDraftSet, unready: boolean): OnboardDraftTask[] {
	const accepted = set.tasks.filter((t) => t.review === "accepted");
	return unready ? accepted : accepted.filter((t) => t.readiness?.admitted);
}

/** The publish confirmation: how many tasks go where, and how many of them are held out. */
export function publishQuestion(set: OnboardDraftSet, unready: boolean, tx: TX): string {
	const tasks = publishTasks(set, unready);
	const q = tx("publish.confirm", { count: tasks.length, suite: set.suite });
	const held = tasks.filter((t) => t.held_out).length;
	return held > 0 ? `${q} ${w4m5Text("publish.heldOut", { count: held })}` : q;
}

/** Step 5: publish the ready, accepted tasks privately; then open the benchmark or evaluate. */
export function PublishPanel({ set }: { set: OnboardDraftSet }) {
	const tx = useTx();
	const confirm = useConfirm();
	const [unready, setUnready] = useState(false);
	const [publish, state] = usePublishOnboardDraftMutation();
	const accepted = set.tasks.filter((t) => t.review === "accepted");
	const ready = accepted.filter((t) => t.readiness?.admitted);
	const count = unready ? accepted.length : ready.length;
	const p = set.published;
	if (p) {
		return (
			<div className="flex min-w-0 flex-col gap-2 text-sm" data-testid="onboard-published">
				<p className="font-medium">{tx("publish.done", { count: p.tasks.length, suite: p.suite })}</p>
				{p.held_out > 0 && (
					<p>
						{set.plan.held_out?.pct
							? tx("publish.heldOutPlan", { count: p.held_out, pct: set.plan.held_out.pct })
							: tx("publish.heldOut", { count: p.held_out })}
					</p>
				)}
				{!p.job_id && <p className="text-muted-foreground">{tx("publish.working")}</p>}
				{(p.recheck?.length ?? 0) > 0 && <p className="text-chart-warning-ink">{tx("publish.recheck", { count: p.recheck?.length })}</p>}
				<div className="flex flex-wrap gap-2">
					{(p.recheck?.length ?? 0) > 0 && (
						<Link
							to="/suites/$suiteId"
							params={{ suiteId: p.suite }}
							search={{ tab: "build" } as never}
							className={buttonVariants({ size: "sm" })}
							data-testid="onboard-run-readiness"
						>
							{tx("publish.runReadiness")}
						</Link>
					)}
					<Link
						to="/suites/$suiteId"
						params={{ suiteId: p.suite }}
						className={buttonVariants({ variant: "outline", size: "sm" })}
						data-testid="onboard-open-suite"
					>
						{tx("publish.open")}
					</Link>
					<Link
						to="/evaluate"
						search={{ suite: p.suite } as never}
						className={buttonVariants({ size: "sm" })}
						data-testid="onboard-evaluate"
					>
						{tx("publish.evaluate")}
					</Link>
				</div>
			</div>
		);
	}
	const run = async () => {
		if (!(await confirm(publishQuestion(set, unready, tx)))) return;
		void publish({ set: set.id, include_unready: unready || undefined });
	};
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="onboard-publish">
			<p className="text-sm">{tx("publish.count", { ready: ready.length, total: set.tasks.length })}</p>
			<label className="flex items-start gap-2 text-xs">
				<input type="checkbox" checked={unready} onChange={(e) => setUnready(e.target.checked)} data-testid="onboard-publish-unready" />
				{tx("publish.unready")}
			</label>
			<div>
				<Button
					onClick={() => void run()}
					disabled={count === 0 || state.isLoading}
					isLoading={state.isLoading}
					dataTestId="onboard-publish-run"
				>
					{tx("publish.run")}
				</Button>
			</div>
			{state.error !== undefined && (
				<p role="alert" className="text-chart-error-ink text-xs">
					{getBenchErrorMessage(state.error)}
				</p>
			)}
		</div>
	);
}