import { FromIssues } from "@/components/issuebench/fromIssues";
import { JobRunButton } from "@/components/jobs/jobRunButton";
import { Section } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { ReadinessResults, readinessReport } from "@/components/suites/admissionResults";
import { ReadinessChecklist } from "@/components/suites/readinessChecklist";
import "@/components/suites/uf6aI18n";
import { Button } from "@/components/ui/button";
import { Pager } from "@/components/ui/pager";
import { Input } from "@/components/ui/input";
import { searchTasks, taskPage } from "@/components/suites/taskPaging";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { benchApi } from "@/lib/store/apis/benchApi";
import { useSetSuiteGraderMutation, useSetTaskSplitMutation } from "@/lib/store/apis/suiteBuildApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type { Job, SuiteSummary, SuiteTask } from "@/lib/types/bench";
import type { AnswerGrader } from "@/lib/types/suiteBuild";
import { SUITE_OPS_KINDS } from "@/lib/types/suiteops";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Braces, CircleDot, FileSpreadsheet, GitCommitHorizontal, Headset, Inbox, MessageSquareText } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { CommitSources } from "./opsTab";
import { ErrorLine, Field, JudgeFields, RowsImportForm, selectClass, type T } from "./rowMapper";
import { MockFromOpenAPI, ScenarioRowsImport } from "./scenarioBuild";

export { formatOfFile, RowPreview } from "./rowMapper";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		suiteBuild: typeof import("@/locales/en/suiteBuild.json");
	}
}

/** The grader a suite uses, in the words the website shows. */
export type GraderChoice = "tests" | "state" | "exact" | "judge" | "toolCalls";

export function graderChoice(suite: Pick<SuiteSummary, "grader" | "output" | "track">): GraderChoice {
	if (suite.grader === "judge") return "judge";
	if (suite.grader === "answer_match") return "exact";
	if (suite.output === "state") return "state";
	// A tool-use benchmark with no output kind (an MCP benchmark drafted on the website): its script
	// checks the tool calls the agent made, not a code change (N119).
	if (suite.track === "tool_use" && !suite.output) return "toolCalls";
	return "tests";
}

/** A question-and-answer suite: its tasks are graded on the answer, so rows and tickets fit it. */
export const isAnswerSuite = (suite: Pick<SuiteSummary, "output">) => suite.output === "answer";

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

/** The choices shown: "toolCalls" takes the place of "tests" only when it is the suite's grader. */
const choicesFor = (current: GraderChoice): GraderChoice[] =>
	current === "toolCalls" ? CHOICES.map((c) => (c === "tests" ? "toolCalls" : c)) : CHOICES;

/** The section text: answer suites can switch graders; tool-use and code suites keep their own. */
const graderDescription = (answer: boolean, current: GraderChoice, t: T) =>
	answer ? t("grader.description") : current === "toolCalls" ? t("grader.fixedToolUse") : t("grader.fixed");

