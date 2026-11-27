import { CompareRefsForm } from "@/components/experiments/compareRefsForm";
import { MigrateForm } from "@/components/experiments/migrateForm";
import { ActionCard } from "@/components/experiments/compareActionCard";
import { FollowUpJob } from "@/components/experiments/followUpJob";
import { groupFaults } from "@/app/evaluate/views/step4Setup";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { useGetExperimentAdaptiveQuery } from "@/lib/store/apis/adaptiveApi";
import { errorStatus } from "@/lib/store/apis/session";
import { getBenchErrorMessage, useGetExperimentQuery, useGetVerdictQuery } from "@/lib/store/apis/benchApi";
import { useGetSuiteQuery } from "@/lib/store/apis/suitesLightApi";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { useGetBenchFaultsQuery } from "@/lib/store/apis/evaluationsApi";
import type { ExperimentStatus, MoreEvidence } from "@/lib/types/bench";
import { formatUsd } from "@/lib/money";
import "@/components/experiments/ufrcI18n";
import "@/components/verdict/uf4aI18n";
import { EXTEND_MAX_TASKS, EXTEND_MAX_TRIALS, type ExtendJobParams, type ProbeJobParams } from "@/lib/types/experimentActions";
import { GitBranch, GitCompareArrows, Layers, Radar } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import "@/components/verdict/hw09I18n";
import "@/app/compare/views/w4m3I18n";
import { wholeNumberProblem } from "@/lib/inputChecks";
import { usePartialSuiteRefetch } from "@/app/experiments_/new/views/partialRefetch";

export interface ActionsTabProps {
	expId: string;
	/** The manifest's suite id, and the version it froze. */
	suite?: string;
	suiteVersion?: string;
	/** The grade source the page shows (decision 1); the broken parent note reads its verdict. */
	gradeSource?: string;
	/** Open every card at first render (and so mount its form): for tests and links that point at one action. */
	openAll?: boolean;
}

/** One setup of the experiment (fix wave 2, N66), from the experiment status `setups`. */
export type ArmSetup = NonNullable<ExperimentStatus["setups"]>[number];

const DECISIONS = ["score", "improved", "regressed", "inconclusive", "invalid", "leader"] as const;

/** Plain words for a verdict decision (fix wave 2, N66); an unknown decision reads as is. */
export function decisionText(decision: string, words: Record<(typeof DECISIONS)[number], string>): string {
	const key = decision.toLowerCase();
	return (DECISIONS as readonly string[]).includes(key) ? words[key as (typeof DECISIONS)[number]] : decision;
}

/**
 * The experiment's follow-up actions (web parity L14): extend (more tasks or trials, combined with
 * this analysis), a fault test over the fault catalog and the suite's own probes, and a model
 * comparison. Each queues a job that writes, freezes and starts a new experiment, then links it.
 * Compare models and Compare two git refs start from setup A's agent and model.
 */
export function ActionsTab({ expId, suite, suiteVersion, gradeSource, openAll }: ActionsTabProps) {
	const { t } = useTranslation("experimentActions");
	const { t: tu } = useTranslation("compareUx");
	const status = useGetExperimentQuery(expId);
	const setups = status.data?.setups ?? [];
	const first = setups.find((s) => s.arm === "A") ?? setups[0];
	const suiteRef = suite ? (suiteVersion ? `${suite}@${suiteVersion}` : suite) : "";
	// The forms read their initial values once: wait for the experiment so the prefill is not lost.
	const formKey = first ? `${first.agent_file}|${first.model}` : "none";
	return (
		<div className="flex flex-col gap-3" data-testid="experiment-actions">
			<ExtendCard expId={expId} gradeSource={gradeSource} arms={setups.length || undefined} />
			<ProbeCard expId={expId} suite={suite} open={openAll} />
			<ActionCard
				icon={GitCompareArrows}
				title={t("migrate.title")}
				description={t("migrate.description")}
				defaultOpen={openAll}
				mountWhenOpen
				testId="actions-migrate"
			>
				<SuiteGate suite={suite} testId="actions-migrate-gone">
					<MigrateForm
						key={formKey}
						suite={suiteRef}
						initialAgent={first?.agent_file}
						initialFrom={first?.model}
						testId="actions-migrate-form"
					/>
				</SuiteGate>
			</ActionCard>
			<CompareRefsCard key={formKey} suite={suiteRef} suiteId={suite} initialAgent={first?.agent_file} open={openAll} />
		</div>
	);
}

