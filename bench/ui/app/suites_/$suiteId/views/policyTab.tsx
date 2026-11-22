import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { useGetJobQuery } from "@/lib/store/apis/platformApi";
import {
	policyReportUrl,
	useGetPolicyDocQuery,
	useGetPolicyReportQuery,
	useListPolicyDocsQuery,
	useReviewPolicyDraftsMutation,
	useUploadPolicyDocMutation,
} from "@/lib/store/apis/policyDocsApi";
import { canUse, useSession } from "@/lib/session/sessionContext";
import type { JsonValue, SuiteSummary } from "@/lib/types/bench";
import type { PolicyDocView, PolicyDraft, PolicyGenerateParams, PolicyRate, PolicyReport } from "@/lib/types/policyDocs";
import { useEffect, useState, type ChangeEvent } from "react";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		policyDocs: typeof import("@/locales/en/policyDocs.json");
	}
}

/** A rate as a whole percent. */
export function pct(v: number): string {
	return `${Math.round(v * 100)}%`;
}

/** Pass rate with its range, e.g. "67% (35% to 88%)". */
export function rateText(r: PolicyRate): string {
	return `${pct(r.rate)} (${pct(r.lo)} to ${pct(r.hi)})`;
}

/** Pending drafts grouped by clause, in clause order. */
export function pendingByClause(drafts: PolicyDraft[]): [string, PolicyDraft[]][] {
	const out = new Map<string, PolicyDraft[]>();
	for (const d of drafts) {
		if (d.status !== "pending") continue;
		out.set(d.clause, [...(out.get(d.clause) ?? []), d]);
	}
	return [...out.entries()];
}

/** The policy.generate job params: an estimate, or a confirmed run within the budget. */
export function generateParams(v: { suite: string; doc: string; provider: string; model: string; budget: string }, estimate: boolean): PolicyGenerateParams {
	const budget = Number(v.budget);
	return {
		suite: v.suite,
		doc: v.doc,
		provider: v.provider.trim(),
		model: v.model.trim(),
		...(budget > 0 ? { budget_usd: budget } : {}),
		...(estimate ? { estimate: true } : { yes: true }),
	};
}

function fileBase64(f: File): Promise<string> {
	return new Promise((resolve, reject) => {
		const r = new FileReader();
		r.onload = () => resolve(String(r.result).replace(/^data:[^,]*,/, ""));
		r.onerror = () => reject(r.error);
		r.readAsDataURL(f);
	});
}

function ErrorLine({ error }: { error: unknown }) {
	if (!error) return null;
	return (
		<p role="alert" className="text-chart-error-ink text-xs break-words">
			{getBenchErrorMessage(error as never)}
		</p>
	);
}

function UploadSection({ suite, onUploaded }: { suite: string; onUploaded: (doc: string) => void }) {
	const { t } = useTranslation("policyDocs");
	const [file, setFile] = useState<File | null>(null);
	const [preview, setPreview] = useState<PolicyDocView | null>(null);
	const [upload, state] = useUploadPolicyDocMutation();
	const send = async (dry: boolean) => {
		if (!file) return;
		const content_base64 = await fileBase64(file);
		const r = await upload({ suite, name: file.name, content_base64, ...(dry ? { dry_run: true } : {}) });
		if ("data" in r && r.data) {
			if (dry) setPreview(r.data);
			else {
				setPreview(null);
				onUploaded(r.data.doc.id);
			}
		}
	};
	return (
		<section className="flex flex-col gap-2" data-testid="policy-upload">
			<h3 className="text-sm font-medium">{t("upload.title")}</h3>
			<div className="flex flex-wrap items-center gap-2">
				<Input
					type="file"
					accept=".pdf,.docx,.md,.markdown,.txt"
					className="h-9 w-72"
					aria-label={t("upload.file")}
					onChange={(e: ChangeEvent<HTMLInputElement>) => (setFile(e.target.files?.[0] ?? null), setPreview(null))}
					data-testid="policy-upload-file"
				/>
				<Button variant="outline" disabled={!file || state.isLoading} onClick={() => void send(true)} dataTestId="policy-upload-preview">
					{t("upload.preview")}
				</Button>
				<Button disabled={!file || state.isLoading} onClick={() => void send(false)} dataTestId="policy-upload-save">
					{t("upload.save")}
				</Button>
			</div>
			<ErrorLine error={state.error} />
			{preview && (
				<>
					<p className="text-muted-foreground text-xs">{t("upload.previewNote", { count: preview.doc.clauses.length })}</p>
					<ClauseList view={preview} />
				</>
			)}
		</section>
	);
}

