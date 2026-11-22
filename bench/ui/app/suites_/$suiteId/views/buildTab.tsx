import { JobRunButton } from "@/components/jobs/jobRunButton";
import { Section } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { ReadinessResults, readinessReport } from "@/components/suites/admissionResults";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { benchApi, getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useImportSuiteRowsMutation, useSetSuiteGraderMutation, useSetTaskSplitMutation } from "@/lib/store/apis/suiteBuildApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type { Job, SuiteSummary, SuiteTask } from "@/lib/types/bench";
import type { AnswerGrader, AnswerMatch, RowFormat, RowImportResult } from "@/lib/types/suiteBuild";
import { SUITE_OPS_KINDS } from "@/lib/types/suiteops";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Braces, FileSpreadsheet, GitCommitHorizontal, Headset, Inbox, MessageSquareText } from "lucide-react";
import { useCallback, useMemo, useState, type ChangeEvent } from "react";
import { useTranslation } from "react-i18next";
import { FromCommitSection } from "./opsTab";
import { MockFromOpenAPI, ScenarioRowsImport } from "./scenarioBuild";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		suiteBuild: typeof import("@/locales/en/suiteBuild.json");
	}
}

type T = ReturnType<typeof useTranslation<"suiteBuild">>["t"];

/** The grader a suite uses, in the four words the website shows. */
export type GraderChoice = "tests" | "state" | "exact" | "judge";

export function graderChoice(suite: Pick<SuiteSummary, "grader" | "output">): GraderChoice {
	if (suite.grader === "judge") return "judge";
	if (suite.grader === "answer_match") return "exact";
	if (suite.output === "state") return "state";
	return "tests";
}

/** A question-and-answer suite: its tasks are graded on the answer, so rows and tickets fit it. */
export const isAnswerSuite = (suite: Pick<SuiteSummary, "output">) => suite.output === "answer";

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

/**
 * Build tab of a team's own benchmark (lane X13): choose the grader (and pin the judge), add
 * tasks from a spreadsheet, tickets, a commit or production failures, mark tasks held out, then
 * run the readiness checks and read each task's results.
 */
export function BuildTab({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("suiteBuild");
	const session = useSession();
	const writable = canUse(session, "member");
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="suite-build-tab">
			<p className="text-muted-foreground max-w-3xl text-sm">{t("intro")}</p>
			{!writable && (
				<p role="note" className="text-muted-foreground rounded-sm border px-4 py-2 text-sm" data-testid="suite-build-viewer">
					{t("viewerNote")}
				</p>
			)}
			<GraderSection suite={suite} writable={writable} t={t} />
			<AddTasksSection suite={suite} writable={writable} hosted={session.authMode === "hosted"} t={t} />
			<PrivateSection suite={suite} writable={writable} t={t} />
			<ReadinessSection suite={suite} hosted={session.authMode === "hosted"} t={t} />
		</div>
	);
}

// Grader

const CHOICES: GraderChoice[] = ["tests", "state", "exact", "judge"];