function CompareRefsCard({
	suite,
	suiteId,
	initialAgent,
	open,
}: {
	suite: string;
	suiteId?: string;
	initialAgent?: string;
	open?: boolean;
}) {
	const { t } = useTranslation("refCompare");
	// V4-1: the form asks GitHub for repos when it mounts, so it mounts only once the card is opened.
	return (
		<ActionCard
			icon={GitBranch}
			title={t("compare.title")}
			description={t("compare.description")}
			defaultOpen={open}
			mountWhenOpen
			testId="actions-compare-refs"
		>
			<SuiteGate suite={suiteId} testId="actions-compare-refs-gone">
				<CompareRefsForm suite={suite} initialAgent={initialAgent} testId="actions-compare-refs-form" />
			</SuiteGate>
		</ActionCard>
	);
}

/** True when the benchmark lookup says the benchmark no longer exists (a 404). Pure. */
export function suiteGone(error: unknown): boolean {
	return errorStatus(error) === 404;
}

/**
 * The setup forms of the Actions tab prefill this comparison's benchmark (Q-20). Mounted only inside
 * an opened card, it asks for that benchmark; when it is gone it says so instead of the form, so
 * nothing can be set up on it.
 */
function SuiteGate({ suite, testId, children }: { suite?: string; testId: string; children: React.ReactNode }) {
	const { t } = useTranslation("ufrc");
	const lookup = useGetSuiteQuery(suite ?? skipToken);
	if (suite && suiteGone(lookup.error)) {
		return (
			<p className="text-muted-foreground text-sm" role="note" data-testid={testId}>
				{t("actions.suiteGone", { suite })}
			</p>
		);
	}
	return <>{children}</>;
}

function clamp(n: number, max: number): number {
	return Math.min(max, Math.max(1, Math.floor(n) || 1));
}

/**
 * The extend job's params for the count typed (p06-2), or null when the count is not a whole number
 * from 1 to max: no silent clamping, the card says what is wrong instead.
 */
export function extendParams(raw: string, max: number, mode: "trials" | "tasks", expId: string, force: boolean): ExtendJobParams | null {
	if (wholeNumberProblem(raw, 1, max)) return null;
	return { exp_id: expId, [mode]: Number(raw.trim()), ...(force ? { force: true } : {}) };
}

/**
 * What an extend would start (M4-33): runs and their price, from the adaptive sizing. More tasks
 * run trials x setups runs each (more.runs / more.tasks); more tries run once per task already
 * run and per setup. The price is the sizing's cost per run times the runs; null when not known.
 */
export function extendEstimate(
	mode: "trials" | "tasks",
	count: number,
	more: MoreEvidence | null | undefined,
	arms: number | undefined,
): { runs: number | null; cost: number | null } {
	if (!Number.isFinite(count) || count < 1 || !more || more.over) return { runs: null, cost: null };
	let runs: number | null = null;
	if (mode === "tasks" && more.tasks > 0 && more.runs > 0) runs = Math.round((count * more.runs) / more.tasks);
	const done = more.tasks_total - more.tasks;
	if (mode === "trials" && arms && done > 0) runs = count * done * arms;
	const perRun = more.cost_usd != null && more.runs > 0 ? more.cost_usd / more.runs : null;
	return { runs, cost: runs != null && perRun != null ? runs * perRun : null };
}

type Translate = (key: string, opts?: Record<string, unknown>) => string;

/**
 * The Extend button label and its confirm sentence (Q-33). The label names the count and, when
 * known, the price ("Extend by 65 tasks, about $7.17"); the confirm does not repeat the price (the
 * PaidButton's estimate line prints it once with the same formatUsd) and says what limits the
 * spend: the job stops after the runs it starts. Pure.
 */
