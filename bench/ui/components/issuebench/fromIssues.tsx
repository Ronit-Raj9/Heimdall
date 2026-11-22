// Build tab source "From issues" (lane G06): pick a connected GitHub repository, scan it
// for merged pull requests that close an issue, then build, check and review each one.
import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetGithubStatusQuery, useListGithubReposQuery } from "@/lib/store/apis/githubApi";
import {
	useGetIssueRunQuery,
	useRetryIssueMutation,
	useReviewIssueMutation,
	useScanIssuesMutation,
	useStartIssueRunMutation,
} from "@/lib/store/apis/issuebenchApi";
import type { IssueFix, IssueItem, IssueRun } from "@/lib/types/issuebench";
import { useState } from "react";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		issuebench: typeof import("@/locales/en/issuebench.json");
	}
}

type T = ReturnType<typeof useTranslation<"issuebench">>["t"];

/** The start body: consent, plus the builder model when both fields are filled (empty uses the server's default). */
export function startBody(consent: boolean, provider: string, model: string) {
	const p = provider.trim();
	const m = model.trim();
	return p && m ? { consent, provider: p, model: m } : { consent };
}

/** The body of a retry with the fix the person chose; null when they cancelled the prompt. */
export function retryBody(fix: IssueFix, answer: string | null) {
	if (fix === "retry") return { fix };
	if (answer === null || answer.trim() === "") return null;
	const v = answer.trim();
	if (fix === "add_secret") return { fix, secrets: [v] };
	if (fix === "allow_service") return { fix, allow_services: [v] };
	const n = Number(v);
	return Number.isFinite(n) && n > 0 ? { fix, budget_usd: n } : null;
}

export function FromIssues({ writable }: { writable: boolean }) {
	const { t } = useTranslation("issuebench");
	const status = useGetGithubStatusQuery();
	const repos = useListGithubReposQuery(1, { skip: !status.data?.connected });
	const [repo, setRepo] = useState("");
	const [runId, setRunId] = useState("");
	const [scan, scanState] = useScanIssuesMutation();
	const [error, setError] = useState("");

	const onScan = async () => {
		setError("");
		try {
			const run = await scan({ repo }).unwrap();
			setRunId(run.id);
		} catch (e) {
			setError(getBenchErrorMessage(e));
		}
	};

	return (
		<div className="flex min-w-0 flex-col gap-4 text-sm" data-testid="issuebench-from-issues">
			<p className="text-muted-foreground max-w-2xl">{t("intro")}</p>
			{status.data && !status.data.connected ? (
				<p className="text-muted-foreground" data-testid="issuebench-connect-first">
					{t("connectFirst")}
				</p>
			) : (
				<div className="flex flex-wrap items-end gap-2">
					<label className="flex flex-col gap-1">
						<span className="text-xs font-medium">{t("repo")}</span>
						<select
							className="border-input bg-background h-9 rounded-md border px-2"
							value={repo}
							onChange={(e) => setRepo(e.target.value)}
							data-testid="issuebench-repo"
						>
							<option value="">{t("pickRepo")}</option>
							{(repos.data?.repos ?? []).map((r) => (
								<option key={r.full_name} value={r.full_name}>
									{r.full_name}
								</option>
							))}
						</select>
					</label>
					<Button size="sm" disabled={!writable || !repo || scanState.isLoading} onClick={onScan} dataTestId="issuebench-scan">
						{scanState.isLoading ? t("scanning") : t("scan")}
					</Button>
				</div>
			)}
			{error && (
				<p className="text-destructive" role="alert">
					{error}
				</p>
			)}
			{runId && <RunView id={runId} writable={writable} t={t} />}
		</div>
	);
}