export function GraderSection({ suite, writable, t }: { suite: SuiteSummary; writable: boolean; t: T }) {
	const current = graderChoice(suite);
	const answer = isAnswerSuite(suite);
	const [choice, setChoice] = useState<GraderChoice>(current);
	const [provider, setProvider] = useState("");
	const [model, setModel] = useState("");
	const [votes, setVotes] = useState(1);
	const [save, saved] = useSetSuiteGraderMutation();
	const changed = choice !== current || (choice === "judge" && (provider.trim() !== "" || model.trim() !== ""));
	const judgeReady = choice !== "judge" || (provider.trim() !== "" && model.trim() !== "");
	const enabled = (c: GraderChoice) => (answer ? c === "exact" || c === "judge" : c === current);
	const submit = () => {
		if (!answer || !judgeReady) return;
		const grader: AnswerGrader = choice === "judge" ? "judge" : "answer_match";
		void save({
			suite: suite.id,
			grader,
			...(grader === "judge" ? { judge: { provider: provider.trim(), model: model.trim(), votes } } : {}),
		});
	};
	return (
		<Section title={t("grader.title")} description={answer ? t("grader.description") : t("grader.fixed")} testId="suite-build-grader">
			<div className="flex min-w-0 flex-col gap-4">
				<div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4" role="radiogroup" aria-label={t("grader.title")}>
					{CHOICES.map((c) => (
						<label
							key={c}
							className={cn(
								"flex flex-col gap-0.5 rounded-md border px-3 py-2 text-sm",
								choice === c && "border-primary bg-primary/5",
								enabled(c) && writable ? "cursor-pointer" : "opacity-60",
							)}
							data-testid={`suite-build-grader-${c}`}
						>
							<span className="flex items-center gap-2 font-medium">
								<input
									type="radio"
									name={`grader-${suite.id}`}
									checked={choice === c}
									disabled={!enabled(c) || !writable}
									onChange={() => setChoice(c)}
								/>
								{t(`grader.choice.${c}`)}
								{current === c && <span className="text-muted-foreground ml-auto text-xs font-normal">{t("grader.current")}</span>}
							</span>
							<span className="text-muted-foreground text-xs">{t(`grader.hint.${c}`)}</span>
						</label>
					))}
				</div>
				{answer && choice === "judge" && (
					<div className="grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-3" data-testid="suite-build-judge">
						<Field label={t("grader.provider")}>
							<Input value={provider} placeholder="openai" disabled={!writable} onChange={(e) => setProvider(e.target.value)} data-testid="suite-build-judge-provider" />
						</Field>
						<Field label={t("grader.model")}>
							<Input value={model} placeholder="gpt-4.1-mini" disabled={!writable} onChange={(e) => setModel(e.target.value)} data-testid="suite-build-judge-model" />
						</Field>
						<Field label={t("grader.votes")} hint={t("grader.votesHint")}>
							<select className={selectClass} value={votes} disabled={!writable} onChange={(e) => setVotes(Number(e.target.value))}>
								{[1, 3, 5].map((n) => (
									<option key={n} value={n}>
										{n}
									</option>
								))}
							</select>
						</Field>
						<p className="text-muted-foreground text-xs sm:col-span-3">{t("grader.pinHint")}</p>
					</div>
				)}
				{answer && writable && (
					<div className="flex flex-wrap items-center gap-3">
						<Button onClick={submit} disabled={!changed || !judgeReady || saved.isLoading} isLoading={saved.isLoading} dataTestId="suite-build-grader-save">
							{choice === "judge" ? t("grader.pin") : t("grader.save")}
						</Button>
						{suite.grading === "judged" && (
							<Link to="/suites/$suiteId" params={{ suiteId: suite.id }} search={{ tab: "calibration" } as never} className="text-primary text-sm hover:underline">
								{t("grader.calibrate")}
							</Link>
						)}
					</div>
				)}
				{saved.data && (
					<p role="status" className="text-sm" data-testid="suite-build-grader-saved">
						{saved.data.next}
					</p>
				)}
				<ErrorLine error={saved.error} testId="suite-build-grader-error" />
			</div>
		</Section>
	);
}

// Add tasks

type Source = "rows" | "tickets" | "commit" | "traces" | "scenarios" | "mock";

const SOURCE_ICONS = {
	rows: FileSpreadsheet,
	tickets: MessageSquareText,
	commit: GitCommitHorizontal,
	traces: Inbox,
	scenarios: Headset,
	mock: Braces,
} as const;

/** Where tasks can come from: answer benchmarks take rows, code ones commits, tool ones (Y14) conversations and an API mock. */
export function sourcesFor(suite: Pick<SuiteSummary, "output" | "track">): Source[] {
	if (isAnswerSuite(suite)) return ["rows", "tickets", "traces"];
	if (!suite.output || suite.output === "diff") return ["commit", "traces"];
	if (suite.track === "support") return ["scenarios", "mock", "traces"];
	return ["mock", "traces"];
}