export function extendTexts(
	t: Translate,
	mode: "trials" | "tasks",
	count: number,
	what: string,
	estimate: { runs: number | null; cost: number | null },
): { label: string; confirm: string } {
	const base = t(mode === "tasks" ? "extend.labelTasks" : "extend.labelTrials", { count });
	const label = estimate.cost != null ? t("extend.labelPriced", { base, cost: formatUsd(estimate.cost) }) : base;
	const confirm =
		estimate.runs == null
			? t("extend.confirmUnknown", { count, what })
			: estimate.cost == null
				? t("extend.confirmNoPrice", { count, what, runs: estimate.runs })
				: t("extend.confirmPriced", { count, what, runs: estimate.runs });
	return { label, confirm };
}

/**
 * Why Extend is off on a broken comparison (fix wave 3, N246): the first reason its test was broken,
 * as in the sentence the extend job refuses with.
 */
export function brokenParentReason(reasons: readonly string[] | undefined): string {
	return reasons?.find(Boolean) ?? "see its result";
}

/** The sentence on a broken comparison's Extend card, with the first reason from its verdict. */
function BrokenParentNote({ expId, gradeSource }: { expId: string; gradeSource?: string }) {
	const { t } = useTranslation("experimentActions");
	const verdict = useGetVerdictQuery(gradeSource ? { expId, gradeSource } : expId);
	return (
		<p className="text-chart-error-ink text-sm" role="note" data-testid="actions-extend-broken">
			{t("extend.brokenParent", { reason: brokenParentReason(verdict.data?.verdict.invalid_reasons) })}
		</p>
	);
}