function ClauseList({ view }: { view: PolicyDocView }) {
	const { t } = useTranslation("policyDocs");
	return (
		<ol className="flex max-h-80 flex-col gap-1 overflow-y-auto rounded-sm border p-2 text-sm" data-testid="policy-clauses">
			{view.doc.clauses.map((c) => (
				<li key={c.number} className={c.skip ? "text-muted-foreground" : undefined}>
					<span className="font-mono">{c.number}</span> {c.title ? <strong>{c.title}: </strong> : null}
					<span className="break-words">{c.text.length > 240 ? `${c.text.slice(0, 240)}...` : c.text}</span>
					{c.skip && <span className="ml-1 text-xs">({t("clauses.skipped")})</span>}
				</li>
			))}
		</ol>
	);
}

function str(v: JsonValue | undefined): string {
	return v === undefined || v === null ? "" : String(v);
}

function GenerateSection({ suite, doc, onDone }: { suite: string; doc: string; onDone: () => void }) {
	const { t } = useTranslation("policyDocs");
	const [provider, setProvider] = useState("");
	const [model, setModel] = useState("");
	const [budget, setBudget] = useState("1");
	const [jobId, setJobId] = useState("");
	const [create, state] = useCreateJobMutation();
	const job = useGetJobQuery(jobId, { skip: !jobId, pollingInterval: 2000 });
	const j = job.data;
	const terminal = j && (j.status === "succeeded" || j.status === "failed" || j.status === "cancelled");
	useEffect(() => {
		if (terminal && j?.status === "succeeded" && !j.result?.estimate) onDone();
	}, [terminal, j, onDone]);
	const start = async (estimate: boolean) => {
		const r = await create({ kind: "policy.generate", params: generateParams({ suite, doc, provider, model, budget }, estimate) });
		if ("data" in r && r.data) setJobId(r.data.id);
	};
	const ready = provider.trim() && model.trim() && !state.isLoading && (!jobId || terminal);
	const res = j?.result;
	return (
		<section className="flex flex-col gap-2" data-testid="policy-generate">
			<h3 className="text-sm font-medium">{t("generate.title")}</h3>
			<div className="flex flex-wrap items-end gap-2">
				<label className="flex flex-col gap-1 text-sm">
					<span className="text-muted-foreground">{t("generate.provider")}</span>
					<Input className="h-9 w-40" value={provider} onChange={(e) => setProvider(e.target.value)} data-testid="policy-generate-provider" />
				</label>
				<label className="flex flex-col gap-1 text-sm">
					<span className="text-muted-foreground">{t("generate.model")}</span>
					<Input className="h-9 w-56" value={model} onChange={(e) => setModel(e.target.value)} data-testid="policy-generate-model" />
				</label>
				<label className="flex flex-col gap-1 text-sm">
					<span className="text-muted-foreground">{t("generate.budget")}</span>
					<Input className="h-9 w-24" inputMode="decimal" value={budget} onChange={(e) => setBudget(e.target.value)} data-testid="policy-generate-budget" />
				</label>
				<Button variant="outline" disabled={!ready} onClick={() => void start(true)} dataTestId="policy-generate-estimate">
					{t("generate.estimate")}
				</Button>
				<Button disabled={!ready} onClick={() => void start(false)} dataTestId="policy-generate-start" title={t("generate.confirm", { budget, model })}>
					{t("generate.start")}
				</Button>
			</div>
			<p className="text-muted-foreground text-xs">{t("generate.budgetHint")}</p>
			<ErrorLine error={state.error} />
			{j && !terminal && <p className="text-xs">{t("generate.running", { id: j.id, status: j.status })}</p>}
			{j?.status === "succeeded" && res?.estimate && (
				<p className="text-xs" data-testid="policy-generate-forecast">
					{t("generate.forecast", { usd: Number(res.forecast_usd ?? 0).toFixed(2), clauses: str(res.clauses) })}
				</p>
			)}
			{j?.status === "succeeded" && !res?.estimate && (
				<p className="text-xs">
					{t("generate.done", { drafted: Array.isArray(res?.drafted) ? res.drafted.length : 0, spent: Number(res?.spent_usd ?? 0).toFixed(2) })}
				</p>
			)}
			{(j?.status === "failed" || j?.status === "cancelled") && (
				<p role="alert" className="text-chart-error-ink text-xs">
					{t("generate.failed", { error: j.error ?? j.status })}
				</p>
			)}
		</section>
	);
}

