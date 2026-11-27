import { Button } from "@/components/ui/button";
import { isPicked, LabeledModelPicker, splitModelRef, usePickedKeyState } from "@/components/models/labeledModelPicker";
import { ConfirmButton } from "@/components/platform/badges";
import type { PickedModel } from "@/components/models/groupedModelPicker";
import { useConfirm, type ConfirmFn } from "@/components/platform/confirmDialog";
import { Input } from "@/components/ui/input";
import { FileDrop } from "@/components/onboard/sourceBox";
import { Link } from "@tanstack/react-router";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { useGetJobQuery } from "@/lib/store/apis/platformApi";
import {
	policyReportUrl,
	useGetPolicyDocQuery,
	useDeletePolicyDocMutation,
	useGetPolicyReportQuery,
	useListPolicyDocsQuery,
	useReviewPolicyDraftsMutation,
	useSetPolicyClausesMutation,
	useUploadPolicyDocMutation,
} from "@/lib/store/apis/policyDocsApi";
import { canUse, useSession } from "@/lib/session/sessionContext";
import type { JsonValue, SuiteSummary } from "@/lib/types/bench";
import type { PolicyClause, PolicyDocView, PolicyDraft, PolicyGenerateParams, PolicyRate, PolicyReport } from "@/lib/types/policyDocs";
import { useEffect, useRef, useState } from "react";
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

/** clauses with clause i changed by patch; the rest unchanged. */
export function updateClause(clauses: readonly PolicyClause[], i: number, patch: Partial<PolicyClause>): PolicyClause[] {
	return clauses.map((c, j) => (j === i ? { ...c, ...patch } : c));
}