export function GraderSection({ suite, writable, t }: { suite: SuiteSummary; writable: boolean; t: T }) {
	const current = graderChoice(suite);
	const answer = isAnswerSuite(suite);
	const [choice, setChoice] = useState<GraderChoice>(current);
	const pinned = suite.judge;
	const [provider, setProvider] = useState(pinned?.provider ?? "");
	const [model, setModel] = useState(pinned?.model ?? "");
	const [votes, setVotes] = useState(pinned?.votes || 1);
	const [save, saved] = useSetSuiteGraderMutation();
	// N267: the picker starts on the pinned judge, so only a different pick (or votes) is a change.
	const changed =
		choice !== current ||
		(choice === "judge" &&
			(provider.trim() !== (pinned?.provider ?? "") || model.trim() !== (pinned?.model ?? "") || votes !== (pinned?.votes || 1)));
	const judgeReady = choice !== "judge" || (provider.trim() !== "" && model.trim() !== "");
	const enabled = (c: GraderChoice) => (answer ? c === "exact" || c === "judge" : c === current);
	const { t: tf } = useTranslation("uf6a");
	// M6-22: a card that cannot be picked says why, in one line under it.
	const whyOff = (c: GraderChoice): string | null => {
		if (enabled(c)) return null;
		if (answer) return c === "state" ? tf("grader.why.state") : tf("grader.why.tests");
		return tf("grader.why.answerOnly");
	};
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
		<Section title={t("grader.title")} description={graderDescription(answer, current, t)} testId="suite-build-grader">
			<div className="flex min-w-0 flex-col gap-4">
				<div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4" role="radiogroup" aria-label={t("grader.title")}>
					{choicesFor(current).map((c) => (
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
							{whyOff(c) && (
								<span className="text-muted-foreground text-xs italic" data-testid={`suite-build-grader-${c}-why`}>
									{whyOff(c)}
								</span>
							)}
						</label>
					))}
				</div>
				{answer && choice === "judge" && (
					<JudgeFields
						provider={provider}
						model={model}
						votes={votes}
						disabled={!writable}
						onChange={(p) => {
							if (p.provider !== undefined) setProvider(p.provider);
							if (p.model !== undefined) setModel(p.model);
							if (p.votes !== undefined) setVotes(p.votes);
						}}
						t={t}
					/>
				)}
				{answer && writable && (
					<div className="flex flex-wrap items-center gap-3">
						{/* M6-22: no disabled primary on load; the button shows once another card (or judge setting) is picked. */}
						{changed && (
							<Button
								onClick={submit}
								disabled={!judgeReady || saved.isLoading}
								isLoading={saved.isLoading}
								dataTestId="suite-build-grader-save"
							>
								{choice !== current ? tf("grader.switchTo", { name: t(`grader.choice.${choice}`) }) : t("grader.pin")}
							</Button>
						)}
						{suite.grading === "judged" && (
							<Link
								to="/suites/$suiteId"
								params={{ suiteId: suite.id }}
								search={{ tab: "calibration" } as never}
								className="text-primary text-sm hover:underline"
							>
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

type Source = "rows" | "tickets" | "commit" | "issues" | "traces" | "scenarios" | "mock";

const SOURCE_ICONS = {
	rows: FileSpreadsheet,
	tickets: MessageSquareText,
	commit: GitCommitHorizontal,
	issues: CircleDot,
	traces: Inbox,
	scenarios: Headset,
	mock: Braces,
} as const;

/** Where tasks can come from: answer benchmarks take rows, code ones commits, tool ones (Y14) conversations and an API mock. */
export function sourcesFor(suite: Pick<SuiteSummary, "output" | "track">): Source[] {
	if (isAnswerSuite(suite)) return ["rows", "tickets", "traces"];
	if (!suite.output || suite.output === "diff") return ["commit", "issues", "traces"];
	if (suite.track === "support") return ["scenarios", "mock", "traces"];
	return ["mock", "traces"];
}

function AddTasksSection({ suite, writable, hosted, t }: { suite: SuiteSummary; writable: boolean; hosted: boolean; t: T }) {
	const { t: tops } = useTranslation("suiteops");
	const { t: ts } = useTranslation("scenarioBuild");
	const { t: tf } = useTranslation("uf6a");
	const sources = sourcesFor(suite);
	const [source, setSource] = useState<Source>(sources[0]);
	return (
		<Section title={t("add.title")} description={t("add.description")} testId="suite-build-add">
			<div className="flex min-w-0 flex-col gap-4">
				{/* M6-23: the sources are one segmented choice (a radio group), not a row of toggle-looking buttons. */}
				<div
					className="bg-muted/40 flex w-full max-w-full flex-wrap gap-1 rounded-lg border p-1 sm:w-fit"
					role="radiogroup"
					aria-label={t("add.title")}
				>
					{sources.map((s) => {
						const Icon = SOURCE_ICONS[s];
						const on = source === s;
						return (
							<button
								key={s}
								type="button"
								role="radio"
								aria-checked={on}
								onClick={() => setSource(s)}
								className={cn(
									"focus-visible:ring-(--focus) inline-flex min-h-9 cursor-pointer items-center gap-2 rounded-md px-3 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:outline-none",
									on ? "bg-card text-foreground border shadow-xs" : "text-muted-foreground hover:text-foreground",
								)}
								data-testid={`suite-build-source-${s}`}
							>
								<Icon className="size-4" aria-hidden="true" />
								{s === "scenarios" || s === "mock" ? ts(`source.${s}`) : s === "commit" ? tf("add.commit") : t(`add.source.${s}`)}
							</button>
						);
					})}
				</div>
				{!isAnswerSuite(suite) && (
					<p className="text-muted-foreground text-xs" data-testid="suite-build-no-rows">
						{/* X-89, Q-192: a link to the builder's question and answer template, where rows can be added. */}
						{tf("add.codeSuite")}{" "}
						<Link to="/suites/new" search={{ template: "qa" } as never} className="text-primary hover:underline" data-testid="suite-build-no-rows-create">
							{tf("add.codeSuiteLink")}
						</Link>
					</p>
				)}
				{source === "scenarios" &&
					(writable ? (
						<ScenarioRowsImport suite={suite} writable={writable} />
					) : (
						<p className="text-muted-foreground text-sm">{t("viewerNote")}</p>
					))}
				{source === "mock" &&
					(writable ? (
						<MockFromOpenAPI suite={suite} writable={writable} />
					) : (
						<p className="text-muted-foreground text-sm">{t("viewerNote")}</p>
					))}
				{source === "rows" && <RowsImport suite={suite} writable={writable} format="rows" t={t} />}
				{source === "tickets" && <RowsImport suite={suite} writable={writable} format="tickets" t={t} />}
				{source === "commit" &&
					(writable ? (
						<CommitSources suite={suite} hosted={hosted} t={tops} />
					) : (
						<p className="text-muted-foreground text-sm">{t("viewerNote")}</p>
					))}
				{source === "issues" &&
					(hosted ? <FromIssues writable={writable} /> : <p className="text-muted-foreground text-sm">{t("add.issuesHosted")}</p>)}
				{source === "traces" && (
					<div className="flex flex-col items-start gap-2 text-sm" data-testid="suite-build-traces">
						<p className="text-muted-foreground max-w-2xl">{t("add.traces")}</p>
						<Button asChild size="sm" variant="outline">
							<Link to="/failures">{t("add.openInbox")}</Link>
						</Button>
					</div>
				)}
			</div>
		</Section>
	);
}

function RowsImport({ suite, writable, format, t }: { suite: SuiteSummary; writable: boolean; format: "rows" | "tickets"; t: T }) {
	if (!writable) return <p className="text-muted-foreground text-sm">{t("viewerNote")}</p>;
	return <RowsImportForm suiteId={suite.id} exact={suite.grader === "answer_match"} format={format} t={t} />;
}

// Held-out tasks

function PrivateSection({ suite, writable, t }: { suite: SuiteSummary; writable: boolean; t: T }) {
	const { t: tf } = useTranslation("uf6a");
	const heldOutTasks = useMemo(() => suite.tasks.filter((x) => x.split === "held_out"), [suite.tasks]);
	const heldOut = heldOutTasks.length;
	// M6-24: the list holds the held-out tasks only; a search finds any task to hold out, 50 at a time.
	const [search, setSearch] = useState("");
	const [page, setPage] = useState(1);
	const rows = search.trim() ? searchTasks(suite.tasks, search) : heldOutTasks;
	const paged = taskPage(rows, page, null);
	return (
		<Section title={t("private.title")} description={t("private.description")} testId="suite-build-private">
			{suite.tasks.length === 0 ? (
				<EmptyState compact message={t("private.empty")} testId="suite-build-private-empty" />
			) : (
				<div className="flex min-w-0 flex-col gap-2">
					<p className="text-muted-foreground text-xs">{t("private.count", { heldOut, total: suite.tasks.length })}</p>
					<label className="flex w-full min-w-0 flex-col gap-1 text-sm sm:w-72">
						<span className="text-muted-foreground text-xs">{tf("private.search")}</span>
						<Input
							type="search"
							className="h-8"
							value={search}
							placeholder={tf("tasks.searchPlaceholder")}
							onChange={(e) => {
								setSearch(e.target.value);
								setPage(1);
							}}
							data-testid="suite-build-private-search"
						/>
					</label>
					{rows.length === 0 ? (
						<p className="text-muted-foreground text-sm" data-testid="suite-build-private-none">
							{search.trim() ? tf("private.noMatch") : tf("private.noneHeldOut")}
						</p>
					) : (
						<>
							<ul className="divide-border flex flex-col divide-y rounded-md border">
								{paged.rows.map((task) => (
									<SplitRow key={task.id} suiteId={suite.id} task={task} writable={writable} t={t} />
								))}
							</ul>
							<Pager
								page={paged.page}
								pageCount={paged.pages}
								onPage={setPage}
								rangeText={tf("tasks.pageOf", { first: paged.first, last: paged.last, total: paged.total })}
								testId="suite-build-private-pager"
							/>
						</>
					)}
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
			<span className={cn("ml-auto text-xs", held ? "text-foreground font-medium" : "text-muted-foreground")}>
				{held ? t("private.heldOut") : t("private.public")}
			</span>
			{writable && (
				<Button
					size="sm"
					variant="outline"
					disabled={state.isLoading}
					onClick={() => void setSplit({ suite: suiteId, task: task.id, held_out: !held })}
					dataTestId={`suite-build-split-toggle-${task.id}`}
				>
					{state.isLoading ? t("private.saving") : held ? t("private.makePublic") : t("private.makeHeldOut")}
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
	const { t: tf } = useTranslation("uf6a");
	const dispatch = useAppDispatch();
	const [task, setTask] = useState("");
	const [job, setJob] = useState<Job | null>(null);
	const report = useMemo(() => (job ? readinessReport(job.result) : null), [job]);
	const onDone = useCallback(
		(j: Job) => {
			setJob(j);
			dispatch(
				benchApi.util.invalidateTags([
					{ type: "BenchSuites", id: "LIST" },
					{ type: "BenchSuites", id: `${suite.id}/health` },
				]),
			);
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
					// M6-12: the three steps with their states; step 3 keeps the "Check: All tasks" scope here.
					<ReadinessChecklist
						suite={suite}
						// The job buttons disable themselves for a viewer, as the readiness run here always did.
						writable
						hosted={hosted}
						testId="suite-build-checklist"
						admitControl={
					<div className="flex flex-wrap items-end gap-3">
						<Field label={t("readiness.scope")}>
							<select
								className={selectClass}
								value={task}
								onChange={(e) => setTask(e.target.value)}
								data-testid="suite-build-readiness-scope"
							>
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
							label={tf("checklist.steps.admit")}
							confirm={confirm}
							disabled={askJudge && !judgeConsent}
							onDone={onDone}
						/>
					</div>
						}
					/>
				)}
				{askJudge && judge && suite.tasks.length > 0 && (
					<label className="flex min-h-11 items-start gap-2 text-sm" data-testid="suite-build-readiness-judge-consent">
						<input type="checkbox" className="mt-0.5 size-4 shrink-0" checked={judgeConsent} onChange={(e) => setJudgeConsent(e.target.checked)} />
						<span>{t("readiness.judgeConsent", { calls, provider: judge.provider, model: judge.model })}</span>
					</label>
				)}
				{job && !report && job.error && (
					<p role="alert" className="text-danger-ink text-sm" data-testid="suite-build-readiness-error">
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