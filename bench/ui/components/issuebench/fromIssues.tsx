// Build tab source "From issues" (lane G06): name any GitHub repository the connected account
// can read (public ones included), scan it for merged pull requests that close an issue, then
// build, check and review each one.
import { PastIssueRuns } from "./pastIssueRuns";
import { WeeklySync } from "@/components/issuebench/weeklySync";
import { JobLogPanel } from "@/components/jobs/jobLogPanel";
import { Button } from "@/components/ui/button";
import { WhyDisabled } from "@/components/ui/whyDisabled";
import { isPicked, LabeledModelPicker, usePickedKeyState } from "@/components/models/labeledModelPicker";
import type { PickedModel } from "@/components/models/groupedModelPicker";
import "@/components/suites/uf6aI18n";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetGithubStatusQuery, useListGithubReposQuery } from "@/lib/store/apis/githubApi";
import {
	useGetIssueReadinessQuery,
	useGetIssueRunQuery,
	useRetryIssueMutation,
	useReviewIssueMutation,
	useScanIssuesMutation,
	useStartIssueRunMutation,
} from "@/lib/store/apis/issuebenchApi";
import type { IssueFix, IssueItem, IssueReadiness, IssueRun, IssueStatus, IssueValidation } from "@/lib/types/issuebench";
import { Link } from "@tanstack/react-router";
import { type ReactNode, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import "./w446I18n";
import "./uft5I18n";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		issuebench: typeof import("@/locales/en/issuebench.json");
	}
}

type T = ReturnType<typeof useTranslation<"issuebench">>["t"];

/** The start body: consent, plus the builder model when both fields are filled (empty uses the server's default), plus the cap per issue when given. */
export function startBody(consent: boolean, provider: string, model: string, budgetUSD?: number) {
	const p = provider.trim();
	const m = model.trim();
	const body: { consent: boolean; provider?: string; model?: string; budget_usd?: number } = p && m ? { consent, provider: p, model: m } : { consent };
	if (budgetUSD !== undefined && budgetUSD > 0) body.budget_usd = budgetUSD;
	return body;
}

/**
 * The proof line of a validated issue: fail_to_pass are the fix's tests that failed before the
 * fix and passed after it (validate keeps only those), so a non-empty list is yes for both.
 */
export function proofHolds(v: IssueValidation): boolean {
	return (v.fail_to_pass?.length ?? 0) > 0;
}

/** The cap the person typed, or null when it is not a number from 0.01 to 50 (the server's range). */
export function parseCap(input: string): number | null {
	const n = Number(input.trim());
	return input.trim() !== "" && Number.isFinite(n) && n >= 0.01 && n <= 50 ? n : null;
}

/** The body of a retry with the fix the person chose; null when they cancelled the prompt. */
export function retryBody(fix: IssueFix, answer: string | null) {
	if (fix === "retry" || fix === "open_workers") return { fix };
	if (answer === null || answer.trim() === "") return null;
	const v = answer.trim();
	if (fix === "add_secret") return { fix, secrets: [v] };
	if (fix === "allow_service") return { fix, allow_services: [v] };
	const n = Number(v);
	return Number.isFinite(n) && n > 0 ? { fix, budget_usd: n } : null;
}

const REPO_RE = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;

/**
 * The repository the person typed as owner/name (a GitHub link, a trailing .git or / are
 * stripped), or "" when it is not one. Same rule as the server (pipeline.ValidRepo).
 */