function AddTasksSection({ suite, writable, hosted, t }: { suite: SuiteSummary; writable: boolean; hosted: boolean; t: T }) {
	const { t: tops } = useTranslation("suiteops");
	const { t: ts } = useTranslation("scenarioBuild");
	const sources = sourcesFor(suite);
	const [source, setSource] = useState<Source>(sources[0]);
	return (
		<Section title={t("add.title")} description={t("add.description")} testId="suite-build-add">
			<div className="flex min-w-0 flex-col gap-4">
				<div className="flex flex-wrap gap-2" role="tablist" aria-label={t("add.title")}>
					{sources.map((s) => {
						const Icon = SOURCE_ICONS[s];
						return (
							<Button
								key={s}
								role="tab"
								aria-selected={source === s}
								size="sm"
								variant={source === s ? "default" : "outline"}
								onClick={() => setSource(s)}
								dataTestId={`suite-build-source-${s}`}
							>
								<Icon className="size-4" aria-hidden="true" />
								{s === "scenarios" || s === "mock" ? ts(`source.${s}`) : t(`add.source.${s}`)}
							</Button>
						);
					})}
				</div>
				{!isAnswerSuite(suite) && (
					<p className="text-muted-foreground text-xs" data-testid="suite-build-no-rows">
						{t("add.codeSuite")}
					</p>
				)}
				{source === "scenarios" && (writable ? <ScenarioRowsImport suite={suite} writable={writable} /> : <p className="text-muted-foreground text-sm">{t("viewerNote")}</p>)}
				{source === "mock" && (writable ? <MockFromOpenAPI suite={suite} writable={writable} /> : <p className="text-muted-foreground text-sm">{t("viewerNote")}</p>)}
				{source === "rows" && <RowsImport suite={suite} writable={writable} format="rows" t={t} />}
				{source === "tickets" && <RowsImport suite={suite} writable={writable} format="tickets" t={t} />}
				{source === "commit" && (writable ? <FromCommitSection suite={suite} hosted={hosted} t={tops} /> : <p className="text-muted-foreground text-sm">{t("viewerNote")}</p>)}
				{source === "traces" && (
					<div className="flex flex-col items-start gap-2 text-sm" data-testid="suite-build-traces">
						<p className="text-muted-foreground max-w-2xl">{t("add.traces")}</p>
						<Button asChild size="sm" variant="outline">
							<Link to="/inbox">{t("add.openInbox")}</Link>
						</Button>
					</div>
				)}
			</div>
		</Section>
	);
}

/** CSV or JSONL from the file's name; null when the name says neither. */
export function formatOfFile(name: string): "csv" | "jsonl" | null {
	const n = name.toLowerCase();
	if (n.endsWith(".csv") || n.endsWith(".tsv")) return "csv";
	if (n.endsWith(".jsonl") || n.endsWith(".ndjson") || n.endsWith(".json")) return "jsonl";
	return null;
}