function ExtendCard({ expId, gradeSource, arms }: { expId: string; gradeSource?: string; arms?: number }) {
	const { t: tf } = useTranslation("uf4a");
	const { t: tr } = useTranslation("ufrc");
	const { t } = useTranslation("experimentActions");
	const { t: tHw09 } = useTranslation("hw09");
	const uid = useId();
	const adaptive = useGetExperimentAdaptiveQuery(expId);
	// M4-33: more tasks by default (more tries per task do not help a test over tasks), sized by the adaptive suggestion.
	const [mode, setMode] = useState<"trials" | "tasks">("tasks");
	const { t: tw } = useTranslation("w4m3");
	const [typed, setCount] = useState<string | null>(null);
	const suggested = adaptive.data?.more_evidence;
	const count = typed ?? (suggested && !suggested.over && suggested.tasks > 0 ? String(suggested.tasks) : "2");
	const [force, setForce] = useState(false);
	const decision = adaptive.data?.decision ?? "";
	const more = adaptive.data?.more_evidence;
	const parent = adaptive.data?.extends;
	// A one-setup score run has no verdict to firm up or overturn: extending it is always allowed.
	const open = ["inconclusive", "score"].includes(decision.toLowerCase());
	// Fix wave 3, N246: a broken comparison is never extended, "anyway" or not; the server refuses too.
	const broken = decision.toLowerCase() === "invalid";
	const max = mode === "trials" ? EXTEND_MAX_TRIALS : EXTEND_MAX_TASKS;
	const problem = wholeNumberProblem(count, 1, max);
	const params: ExtendJobParams | null = parent ? null : extendParams(count, max, mode, expId, force);
	const estimate = extendEstimate(mode, Number(count), more, arms);
	const startParams = params && !broken && (open || force || !decision) ? params : null;
	const what = tw(mode === "trials" ? "extend.whatTrials" : "extend.whatTasks");
	const estimateText =
		estimate.runs == null
			? tf("extend.estimateUnknown")
			: estimate.cost == null
				? tf("extend.estimateNoPrice", { runs: estimate.runs })
				: tf("extend.estimate", { runs: estimate.runs, cost: formatUsd(estimate.cost) });
	const texts = extendTexts(tr as unknown as Translate, mode, Number(count), what, estimate);
	return (
		<ActionCard
			icon={Layers}
			title={t("extend.title")}
			description={t("extend.description")}
			testId="actions-extend"
			defaultOpen={open && !parent}
			status={
				decision ? (
					<span
						className="bg-muted text-muted-foreground mt-1 w-fit rounded-full px-2 py-0.5 text-xs"
						data-testid="actions-extend-decision"
					>
						{t("extend.decision", {
							decision: decisionText(decision, {
								score: t("decisionWords.score"),
								improved: t("decisionWords.improved"),
								regressed: t("decisionWords.regressed"),
								inconclusive: t("decisionWords.inconclusive"),
								invalid: t("decisionWords.invalid"),
								leader: tHw09("decision.leader"),
							}),
						})}
					</span>
				) : undefined
			}
		>
			<div className="flex flex-col gap-3">
				{more && !more.over && (
					<p className="text-muted-foreground text-xs">{t("extend.sizing", { tasks: more.tasks, target: more.target_pp })}</p>
				)}
				{broken && !parent && <BrokenParentNote expId={expId} gradeSource={gradeSource} />}
				{parent ? (
					<p className="text-muted-foreground text-sm" data-testid="actions-extend-followup">
						{t("extend.already", { expId: parent.exp_id })}
					</p>
				) : (
					<>
						<fieldset className="flex flex-wrap gap-4 text-sm">
							<legend className="mb-1 text-sm font-medium">{t("extend.mode")}</legend>
							<label className="flex items-center gap-2">
								<input
									type="radio"
									className="size-4"
									name={`${uid}-mode`}
									checked={mode === "trials"}
									onChange={() => setMode("trials")}
									data-testid="actions-extend-trials"
								/>
								{t("extend.trials")}
							</label>
							<label className="flex items-center gap-2">
								<input
									type="radio"
									className="size-4"
									name={`${uid}-mode`}
									checked={mode === "tasks"}
									onChange={() => {
										setMode("tasks");
										if (more?.tasks) setCount(String(more.tasks));
									}}
									data-testid="actions-extend-tasks"
								/>
								{t("extend.tasks")}
							</label>
						</fieldset>
						<label className="flex max-w-40 flex-col gap-1 text-sm" htmlFor={`${uid}-count`}>
							{t("extend.count")}
							<Input
								id={`${uid}-count`}
								type="number"
								min={1}
								max={max}
								value={count}
								aria-invalid={problem ? true : undefined}
								onChange={(e) => setCount(e.target.value)}
								data-testid="actions-extend-count"
							/>
						</label>
						{decision && !open && !broken && (
							<Checkbox checked={force} onChange={setForce} label={t("extend.force")} testId="actions-extend-force" />
						)}
					</>
				)}
				<FollowUpJob
					kind="experiment.extend"
					params={startParams}
					// Q-33: the label carries the amount; the confirm says what limits the spend, and the price is
					// printed once, by the PaidButton's estimate line, with the same formatUsd as the label.
					label={problem ? t("extend.run") : texts.label}
					confirm={texts.confirm}
					paid={{ estimateRuns: estimate.runs, estimateUsd: estimate.cost }}
					missing={parent || broken ? undefined : (problem ?? t("extend.notInconclusive"))}
					testId="actions-extend-job"
				/>
				{/* The PaidButton prints a known estimate under itself; this line covers the rest. */}
				{!parent && !broken && (!startParams || estimate.runs == null) && (
					<p className="text-muted-foreground text-sm tabular-nums" data-testid="actions-extend-estimate">
						{estimateText}
					</p>
				)}
			</div>
		</ActionCard>
	);
}

/**
 * The fault test: the shared fault catalog (GET /faults, the same faults an evaluation offers),
 * grouped, and below it the benchmark's own failure tests from its suite.yaml. Run stays off until
 * one fault or failure test is ticked.
 */
function ProbeCard({ expId, suite, open }: { expId: string; suite?: string; open?: boolean }) {
	const { t } = useTranslation("experimentActions");
	// Q-20: the benchmark and the fault catalog are asked for only once the card is opened.
	return (
		<ActionCard
			icon={Radar}
			title={t("probe.title")}
			description={t("probe.description")}
			defaultOpen={open}
			mountWhenOpen
			testId="actions-probe"
		>
			<ProbeBody expId={expId} suite={suite} />
		</ActionCard>
	);
}