export function normalizeRepo(input: string): string {
	let v = input
		.trim()
		.replace(/^https?:\/\//i, "")
		.replace(/^(www\.)?github\.com\//i, "");
	v = v
		.replace(/\/+$/, "")
		.replace(/\.git$/i, "")
		.replace(/\/+$/, "");
	return REPO_RE.test(v) && !v.includes("..") ? v : "";
}

/** How often the form refreshes readiness while it is open. */
export const READINESS_POLL_MS = 15000;
/** M1-62: a running issue run is read again every 5 s, only while the tab has focus. */
export const RUN_POLL_MS = 5000;

/** Why Start is blocked on the hosted site, or "" when it is not (no worker taking issuebench.work, or no model gateway). */
export function startBlocked(r: IssueReadiness | undefined): "" | "worker" | "gateway" {
	if (!r || !r.hosted) return "";
	if (!r.worker_online) return "worker";
	if (!r.gateway) return "gateway";
	return "";
}

/** Settings > Workers, the page the no-worker job error also links to. */
function WorkersLink({ children, testId }: { children: ReactNode; testId: string }) {
	return (
		<Link to="/settings" search={{ tab: "runners" } as never} className="text-primary underline" data-testid={testId}>
			{children}
		</Link>
	);
}

/** The banner above Start when the hosted site can not build: no worker online, or no model gateway. */
export function ReadinessBanner({ readiness, t }: { readiness: IssueReadiness | undefined; t: T }) {
	const why = startBlocked(readiness);
	if (!why || !readiness) return null;
	const message = readiness.message || t(why === "worker" ? "readiness.noWorker" : "readiness.noGateway");
	return (
		<div
			role="alert"
			className="border-danger bg-danger-soft text-danger-ink flex flex-col gap-1 rounded-lg border px-3 py-2 text-xs"
			data-testid="issuebench-readiness"
		>
			<span>{message}</span>
			{why === "worker" && <WorkersLink testId="issuebench-readiness-workers">{t("readiness.openWorkers")}</WorkersLink>}
		</div>
	);
}

/** Words for what a worker job does for an issue right now, or "" when nothing runs. */
export function stageWords(it: Pick<IssueItem, "stage" | "status">, t: T): string {
	const done: IssueStatus[] = ["ready", "failed", "accepted", "rejected"];
	if (!it.stage || done.includes(it.status)) return "";
	return t(`stage.${it.stage}`);
}

export function FromIssues({ writable }: { writable: boolean }) {
	const { t } = useTranslation("issuebench");
	const listId = useId();
	const status = useGetGithubStatusQuery();
	const connected = status.data?.connected === true;
	const repos = useListGithubReposQuery(1, { skip: !connected });
	const [repo, setRepo] = useState("");
	const [runId, setRunId] = useState("");
	const [scan, scanState] = useScanIssuesMutation();
	const [error, setError] = useState("");
	const readiness = useGetIssueReadinessQuery(undefined, { pollingInterval: READINESS_POLL_MS, skipPollingIfUnfocused: true });
	const name = normalizeRepo(repo);

	const onScan = async () => {
		setError("");
		try {
			const run = await scan({ repo: name }).unwrap();
			setRunId(run.id);
		} catch (e) {
			setError(getBenchErrorMessage(e));
		}
	};

	return (
		<div className="flex min-w-0 flex-col gap-4 text-sm" data-testid="issuebench-from-issues">
			<p className="text-muted-foreground max-w-2xl">{t("intro")}</p>
			<div className="flex flex-wrap items-end gap-2">
				<label className="flex flex-col gap-1">
					<span className="text-xs font-medium">{t("repo")}</span>
					<input
						className="border-input bg-background h-9 w-72 rounded-md border px-2"
						value={repo}
						list={listId}
						placeholder={t("repoPlaceholder")}
						autoComplete="off"
						spellCheck={false}
						aria-invalid={repo.trim() !== "" && !name}
						onChange={(e) => setRepo(e.target.value)}
						data-testid="ib-repo-input"
					/>
					<datalist id={listId}>
						{(repos.data?.repos ?? []).map((r) => (
							<option key={r.full_name} value={r.full_name} />
						))}
					</datalist>
				</label>
				<Button size="sm" disabled={!writable || !connected || !name || scanState.isLoading} onClick={onScan} dataTestId="issuebench-scan">
					{scanState.isLoading ? t("scanning") : t("scan")}
				</Button>
			</div>
			<p className="text-muted-foreground text-xs" data-testid="issuebench-languages">
				{t("fromIssues.languages", { ns: "w446" })}
			</p>
			{repo.trim() !== "" && !name && (
				<p className="text-muted-foreground text-xs" data-testid="issuebench-repo-invalid">
					{t("repoInvalid")}
				</p>
			)}
			{status.data && !connected && (
				<p className="text-muted-foreground" data-testid="issuebench-connect-first">
					{t("connectFirst")}
				</p>
			)}
			{error && (
				<p className="text-destructive" role="alert">
					{error}
				</p>
			)}
			{runId && <RunView id={runId} writable={writable} readiness={readiness.data} t={t} />}
			<PastIssueRuns openId={runId} onOpen={setRunId} writable={writable} />
			<WeeklySync repo={name} writable={writable} />
		</div>
	);
}

export function RunView({ id, writable, readiness, t }: { id: string; writable: boolean; readiness?: IssueReadiness; t: T }) {
	const [poll, setPoll] = useState(0);
	const run = useGetIssueRunQuery(id, { pollingInterval: poll, skipPollingIfUnfocused: true });
	const [start, startState] = useStartIssueRunMutation();
	const [consent, setConsent] = useState(false);
	// M6-27: provider and model come from the shared model picker (your model keys), not two free text boxes.
	const [picked, setPicked] = useState<PickedModel | null>(null);
	const { t: tf } = useTranslation("uf6a");
	const { t: t5 } = useTranslation("uft5");
	const [capText, setCapText] = useState("1.00");
	const cap = parseCap(capText);
	const keyState = usePickedKeyState(picked);
	const provider = isPicked(picked) && keyState === "ok" ? picked.provider : "";
	const model = isPicked(picked) && keyState === "ok" ? picked.model : "";
	const [error, setError] = useState("");
	const data = run.data;
	const running = data?.status === "running";
	if (running && poll === 0) setPoll(RUN_POLL_MS);
	if (!running && poll !== 0 && data) setPoll(0);
	if (!data) return null;
	const items = data.items ?? [];
	const blocked = startBlocked(readiness);
	// M6-27: why Start is off, as visible text (not only a tooltip).
	const startWhy = !writable
		? tf("issues.whyViewer")
		: blocked
			? t(blocked === "worker" ? "readiness.startNoWorker" : "readiness.startNoGateway")
			: !consent
				? tf("issues.whyConsent")
				: cap === null
					? t5("capBad")
					: "";

	const onStart = async () => {
		setError("");
		try {
			await start({ id, ...startBody(consent, provider, model, cap ?? undefined) }).unwrap();
			setPoll(RUN_POLL_MS);
		} catch (e) {
			setError(getBenchErrorMessage(e));
		}
	};

	return (
		<div className="flex min-w-0 flex-col gap-4">
			<FunnelTable run={data} t={t} />
			{items.length > 0 && <ReadinessBanner readiness={readiness} t={t} />}
			{items.length === 0 ? (
				<p className="text-muted-foreground">{t("none")}</p>
			) : (
				<div className="flex flex-wrap items-end gap-3">
					<div className="w-full min-w-0 sm:w-80">
						<LabeledModelPicker
							label={t("model")}
							hint={
								data.builder_model?.model
									? tf("issues.modelDefault", { model: `${data.builder_model.provider}/${data.builder_model.model}` })
									: undefined
							}
							value={picked}
							onChange={setPicked}
							testId="issuebench-model"
						/>
					</div>
					<label className="flex flex-col gap-1 text-sm">
						<span>{t5("capLabel")}</span>
						<input
							type="number"
							min="0.01"
							max="50"
							step="0.01"
							inputMode="decimal"
							className="border-input bg-background w-28 rounded-md border px-2 py-1"
							value={capText}
							onChange={(e) => setCapText(e.target.value)}
							data-testid="issuebench-cap"
						/>
					</label>
					<label className="flex items-center gap-2">
						<input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} data-testid="issuebench-consent" />
						<span>{t("consent")}</span>
					</label>
					<Button
						size="sm"
						disabled={!writable || !consent || cap === null || running || startState.isLoading || blocked !== ""}
						title={blocked ? t(blocked === "worker" ? "readiness.startNoWorker" : "readiness.startNoGateway") : undefined}
						aria-describedby={startWhy ? "issuebench-start-why" : undefined}
						onClick={onStart}
						dataTestId="issuebench-start"
					>
						{running ? t("running") : t("start")}
					</Button>
					<span className="text-muted-foreground text-xs" data-testid="issuebench-cost">
						{t("cost", { cost: data.cost_usd.toFixed(2) })}
					</span>
					</div>
					)}
					{items.length > 0 && (
						<div className="text-muted-foreground flex flex-col gap-1 text-sm" data-testid="issuebench-start-info">
							<p>{t5("what")}</p>
							<p>{t5("paid", { cap: (cap ?? 1).toFixed(2) })}</p>
							<p>{t5("time")}</p>
						</div>
					)}
					{items.length > 0 && <WhyDisabled reason={startWhy || null} testId="issuebench-start-why" />}
			{error && (
				<p className="text-destructive" role="alert">
					{error}
				</p>
			)}
			{data.benchmark && <p data-testid="issuebench-benchmark">{t("benchmark", { id: data.benchmark })}</p>}
			{items.length > 0 && <IssueGrid run={data} writable={writable} t={t} />}
			{(data.log ?? []).length > 0 && (
				<div className="flex flex-col gap-1">
					<span className="text-xs font-medium">{t("log")}</span>
					<pre className="bg-muted max-h-64 overflow-auto rounded-md p-2 text-xs whitespace-pre-wrap" data-testid="issuebench-log">
						{(data.log ?? []).map((l) => l.line).join("\n")}
					</pre>
				</div>
			)}
		</div>
	);
}

export function FunnelTable({ run, t }: { run: Pick<IssueRun, "funnel">; t: T }) {
	const f = run.funnel;
	const reasons = Object.entries(f.reasons ?? {}).sort((a, b) => b[1] - a[1]);
	return (
		<div className="flex flex-col gap-1" data-testid="issuebench-funnel">
			<span className="text-xs font-medium">{t("funnel.title")}</span>
			<table className="w-full max-w-md text-left text-xs">
				<tbody>
					<tr>
						<td>{t("funnel.merged")}</td>
						<td className="tabular-nums">{f.merged_prs}</td>
					</tr>
					<tr>
						<td>{t("funnel.linked")}</td>
						<td className="tabular-nums">{f.linked}</td>
					</tr>
					<tr>
						<td>{t("funnel.qualified")}</td>
						<td className="tabular-nums">{f.qualified}</td>
					</tr>
					{reasons.map(([r, n]) => (
						<tr key={r} className="text-muted-foreground">
							<td>
								{t("funnel.reason")} {r}
							</td>
							<td className="tabular-nums">{n}</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

function IssueGrid({ run, writable, t }: { run: IssueRun; writable: boolean; t: T }) {
	return (
		<div className="overflow-x-auto">
			<table className="w-full text-left text-xs" data-testid="issuebench-grid">
				<thead>
					<tr className="text-muted-foreground">
						<th className="py-1 pr-2">{t("grid.issue")}</th>
						<th className="py-1 pr-2">{t("grid.title")}</th>
						<th className="py-1 pr-2">{t("grid.version")}</th>
						<th className="py-1 pr-2">{t("grid.status")}</th>
						<th className="py-1 pr-2">{t("grid.tests")}</th>
						<th className="py-1">{t("grid.actions")}</th>
					</tr>
				</thead>
				<tbody>
					{(run.items ?? []).map((it) => (
						<IssueRow key={it.candidate.issue} runId={run.id} it={it} writable={writable} t={t} />
					))}
				</tbody>
			</table>
		</div>
	);
}

/** A "Show log" toggle that opens the job's live log for the issue's current or last job. */
export function IssueJobLog({ jobId, t, initialOpen = false }: { jobId: string; t: T; initialOpen?: boolean }) {
	const [open, setOpen] = useState(initialOpen);
	return (
		<div className="flex flex-col gap-1">
			<button
				type="button"
				className="text-primary w-fit text-left underline"
				onClick={() => setOpen(!open)}
				data-testid={`issuebench-log-toggle-${jobId}`}
			>
				{open ? t("hideLog") : t("showLog")}
			</button>
			{open && <JobLogPanel jobId={jobId} onClose={() => setOpen(false)} />}
		</div>
	);
}

export function IssueRow({ runId, it, writable, t }: { runId: string; it: IssueItem; writable: boolean; t: T }) {
	const { t: t5 } = useTranslation("uft5");
	const [review, reviewState] = useReviewIssueMutation();
	const [retry, retryState] = useRetryIssueMutation();
	const [editing, setEditing] = useState(false);
	const [text, setText] = useState(it.instruction || it.candidate.issue_body);
	const [error, setError] = useState("");
	const busy = reviewState.isLoading || retryState.isLoading;
	const issue = it.candidate.issue;
	const act = async (p: Promise<unknown>) => {
		setError("");
		try {
			await p;
		} catch (e) {
			setError(getBenchErrorMessage(e));
		}
	};
	const onFix = (fix: IssueFix) => {
		const answer = fix === "retry" || fix === "open_workers" ? "" : window.prompt(t(`fixPrompt.${fix}`));
		const body = retryBody(fix, answer);
		if (body) void act(retry({ id: runId, issue, ...body }).unwrap());
	};
	const v = it.validation;
	const stage = stageWords(it, t);
	return (
		<tr className="border-t align-top" data-testid={`issuebench-row-${issue}`}>
			<td className="py-1 pr-2 tabular-nums">#{issue}</td>
			<td className="py-1 pr-2">
				{editing ? (
					<textarea
						className="border-input w-full min-w-64 rounded-md border p-1"
						rows={5}
						value={text}
						onChange={(e) => setText(e.target.value)}
					/>
				) : (
					it.candidate.issue_title
				)}
			</td>
			<td className="py-1 pr-2">{it.version}</td>
			<td className="py-1 pr-2">
				<span data-testid={`issuebench-status-${issue}`}>{t(`status.${it.status}`)}</span>
				{stage && (
					<div className="text-muted-foreground" data-testid={`issuebench-stage-${issue}`}>
						{stage}
					</div>
				)}
				{it.reason && <div className="text-muted-foreground">{it.reason}</div>}
				{it.job_id && <IssueJobLog jobId={it.job_id} t={t} />}
				{error && <div className="text-destructive">{error}</div>}
			</td>
			<td className="py-1 pr-2">
				{v ? t("tests", { f2p: v.fail_to_pass?.length ?? 0, p2p: v.pass_to_pass?.length ?? 0 }) : ""}
				{v && <div className="text-muted-foreground text-xs" data-testid={`issuebench-proof-${issue}`}>{t5("proof", { before: t5(proofHolds(v) ? "yes" : "no"), after: t5(proofHolds(v) ? "yes" : "no") })}</div>}
			</td>
			<td className="py-1">
				<div className="flex flex-wrap gap-1">
					{writable && it.status === "ready" && !editing && (
						<>
							<Button
								size="sm"
								disabled={busy}
								onClick={() => void act(review({ id: runId, issue, decision: "accept" }).unwrap())}
								dataTestId={`issuebench-accept-${issue}`}
							>
								{t("accept")}
							</Button>
							<Button size="sm" variant="outline" disabled={busy} onClick={() => setEditing(true)}>
								{t("edit")}
							</Button>
							<Button
								size="sm"
								variant="outline"
								disabled={busy}
								onClick={() => void act(review({ id: runId, issue, decision: "reject" }).unwrap())}
								dataTestId={`issuebench-reject-${issue}`}
							>
								{t("reject")}
							</Button>
						</>
					)}
					{writable && editing && (
						<Button
							size="sm"
							disabled={busy}
							onClick={() =>
								void act(
									review({ id: runId, issue, decision: "edit", instruction: text })
										.unwrap()
										.then(() => setEditing(false)),
								)
							}
						>
							{t("save")}
						</Button>
					)}
					{it.status === "failed" && it.fix === "open_workers" && (
						<WorkersLink testId={`issuebench-open-workers-${issue}`}>{t("fix.open_workers")}</WorkersLink>
					)}
					{writable && it.status === "failed" && it.fix && (
						<Button
							size="sm"
							variant="outline"
							disabled={busy}
							onClick={() => onFix(it.fix as IssueFix)}
							dataTestId={`issuebench-fix-${issue}`}
						>
							{t(it.fix === "open_workers" ? "fix.retry" : `fix.${it.fix}`)}
						</Button>
					)}
				</div>
			</td>
		</tr>
	);
}