function RowsImport({ suite, writable, format, t }: { suite: SuiteSummary; writable: boolean; format: "rows" | "tickets"; t: T }) {
	const tickets = format === "tickets";
	const [data, setData] = useState("");
	const [fileFormat, setFileFormat] = useState<"csv" | "jsonl">("csv");
	const [source, setSource] = useState("");
	const [match, setMatch] = useState<AnswerMatch>("exact");
	const [heldOut, setHeldOut] = useState(false);
	const [preview, setPreview] = useState<RowImportResult | null>(null);
	const [done, setDone] = useState<RowImportResult | null>(null);
	const [run, state] = useImportSuiteRowsMutation();
	const exact = suite.grader === "answer_match";
	const fmt: RowFormat = tickets ? "tickets" : fileFormat;
	const body = { suite: suite.id, format: fmt, data, held_out: heldOut, ...(exact && !tickets ? { match } : {}), ...(source ? { source } : {}) };
	const reset = () => {
		setPreview(null);
		setDone(null);
	};
	const onFile = (e: ChangeEvent<HTMLInputElement>) => {
		const file = e.target.files?.[0];
		if (!file) return;
		const f = formatOfFile(file.name);
		if (f && !tickets) setFileFormat(f);
		setSource(file.name);
		reset();
		void file.text().then(setData);
	};
	const doPreview = async () => {
		setDone(null);
		const res = await run({ ...body, dry_run: true });
		if ("data" in res && res.data) setPreview(res.data);
	};
	const doImport = async () => {
		const res = await run({ ...body, dry_run: false });
		if ("data" in res && res.data) {
			setDone(res.data);
			setPreview(null);
			setData("");
		}
	};
	if (!writable) return <p className="text-muted-foreground text-sm">{t("viewerNote")}</p>;
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid={`suite-build-${format}`}>
			<p className="text-muted-foreground max-w-3xl text-xs">{tickets ? t("rows.ticketsHelp") : t("rows.help")}</p>
			{tickets && exact && (
				<p role="note" className="border-chart-warning/50 bg-chart-warning/10 rounded-md border px-3 py-2 text-xs" data-testid="suite-build-tickets-exact">
					{t("rows.ticketsNeedJudge")}
				</p>
			)}
			<div className="flex flex-wrap items-end gap-3">
				<Field label={t("rows.file")}>
					<input type="file" accept={tickets ? ".json,.jsonl,.txt" : ".csv,.jsonl,.ndjson,.json"} onChange={onFile} className="text-sm" data-testid={`suite-build-${format}-file`} />
				</Field>
				{!tickets && (
					<Field label={t("rows.format")}>
						<select className={selectClass} value={fileFormat} onChange={(e) => (setFileFormat(e.target.value as "csv" | "jsonl"), reset())}>
							<option value="csv">CSV</option>
							<option value="jsonl">JSON Lines</option>
						</select>
					</Field>
				)}
				{exact && !tickets && (
					<Field label={t("rows.match")}>
						<select className={selectClass} value={match} onChange={(e) => (setMatch(e.target.value as AnswerMatch), reset())} data-testid="suite-build-rows-match">
							{(["exact", "contains_all", "numeric", "regex", "choice"] as const).map((m) => (
								<option key={m} value={m}>
									{t(`rows.matchMode.${m}`)}
								</option>
							))}
						</select>
					</Field>
				)}
			</div>
			<textarea
				className="border-input bg-background min-h-40 w-full rounded-md border p-2 font-mono text-xs"
				value={data}
				placeholder={tickets ? t("rows.ticketsPlaceholder") : fileFormat === "csv" ? t("rows.csvPlaceholder") : t("rows.jsonlPlaceholder")}
				aria-label={t("rows.paste")}
				onChange={(e) => (setData(e.target.value), reset())}
				data-testid={`suite-build-${format}-data`}
			/>
			<label className="flex items-center gap-2 text-sm">
				<input type="checkbox" checked={heldOut} onChange={(e) => (setHeldOut(e.target.checked), reset())} data-testid={`suite-build-${format}-held-out`} />
				{t("rows.heldOut")}
			</label>
			<div className="flex flex-wrap gap-2">
				<Button variant="outline" disabled={!data.trim() || state.isLoading} onClick={() => void doPreview()} dataTestId={`suite-build-${format}-preview`}>
					{t("rows.preview")}
				</Button>
				<Button disabled={!preview || preview.tasks.length === 0 || state.isLoading} isLoading={state.isLoading} onClick={() => void doImport()} dataTestId={`suite-build-${format}-import`}>
					{preview ? t("rows.import", { count: preview.tasks.length }) : t("rows.importIdle")}
				</Button>
			</div>
			<ErrorLine error={state.error} testId={`suite-build-${format}-error`} />
			{preview && <RowPreview result={preview} t={t} />}
			{done && (
				<div role="status" className="border-primary/40 bg-primary/5 flex flex-col gap-1 rounded-md border px-3 py-2 text-sm" data-testid={`suite-build-${format}-done`}>
					<p className="font-medium">{t("rows.done", { count: done.tasks.length })}</p>
					<p className="text-muted-foreground text-xs">{done.next}</p>
					{done.problems.length > 0 && <ProblemList problems={done.problems} t={t} />}
				</div>
			)}
		</div>
	);
}