function ProbeBody({ expId, suite }: { expId: string; suite?: string }) {
	const { t } = useTranslation("experimentActions");
	const { t: tr } = useTranslation("ufrc");
	const { t: th } = useTranslation("hw17");
	const uid = useId();
	// M4-34: only this benchmark (GET /suites/{id}), never the whole 24 MB list.
	const suites = useGetSuiteQuery(suite ?? skipToken);
	const catalog = useGetBenchFaultsQuery();
	const [faults, setFaults] = useState<string[]>([]);
	const [picked, setPicked] = useState<string[]>([]);
	const [trials, setTrials] = useState(1);
	const entry = suites.data;
	const probes = entry?.probes ?? [];
	// N281: a partial entry (the server is still loading the benchmark) has no probes yet: say so and read again.
	const partial = entry?.partial === true;
	usePartialSuiteRefetch(partial, suites.refetch);
	const groups = groupFaults(catalog.data ?? []);
	const flip = (list: string[], id: string) => (list.includes(id) ? list.filter((x) => x !== id) : [...list, id]);
	const params: ProbeJobParams | null =
		faults.length + picked.length > 0
			? {
					exp_id: expId,
					trials: clamp(trials, EXTEND_MAX_TRIALS),
					...(faults.length ? { faults } : {}),
					...(picked.length ? { probes: picked } : {}),
				}
			: null;
	const chip = "hover:bg-muted flex items-center gap-2 rounded-full border px-3 py-1";
	if (suite && suiteGone(suites.error)) {
		return (
			<p className="text-muted-foreground text-sm" role="note" data-testid="actions-probe-gone">
				{tr("actions.suiteGone", { suite })}
			</p>
		);
	}
	return (
		<div className="flex flex-col gap-3">
			{catalog.isLoading || suites.isLoading ? (
				<div className="bg-muted h-16 animate-pulse rounded-lg" aria-label={t("probe.loading")} />
			) : (
				<>
					{catalog.isError ? (
						<p role="alert" className="text-destructive text-sm" data-testid="actions-probe-catalog-error">
							{t("probe.catalogError", { error: getBenchErrorMessage(catalog.error) })}
						</p>
					) : (
						<fieldset className="flex flex-col gap-2 text-sm" data-testid="actions-probe-catalog">
							<legend className="mb-1 text-sm font-medium">{t("probe.catalog")}</legend>
							{groups.map((g) => (
								<div key={g.group} className="flex flex-col gap-1" data-testid={`actions-probe-group-${g.group}`}>
									<span className="text-muted-foreground text-xs font-medium">{g.group}</span>
									<div className="flex flex-wrap gap-x-4 gap-y-2">
										{g.faults.map((f) => (
											<span key={f.id} title={f.help}>
												<Checkbox
													className={chip}
													checked={faults.includes(f.id)}
													onChange={() => setFaults((cur) => flip(cur, f.id))}
													label={<span className="text-xs">{f.label}</span>}
													testId={`actions-fault-${f.id}`}
												/>
											</span>
										))}
									</div>
								</div>
							))}
						</fieldset>
					)}
					{partial && probes.length === 0 && (
						<p className="text-muted-foreground text-xs" data-testid="actions-probe-own-loading">
							{th("probe.ownLoading")}
						</p>
					)}
					{probes.length > 0 && (
						<fieldset className="flex flex-wrap gap-x-4 gap-y-2 text-sm" data-testid="actions-probe-own">
							<legend className="mb-1 text-sm font-medium">{t("probe.own")}</legend>
							{probes.map((p) => (
								<Checkbox
									key={p.id}
									className={chip}
									checked={picked.includes(p.id)}
									onChange={() => setPicked((cur) => flip(cur, p.id))}
									label={<span className="font-mono text-xs">{p.id}</span>}
									testId={`actions-probe-${p.id}`}
								/>
							))}
						</fieldset>
					)}
					<label className="flex max-w-40 flex-col gap-1 text-sm" htmlFor={`${uid}-trials`}>
						{t("probe.trials")}
						<Input
							id={`${uid}-trials`}
							type="number"
							min={1}
							max={EXTEND_MAX_TRIALS}
							value={trials}
							onChange={(e) => setTrials(Number(e.target.value))}
						/>
					</label>
				</>
			)}
			<FollowUpJob
				kind="probe.run"
				params={params}
				label={t("probe.run")}
				confirm={t("probe.confirm", { expId })}
				missing={t("probe.tickOne")}
				paid={{}}
				testId="actions-probe-job"
			/>
		</div>
	);
}

export default ActionsTab;