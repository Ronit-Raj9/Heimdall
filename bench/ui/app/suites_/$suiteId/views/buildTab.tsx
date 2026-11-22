import { JobRunButton } from "@/components/jobs/jobRunButton";
import { Section } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { ReadinessResults, readinessReport } from "@/components/suites/admissionResults";
import { Button } from "@/components/ui/button";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { benchApi } from "@/lib/store/apis/benchApi";
import { useSetSuiteGraderMutation, useSetTaskSplitMutation } from "@/lib/store/apis/suiteBuildApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type { Job, SuiteSummary, SuiteTask } from "@/lib/types/bench";
import type { AnswerGrader } from "@/lib/types/suiteBuild";
import { SUITE_OPS_KINDS } from "@/lib/types/suiteops";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Braces, FileSpreadsheet, GitCommitHorizontal, Headset, Inbox, MessageSquareText } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { FromCommitSection } from "./opsTab";
import { ErrorLine, Field, JudgeFields, RowsImportForm, selectClass, type T } from "./rowMapper";
import { MockFromOpenAPI, ScenarioRowsImport } from "./scenarioBuild";

export { formatOfFile, RowPreview } from "./rowMapper";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		suiteBuild: typeof import("@/locales/en/suiteBuild.json");
	}
}

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

function RowsImport({ suite, writable, format, t }: { suite: SuiteSummary; writable: boolean; format: "rows" | "tickets"; t: T }) {
	if (!writable) return <p className="text-muted-foreground text-sm">{t("viewerNote")}</p>;
	return <RowsImportForm suiteId={suite.id} exact={suite.grader === "answer_match"} format={format} t={t} />;
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