export function RowPreview({ result, t }: { result: RowImportResult; t: T }) {
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid="suite-build-preview">
			<p className="text-sm">{t("rows.previewSummary", { ok: result.tasks.length, bad: result.problems.length })}</p>
			{result.tasks.length > 0 && (
				<div className="max-h-80 overflow-auto rounded-md border">
					<table className="w-full text-left text-xs">
						<thead className="bg-muted/50 text-muted-foreground sticky top-0">
							<tr>
								<th className="px-2 py-1.5 font-medium">{t("rows.col.line")}</th>
								<th className="px-2 py-1.5 font-medium">{t("rows.col.task")}</th>
								<th className="px-2 py-1.5 font-medium">{t("rows.col.question")}</th>
								<th className="px-2 py-1.5 font-medium">{result.grader === "judge" ? t("rows.col.rubric") : t("rows.col.answer")}</th>
								<th className="px-2 py-1.5 font-medium">{t("rows.col.wrong")}</th>
							</tr>
						</thead>
						<tbody className="divide-border divide-y">
							{result.tasks.map((r) => (
								<tr key={r.id} data-testid={`suite-build-preview-${r.id}`}>
									<td className="text-muted-foreground px-2 py-1 tabular-nums">{r.line}</td>
									<td className="px-2 py-1 font-mono">
										{r.id}
										{r.held_out && <span className="text-muted-foreground ml-1 font-sans">({t("private.heldOut")})</span>}
									</td>
									<td className="px-2 py-1">{r.question}</td>
									<td className="px-2 py-1">{r.check}</td>
									<td className="px-2 py-1" data-testid={`suite-build-preview-wrong-${r.id}`}>
										{(r.wrong_answers ?? []).length > 0 ? (
											<ul className="flex flex-col gap-0.5">
												{(r.wrong_answers ?? []).map((w, i) => (
													<li key={`${i}-${w}`}>{w}</li>
												))}
											</ul>
										) : null}
										{r.generated_wrong && <span className="text-muted-foreground mt-0.5 block">{t("rows.generatedWrong")}</span>}
									</td>
								</tr>
							))}
						</tbody>
					</table>
				</div>
			)}
			{result.problems.length > 0 && <ProblemList problems={result.problems} t={t} />}
		</div>
	);
}

function ProblemList({ problems, t }: { problems: RowImportResult["problems"]; t: T }) {
	return (
		<ul className="text-chart-warning-ink flex flex-col gap-0.5 text-xs" data-testid="suite-build-problems">
			{problems.map((p, i) => (
				<li key={`${p.line}-${i}`}>{t("rows.problem", { line: p.line, message: p.message })}</li>
			))}
		</ul>
	);
}

// Held-out tasks

function PrivateSection({ suite, writable, t }: { suite: SuiteSummary; writable: boolean; t: T }) {
	const heldOut = suite.tasks.filter((x) => x.split === "held_out").length;
	return (
		<Section title={t("private.title")} description={t("private.description")} testId="suite-build-private">
			{suite.tasks.length === 0 ? (
				<EmptyState compact message={t("private.empty")} testId="suite-build-private-empty" />
			) : (
				<div className="flex min-w-0 flex-col gap-2">
					<p className="text-muted-foreground text-xs">{t("private.count", { heldOut, total: suite.tasks.length })}</p>
					<ul className="divide-border flex max-h-96 flex-col divide-y overflow-auto rounded-md border">
						{suite.tasks.map((task) => (
							<SplitRow key={task.id} suiteId={suite.id} task={task} writable={writable} t={t} />
						))}
					</ul>
				</div>
			)}
		</Section>
	);
}

function SplitRow({ suiteId, task, writable, t }: { suiteId: string; task: SuiteTask; writable: boolean; t: T }) {
	const [setSplit, state] = useSetTaskSplitMutation();
	const held = task.split === "held_out";
	return (
		<li className="flex flex-wrap items-center gap-2 px-3 py-2 text-sm" data-testid={`suite-build-split-${task.id}`}>
			<span className="font-mono">{task.id}</span>
			{!task.redacted && task.title && <span className="text-muted-foreground truncate text-xs">{task.title}</span>}
			<span className={cn("ml-auto text-xs", held ? "text-foreground font-medium" : "text-muted-foreground")}>{held ? t("private.heldOut") : t("private.public")}</span>
			{writable && (
				<Button
					size="sm"
					variant="outline"
					disabled={state.isLoading}
					onClick={() => void setSplit({ suite: suiteId, task: task.id, held_out: !held })}
					dataTestId={`suite-build-split-toggle-${task.id}`}
				>
					{held ? t("private.makePublic") : t("private.makeHeldOut")}
				</Button>
			)}
			{state.error ? (
				<span className="basis-full">
					<ErrorLine error={state.error} testId={`suite-build-split-error-${task.id}`} />
				</span>
			) : null}
		</li>
	);
}