function RunView({ id, writable, t }: { id: string; writable: boolean; t: T }) {
	const [poll, setPoll] = useState(0);
	const run = useGetIssueRunQuery(id, { pollingInterval: poll });
	const [start, startState] = useStartIssueRunMutation();
	const [consent, setConsent] = useState(false);
	const [provider, setProvider] = useState("");
	const [model, setModel] = useState("");
	const [error, setError] = useState("");
	const data = run.data;
	const running = data?.status === "running";
	if (running && poll === 0) setPoll(2000);
	if (!running && poll !== 0 && data) setPoll(0);
	if (!data) return null;
	const items = data.items ?? [];

	const onStart = async () => {
		setError("");
		try {
			await start({ id, ...startBody(consent, provider, model) }).unwrap();
			setPoll(2000);
		} catch (e) {
			setError(getBenchErrorMessage(e));
		}
	};

	return (
		<div className="flex min-w-0 flex-col gap-4">
			<FunnelTable run={data} t={t} />
			{items.length === 0 ? (
				<p className="text-muted-foreground">{t("none")}</p>
			) : (
				<div className="flex flex-wrap items-end gap-3">
					<label className="flex flex-col gap-1">
						<span className="text-xs font-medium">{t("provider")}</span>
						<input
							className="border-input bg-background h-9 w-36 rounded-md border px-2"
							value={provider}
							placeholder={data.builder_model?.provider || "openai"}
							onChange={(e) => setProvider(e.target.value)}
							data-testid="issuebench-provider"
						/>
					</label>
					<label className="flex flex-col gap-1">
						<span className="text-xs font-medium">{t("model")}</span>
						<input
							className="border-input bg-background h-9 w-48 rounded-md border px-2"
							value={model}
							placeholder={data.builder_model?.model || t("modelDefault")}
							onChange={(e) => setModel(e.target.value)}
							data-testid="issuebench-model"
						/>
					</label>
					<label className="flex items-center gap-2">
						<input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} data-testid="issuebench-consent" />
						<span>{t("consent")}</span>
					</label>
					<Button size="sm" disabled={!writable || !consent || running || startState.isLoading} onClick={onStart} dataTestId="issuebench-start">
						{running ? t("running") : t("start")}
					</Button>
					<span className="text-muted-foreground text-xs">{t("cost", { cost: data.cost_usd.toFixed(2) })}</span>
				</div>
			)}
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

function IssueRow({ runId, it, writable, t }: { runId: string; it: IssueItem; writable: boolean; t: T }) {
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
		const answer = fix === "retry" ? "" : window.prompt(t(`fixPrompt.${fix}`));
		const body = retryBody(fix, answer);
		if (body) void act(retry({ id: runId, issue, ...body }).unwrap());
	};
	const v = it.validation;
	return (
		<tr className="border-t align-top" data-testid={`issuebench-row-${issue}`}>
			<td className="py-1 pr-2 tabular-nums">#{issue}</td>
			<td className="py-1 pr-2">
				{editing ? (
					<textarea className="border-input w-full min-w-64 rounded-md border p-1" rows={5} value={text} onChange={(e) => setText(e.target.value)} />
				) : (
					it.candidate.issue_title
				)}
			</td>
			<td className="py-1 pr-2">{it.version}</td>
			<td className="py-1 pr-2">
				<span data-testid={`issuebench-status-${issue}`}>{t(`status.${it.status}`)}</span>
				{it.reason && <div className="text-muted-foreground">{it.reason}</div>}
				{error && <div className="text-destructive">{error}</div>}
			</td>
			<td className="py-1 pr-2">{v ? t("tests", { f2p: v.fail_to_pass?.length ?? 0, p2p: v.pass_to_pass?.length ?? 0 }) : ""}</td>
			<td className="py-1">
				<div className="flex flex-wrap gap-1">
					{writable && it.status === "ready" && !editing && (
						<>
							<Button size="sm" disabled={busy} onClick={() => void act(review({ id: runId, issue, decision: "accept" }).unwrap())} dataTestId={`issuebench-accept-${issue}`}>
								{t("accept")}
							</Button>
							<Button size="sm" variant="outline" disabled={busy} onClick={() => setEditing(true)}>
								{t("edit")}
							</Button>
							<Button size="sm" variant="outline" disabled={busy} onClick={() => void act(review({ id: runId, issue, decision: "reject" }).unwrap())} dataTestId={`issuebench-reject-${issue}`}>
								{t("reject")}
							</Button>
						</>
					)}
					{writable && editing && (
						<Button
							size="sm"
							disabled={busy}
							onClick={() => void act(review({ id: runId, issue, decision: "edit", instruction: text }).unwrap().then(() => setEditing(false)))}
						>
							{t("save")}
						</Button>
					)}
					{writable && it.status === "failed" && it.fix && (
						<Button size="sm" variant="outline" disabled={busy} onClick={() => onFix(it.fix as IssueFix)} dataTestId={`issuebench-fix-${issue}`}>
							{t(`fix.${it.fix}`)}
						</Button>
					)}
				</div>
			</td>
		</tr>
	);
}