function DraftsSection({ suite, view }: { suite: string; view: PolicyDocView }) {
	const { t } = useTranslation("policyDocs");
	const [review, state] = useReviewPolicyDraftsMutation();
	const [jp, setJp] = useState("");
	const [jm, setJm] = useState("");
	const groups = pendingByClause(view.doc.drafts);
	const needSwitch = view.grader !== "checks" || !view.judged;
	const judge = needSwitch && jp.trim() && jm.trim() ? { switch_grader: true, judge: { provider: jp.trim(), model: jm.trim() } } : {};
	const act = (accept: string[], reject: string[]) => void review({ suite, doc: view.doc.id, accept, reject, ...(accept.length ? judge : {}) });
	const all = groups.flatMap(([, ds]) => ds.map((d) => d.task));
	return (
		<section className="flex flex-col gap-2" data-testid="policy-drafts">
			<h3 className="text-sm font-medium">{t("drafts.title")}</h3>
			{groups.length === 0 ? (
				<p className="text-muted-foreground text-sm">{t("drafts.empty")}</p>
			) : (
				<>
					{needSwitch && (
						<div className="flex flex-wrap items-end gap-2">
							<p className="text-muted-foreground w-full text-xs">{t("drafts.switchNote")}</p>
							<Input className="h-9 w-40" placeholder={t("drafts.judgeProvider")} value={jp} onChange={(e) => setJp(e.target.value)} />
							<Input className="h-9 w-56" placeholder={t("drafts.judgeModel")} value={jm} onChange={(e) => setJm(e.target.value)} />
						</div>
					)}
					<Button className="self-start" disabled={state.isLoading} onClick={() => act(all, [])} dataTestId="policy-drafts-accept-all">
						{t("drafts.acceptAll")}
					</Button>
					{groups.map(([clause, ds]) => (
						<div key={clause} className="rounded-sm border p-2">
							<p className="text-sm font-medium">{t("drafts.clause", { number: clause })}</p>
							<ul className="flex flex-col gap-2">
								{ds.map((d) => (
									<li key={d.task} className="flex flex-wrap items-start justify-between gap-2 text-sm" data-testid={`policy-draft-${d.task}`}>
										<div className="min-w-0 flex-1">
											<span className="text-muted-foreground text-xs">{t(`drafts.kind.${d.kind}`, { defaultValue: d.kind })}</span>{" "}
											<strong>{d.title}</strong>
											<p className="break-words">{d.opening}</p>
											{d.warnings?.map((w) => (
												<p key={w} className="text-chart-error-ink text-xs">
													{w}
												</p>
											))}
										</div>
										<div className="flex gap-1">
											<Button size="sm" variant="outline" disabled={state.isLoading} onClick={() => act([d.task], [])}>
												{t("drafts.accept")}
											</Button>
											<Button size="sm" variant="ghost" disabled={state.isLoading} onClick={() => act([], [d.task])}>
												{t("drafts.reject")}
											</Button>
										</div>
									</li>
								))}
							</ul>
						</div>
					))}
				</>
			)}
			<ErrorLine error={state.error} />
			{state.data?.next && <p className="text-muted-foreground text-xs">{state.data.next}</p>}
		</section>
	);
}