// Readiness

function ReadinessSection({ suite, hosted, t }: { suite: SuiteSummary; hosted: boolean; t: T }) {
	const dispatch = useAppDispatch();
	const [task, setTask] = useState("");
	const [job, setJob] = useState<Job | null>(null);
	const report = useMemo(() => (job ? readinessReport(job.result) : null), [job]);
	const onDone = useCallback(
		(j: Job) => {
			setJob(j);
			dispatch(benchApi.util.invalidateTags([{ type: "BenchSuites", id: "LIST" }, { type: "BenchSuites", id: `${suite.id}/health` }]));
		},
		[dispatch, suite.id],
	);
	const [judgeConsent, setJudgeConsent] = useState(false);
	const judge = suite.judge;
	const askJudge = hosted && !!judge;
	const calls = judgeCalls(suite, task);
	const params = { suite: suite.id, ...(task ? { task } : {}), ...(askJudge && judgeConsent ? { judge_consent: true } : {}) };
	const confirm = task
		? t("readiness.confirmOne", { task })
		: askJudge
			? t("readiness.confirmHostedJudged", { count: suite.tasks.length, calls })
			: hosted
				? t("readiness.confirmHosted", { count: suite.tasks.length })
				: t("readiness.confirm", { count: suite.tasks.length });
	return (
		<Section title={t("readiness.title")} description={t("readiness.description")} testId="suite-build-readiness">
			<div className="flex min-w-0 flex-col gap-3">
				<p className="text-sm" data-testid="suite-build-readiness-state">
					{t("readiness.state", { ready: suite.admitted_count, total: suite.task_count })}
				</p>
				{suite.tasks.length === 0 ? (
					<p className="text-muted-foreground text-sm">{t("readiness.noTasks")}</p>
				) : (
					<div className="flex flex-wrap items-end gap-3">
						<Field label={t("readiness.scope")}>
							<select className={selectClass} value={task} onChange={(e) => setTask(e.target.value)} data-testid="suite-build-readiness-scope">
								<option value="">{t("readiness.all", { count: suite.tasks.length })}</option>
								{suite.tasks.map((x) => (
									<option key={x.id} value={x.id}>
										{x.id}
										{x.admitted ? ` (${t("readiness.ready")})` : ""}
									</option>
								))}
							</select>
						</Field>
						<JobRunButton
							key={task}
							kind={SUITE_OPS_KINDS.admit}
							params={params}
							label={t("readiness.run")}
							confirm={confirm}
							disabled={askJudge && !judgeConsent}
							onDone={onDone}
						/>
					</div>
				)}
				{askJudge && judge && suite.tasks.length > 0 && (
					<label className="flex items-start gap-2 text-sm" data-testid="suite-build-readiness-judge-consent">
						<input type="checkbox" className="mt-1" checked={judgeConsent} onChange={(e) => setJudgeConsent(e.target.checked)} />
						<span>{t("readiness.judgeConsent", { calls, provider: judge.provider, model: judge.model })}</span>
					</label>
				)}
				{job && !report && job.error && (
					<p role="alert" className="text-chart-error-ink text-sm" data-testid="suite-build-readiness-error">
						{job.error}
					</p>
				)}
				{report && <ReadinessResults report={report} />}
			</div>
		</Section>
	);
}

/**
 * About how many judge calls the readiness check of a judged benchmark makes:
 * the suites API total for every task, or one task's share of it.
 */
export function judgeCalls(suite: Pick<SuiteSummary, "judge" | "tasks">, task: string): number {
	const total = suite.judge?.readiness_calls ?? 0;
	if (!task || suite.tasks.length === 0) return total;
	return Math.max(1, Math.round(total / suite.tasks.length));
}

export default BuildTab;