/** The policy.generate job params: an estimate, or a confirmed run within the budget. */
export function generateParams(
	v: { suite: string; doc: string; provider: string; model: string; budget: string },
	estimate: boolean,
): PolicyGenerateParams {
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

function UploadSection({ suite, onUploaded, blocked = false }: { suite: string; onUploaded: (doc: string) => void; blocked?: boolean }) {
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
				{/* M6-21: the same drop zone as Add benchmark, not the native file input. */}
				<div className="w-full sm:w-80">
					<FileDrop
						file={file}
						onFile={(f) => {
							setFile(f);
							setPreview(null);
						}}
						accept=".pdf,.docx,.md,.markdown,.txt"
						disabled={blocked}
						label={file ? file.name : t("upload.file")}
						testId="policy-upload-drop"
						inputTestId="policy-upload-file"
					/>
				</div>
				<Button
					variant="outline"
					disabled={blocked || !file || state.isLoading}
					onClick={() => void send(true)}
					dataTestId="policy-upload-preview"
				>
					{t("upload.preview")}
				</Button>
				<Button disabled={blocked || !file || state.isLoading} onClick={() => void send(false)} dataTestId="policy-upload-save">
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

/** Edits the clauses in place and saves them with PUT .../clauses (the server checks numbers and drafted clauses). */
export function ClauseEditor({ suite, view, onClose }: { suite: string; view: PolicyDocView; onClose: () => void }) {
	const { t } = useTranslation("policyDocs");
	const [clauses, setClauses] = useState<PolicyClause[]>(() => view.doc.clauses.map((c) => ({ ...c })));
	const [save, state] = useSetPolicyClausesMutation();
	const submit = async () => {
		const r = await save({ suite, doc: view.doc.id, clauses });
		if ("data" in r && r.data) onClose();
	};
	return (
		<section className="flex flex-col gap-2" data-testid="policy-clause-editor">
			<h3 className="text-sm font-medium">{t("clauses.edit")}</h3>
			<p className="text-muted-foreground text-xs">{t("clauses.editHint")}</p>
			<ol className="flex max-h-[32rem] flex-col gap-2 overflow-y-auto rounded-sm border p-2">
				{clauses.map((c, i) => (
					<li key={i} className="flex min-w-0 flex-wrap items-start gap-2 text-sm" data-testid={`policy-clause-edit-${i}`}>
						<Input
							className="h-9 w-20 font-mono"
							aria-label={t("clauses.number")}
							value={c.number}
							onChange={(e) => setClauses(updateClause(clauses, i, { number: e.target.value }))}
						/>
						<Input
							className="h-9 w-48"
							aria-label={t("clauses.heading")}
							placeholder={t("clauses.heading")}
							value={c.title ?? ""}
							onChange={(e) => setClauses(updateClause(clauses, i, { title: e.target.value }))}
						/>
						<textarea
							className="border-input bg-background min-h-16 min-w-0 flex-1 rounded-md border px-2 py-1 text-sm"
							aria-label={t("clauses.text")}
							value={c.text}
							onChange={(e) => setClauses(updateClause(clauses, i, { text: e.target.value }))}
						/>
						<label className="flex items-center gap-1.5 text-xs">
							<input
								type="checkbox"
								checked={!!c.skip}
								onChange={(e) => setClauses(updateClause(clauses, i, { skip: e.target.checked }))}
							/>
							{t("clauses.skip")}
						</label>
					</li>
				))}
			</ol>
			<div className="flex gap-2">
				<Button disabled={state.isLoading} onClick={() => void submit()} dataTestId="policy-clauses-save">
					{t("clauses.save")}
				</Button>
				<Button variant="ghost" disabled={state.isLoading} onClick={onClose}>
					{t("clauses.cancel")}
				</Button>
			</div>
			<ErrorLine error={state.error} />
		</section>
	);
}

function str(v: JsonValue | undefined): string {
	return v === undefined || v === null ? "" : String(v);
}

/** The model of the document's last generation ("provider/model"), or null (N275). */
export function lastGenerationModel(doc: PolicyDocView["doc"]): PickedModel | null {
	const gens = (doc as PolicyDocView["doc"] & { generations?: { model?: string }[] | null }).generations ?? [];
	return splitModelRef(gens[gens.length - 1]?.model);
}

function GenerateSection({
	suite,
	doc,
	initial,
	onDone,
	blocked = false,
}: {
	suite: string;
	doc: string;
	initial: PickedModel | null;
	onDone: () => void;
	blocked?: boolean;
}) {
	const { t } = useTranslation("policyDocs");
	const [picked, setPicked] = useState<PickedModel | null>(initial);
	const keyState = usePickedKeyState(picked);
	const provider = picked?.provider ?? "";
	const model = picked?.model ?? "";
	const [budget, setBudget] = useState("0.30");
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
	const keyOk = isPicked(picked) && keyState === "ok";
	const ready = !blocked && keyOk && !state.isLoading && (!jobId || terminal);
	const res = j?.result;
	return (
		<section className="flex flex-col gap-2" data-testid="policy-generate">
			<h3 className="text-sm font-medium">{t("generate.title")}</h3>
			<div className="flex flex-wrap items-end gap-2">
				<LabeledModelPicker
					label={t("generate.model")}
					hint={keyOk || blocked ? undefined : t("generate.pickHint")}
					value={picked}
					disabled={blocked}
					onChange={setPicked}
					testId="policy-generate-model"
				/>
				<label className="flex flex-col gap-1 text-sm">
					<span className="text-muted-foreground">{t("generate.budget")}</span>
					<Input
						className="h-9 w-24"
						inputMode="decimal"
						value={budget}
						disabled={blocked}
						onChange={(e) => setBudget(e.target.value)}
						data-testid="policy-generate-budget"
					/>
				</label>
				<Button variant="outline" disabled={!ready} onClick={() => void start(true)} dataTestId="policy-generate-estimate">
					{t("generate.estimate")}
				</Button>
				<ConfirmButton
					label={t("generate.start")}
					question={t("generate.confirm", { provider, model, budget })}
					confirmLabel={t("generate.start")}
					cancelLabel={t("generate.cancel")}
					variant="outline"
					disabled={!ready}
					onConfirm={() => void start(false)}
					testId="policy-generate-start"
				/>
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
					{t("generate.done", {
						drafted: Array.isArray(res?.drafted) ? res.drafted.length : 0,
						spent: Number(res?.spent_usd ?? 0).toFixed(2),
					})}
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

/** The newest draft's model as the reply judge to offer (N276), or null. */
export function judgeFromDrafts(drafts: readonly PolicyDraft[]): PickedModel | null {
	let best: PolicyDraft | undefined;
	for (const d of drafts) if (d.model && (!best || (d.at ?? "") >= (best.at ?? ""))) best = d;
	return splitModelRef(best?.model);
}

/** A review request of the Policy tab: accepts carry the grader switch and judge when the benchmark needs one. */
export interface PolicyReviewSend {
	accept: string[];
	reject: string[];
	switch_grader?: true;
	judge?: PickedModel;
}

export interface ReviewDraftsDeps {
	needSwitch: boolean;
	judge: PickedModel | null;
	/** Set once the switch was confirmed, so a second accept does not ask again. */
	asked: { current: boolean };
	confirm: ConfirmFn;
	question: { body: string; title: string; confirmLabel: string; cancelLabel: string };
	send(body: PolicyReviewSend): unknown;
}

/**
 * Sends one review (N276). An accept on a benchmark that needs the switch sends switch_grader and
 * the picked judge, and asks once before the first switch; with no judge picked nothing is sent.
 * Reports whether a request was sent.
 */
export async function reviewDrafts(accept: string[], reject: string[], d: ReviewDraftsDeps): Promise<boolean> {
	if (accept.length === 0 || !d.needSwitch) {
		d.send({ accept, reject });
		return true;
	}
	if (!isPicked(d.judge)) return false;
	if (!d.asked.current) {
		const { body, ...opts } = d.question;
		if (!(await d.confirm(body, opts))) return false;
		d.asked.current = true;
	}
	d.send({ accept, reject, switch_grader: true, judge: { provider: d.judge.provider, model: d.judge.model } });
	return true;
}

function DraftsSection({ suite, view, taskCount }: { suite: string; view: PolicyDocView; taskCount: number }) {
	const { t } = useTranslation("policyDocs");
	const [review, state] = useReviewPolicyDraftsMutation();
	const confirm = useConfirm();
	const asked = useRef(false);
	const [judge, setJudge] = useState<PickedModel | null>(() => judgeFromDrafts(view.doc.drafts ?? []));
	const groups = pendingByClause(view.doc.drafts ?? []);
	const needSwitch = view.grader !== "checks" || !view.judged;
	const waiting = needSwitch && !isPicked(judge);
	const act = (accept: string[], reject: string[]) =>
		void reviewDrafts(accept, reject, {
			needSwitch,
			judge,
			asked,
			confirm,
			question: {
				title: t("drafts.switchTitle"),
				body: t("drafts.switchBody", { suite, provider: judge?.provider ?? "", model: judge?.model ?? "", count: taskCount }),
				confirmLabel: t("drafts.switchConfirm"),
				cancelLabel: t("drafts.switchCancel"),
			},
			send: (body) => review({ suite, doc: view.doc.id, ...body }),
		});
	const all = groups.flatMap(([, ds]) => ds.map((d) => d.task));
	return (
		<section className="flex flex-col gap-2" data-testid="policy-drafts">
			<h3 className="text-sm font-medium">{t("drafts.title")}</h3>
			{groups.length === 0 ? (
				<p className="text-muted-foreground text-sm">{t("drafts.empty")}</p>
			) : (
				<>
					{needSwitch && (
						<div className="flex flex-col gap-2">
							<p className="text-muted-foreground text-xs">{t("drafts.switchNote")}</p>
							<LabeledModelPicker
								label={t("drafts.judgeLabel")}
								hint={waiting ? t("drafts.judgeHint") : undefined}
								value={judge}
								onChange={setJudge}
								testId="policy-drafts-judge"
							/>
						</div>
					)}
					<Button
						className="self-start"
						disabled={state.isLoading || waiting}
						onClick={() => act(all, [])}
						dataTestId="policy-drafts-accept-all"
					>
						{t("drafts.acceptAll")}
					</Button>
					{groups.map(([clause, ds]) => (
						<div key={clause} className="rounded-sm border p-2">
							<p className="text-sm font-medium">{t("drafts.clause", { number: clause })}</p>
							<ul className="flex flex-col gap-2">
								{ds.map((d) => (
									<li
										key={d.task}
										className="flex flex-wrap items-start justify-between gap-2 text-sm"
										data-testid={`policy-draft-${d.task}`}
									>
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
											<Button
												size="sm"
												variant="outline"
												disabled={state.isLoading || waiting}
												onClick={() => act([d.task], [])}
												dataTestId={`policy-draft-accept-${d.task}`}
											>
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
			<p className="text-muted-foreground text-xs">
				{t("report.summary", { tested: report.tested_clauses, clauses: report.clauses, runs: report.runs })}
			</p>
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
			{report.untested_clauses.length > 0 && (
				<p className="text-muted-foreground text-xs">{t("report.untested", { list: report.untested_clauses.join(", ") })}</p>
			)}
		</section>
	);
}

function DocDetail({
	suite,
	doc,
	writable,
	blocked,
	taskCount,
}: {
	suite: string;
	doc: string;
	writable: boolean;
	blocked: boolean;
	taskCount: number;
}) {
	const { t } = useTranslation("policyDocs");
	const view = useGetPolicyDocQuery({ suite, doc }, { skip: !writable });
	const report = useGetPolicyReportQuery({ suite, doc });
	const [editing, setEditing] = useState(false);
	const refetch = view.refetch;
	return (
		<div className="flex flex-col gap-6">
			{writable && view.data && (
				<>
					{editing ? (
						<ClauseEditor key={view.data.doc.id} suite={suite} view={view.data} onClose={() => setEditing(false)} />
					) : (
						<div className="flex flex-col gap-2">
							<ClauseList view={view.data} />
							<Button size="sm" variant="outline" className="self-start" onClick={() => setEditing(true)} dataTestId="policy-clauses-edit">
								{t("clauses.edit")}
							</Button>
						</div>
					)}
					<GenerateSection suite={suite} doc={doc} initial={lastGenerationModel(view.data.doc)} onDone={refetch} blocked={blocked} />
					<DraftsSection suite={suite} view={view.data} taskCount={taskCount} />
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
	const list = useListPolicyDocsQuery(suite.id);
	const [remove, removeState] = useDeletePolicyDocMutation();
	const [doc, setDoc] = useState("");
	const docs = list.data?.docs ?? [];
	// No support task to copy: say so before anything is uploaded or a job fails.
	const noShape = !!list.data && !list.data.shape;
	const current = (docs.some((d) => d.id === doc) ? doc : "") || docs[0]?.id || "";
	// M6-75: the in-page confirm, not window.confirm.
	const confirm = useConfirm();
	const onDelete = async (id: string, name: string) => {
		if (!(await confirm(t("docs.deleteConfirm", { name })))) return;
		void remove({ suite: suite.id, doc: id }).then((r) => {
			if (!("error" in r) && id === doc) setDoc("");
		});
	};
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="suite-policy-tab">
			<p className="text-muted-foreground max-w-3xl text-sm">{t("intro")}</p>
			{!writable && (
				<p role="note" className="text-muted-foreground rounded-sm border px-4 py-2 text-sm">
					{t("viewerNote")}
				</p>
			)}
			{noShape && (
				// M6-17: "does not apply here" is a neutral note with the way out, not a red error.
				<p role="note" className="bg-muted/40 text-foreground max-w-3xl rounded-sm border px-4 py-2 text-sm" data-testid="policy-no-shape">
					{t("noShape")}{" "}
					<Link
						to="/suites/new"
						search={{ template: "support" } as never}
						className="text-primary hover:underline"
						data-testid="policy-no-shape-create"
					>
						{t("noShapeLink")}
					</Link>
				</p>
			)}
			{writable && <UploadSection suite={suite.id} onUploaded={setDoc} blocked={noShape} />}
			<section className="flex flex-col gap-2" data-testid="policy-docs">
				<h3 className="text-sm font-medium">{t("docs.title")}</h3>
				{!writable && docs.length > 1 && <p className="text-muted-foreground text-xs">{t("docs.viewerList")}</p>}
				{docs.length === 0 ? (
					<p className="text-muted-foreground text-sm">{t("docs.empty")}</p>
				) : (
					<ul className="flex flex-col gap-1 text-sm">
						{docs.map((d) => (
							<li key={d.id} className="flex flex-wrap items-center gap-2">
								<Button
									size="sm"
									variant={d.id === current ? "default" : "outline"}
									onClick={() => setDoc(d.id)}
									dataTestId={`policy-doc-${d.id}`}
								>
									{t("docs.open")}
								</Button>
								<span className="font-medium">{d.title || d.name || d.id}</span>
								{!list.data?.limited && (
									<span className="text-muted-foreground text-xs">
										{t("docs.summary", { clauses: d.clauses ?? 0, pending: d.drafts?.pending ?? 0 })}
									</span>
								)}
								{writable && (
									<Button
										size="sm"
										variant="ghost"
										disabled={removeState.isLoading}
										onClick={() => void onDelete(d.id, d.title || d.name || d.id)}
										dataTestId={`policy-doc-delete-${d.id}`}
									>
										{t("docs.delete")}
									</Button>
								)}
							</li>
						))}
					</ul>
				)}
				<ErrorLine error={list.error} />
				<ErrorLine error={removeState.error} />
			</section>
			{current && (
				<DocDetail
					suite={suite.id}
					doc={current}
					writable={writable}
					blocked={noShape}
					taskCount={suite.task_count ?? suite.tasks?.length ?? 0}
				/>
			)}
		</div>
	);
}