export function ReportView({ suite, report }: { suite: string; report: PolicyReport }) {
	const { t } = useTranslation("policyDocs");
	return (
		<section className="flex flex-col gap-2" data-testid="policy-report">
			<div className="flex flex-wrap items-center gap-2">
				<h3 className="text-sm font-medium">{t("report.title")}</h3>
				<a className="text-sm underline" href={policyReportUrl(suite, report.doc, "markdown")} data-testid="policy-report-md">
					{t("report.markdown")}
				</a>
				<a className="text-sm underline" href={policyReportUrl(suite, report.doc, "pdf")} data-testid="policy-report-pdf">
					{t("report.pdf")}
				</a>
			</div>
			<p className="text-muted-foreground text-xs">{t("report.summary", { tested: report.tested_clauses, clauses: report.clauses, runs: report.runs })}</p>
			{report.agents.length === 0 && <p className="text-muted-foreground text-sm">{t("report.empty")}</p>}
			{report.agents.map((a) => (
				<div key={a.agent} className="rounded-sm border p-2">
					<p className="text-sm font-medium">
						{a.agent}: {rateText(a.total)}
					</p>
					<table className="mt-1 w-full text-sm">
						<thead>
							<tr className="text-muted-foreground text-left text-xs">
								<th className="pr-2 font-normal">{t("report.clause")}</th>
								<th className="font-normal">{t("report.passRate")}</th>
							</tr>
						</thead>
						<tbody>
							{a.clauses.map((c) => (
								<tr key={c.number}>
									<td className="pr-2">
										<span className="font-mono">{c.number}</span> {c.title}
									</td>
									<td>{rateText(c.total)}</td>
								</tr>
							))}
						</tbody>
					</table>
					{a.findings.length > 0 && (
						<>
							<p className="mt-2 text-xs font-medium">{t("report.findings")}</p>
							<ul className="list-disc pl-5 text-sm">
								{a.findings.map((f) => (
									<li key={`${f.clause}-${f.kind}`}>
										{f.text}
										{f.examples.slice(0, 3).map((e) => (
											<span key={e.run_id} className="text-muted-foreground ml-1 font-mono text-xs">
												{e.run_id}
											</span>
										))}
									</li>
								))}
							</ul>
						</>
					)}
				</div>
			))}
			{report.untested_clauses.length > 0 && <p className="text-muted-foreground text-xs">{t("report.untested", { list: report.untested_clauses.join(", ") })}</p>}
		</section>
	);
}

function DocDetail({ suite, doc, writable }: { suite: string; doc: string; writable: boolean }) {
	const view = useGetPolicyDocQuery({ suite, doc }, { skip: !writable });
	const report = useGetPolicyReportQuery({ suite, doc });
	const refetch = view.refetch;
	return (
		<div className="flex flex-col gap-6">
			{writable && view.data && (
				<>
					<ClauseList view={view.data} />
					<GenerateSection suite={suite} doc={doc} onDone={refetch} />
					<DraftsSection suite={suite} view={view.data} />
				</>
			)}
			<ErrorLine error={view.error} />
			{report.data && <ReportView suite={suite} report={report.data} />}
			<ErrorLine error={report.error} />
		</div>
	);
}

/** Policy tab of a benchmark (lane Z23): policy document to compliance benchmark. */
export function PolicyTab({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("policyDocs");
	const session = useSession();
	const writable = canUse(session, "member");
	const list = useListPolicyDocsQuery(suite.id, { skip: !writable });
	const [doc, setDoc] = useState("");
	const docs = list.data?.docs ?? [];
	const current = doc || docs[0]?.id || "";
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="suite-policy-tab">
			<p className="text-muted-foreground max-w-3xl text-sm">{t("intro")}</p>
			{!writable && (
				<p role="note" className="text-muted-foreground rounded-sm border px-4 py-2 text-sm">
					{t("viewerNote")}
				</p>
			)}
			{writable && <UploadSection suite={suite.id} onUploaded={setDoc} />}
			{writable && (
				<section className="flex flex-col gap-2">
					<h3 className="text-sm font-medium">{t("docs.title")}</h3>
					{docs.length === 0 ? (
						<p className="text-muted-foreground text-sm">{t("docs.empty")}</p>
					) : (
						<ul className="flex flex-col gap-1 text-sm">
							{docs.map((d) => (
								<li key={d.id} className="flex flex-wrap items-center gap-2">
									<Button size="sm" variant={d.id === current ? "default" : "outline"} onClick={() => setDoc(d.id)} dataTestId={`policy-doc-${d.id}`}>
										{t("docs.open")}
									</Button>
									<span className="font-medium">{d.title || d.name}</span>
									<span className="text-muted-foreground text-xs">{t("docs.summary", { clauses: d.clauses, pending: d.drafts?.pending ?? 0 })}</span>
								</li>
							))}
						</ul>
					)}
					<ErrorLine error={list.error} />
				</section>
			)}
			{current && <DocDetail suite={suite.id} doc={current} writable={writable} />}
		</div>
	);
}
