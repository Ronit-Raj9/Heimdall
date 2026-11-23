import { CompareRefsForm } from "@/components/experiments/compareRefsForm";
import { FollowUpJob } from "@/components/experiments/followUpJob";
import { MigrateForm } from "@/components/experiments/migrateForm";
import { ActionCard } from "@/components/experiments/compareActionCard";
import { groupFaults } from "@/app/evaluate/views/step4Setup";
import { Input } from "@/components/ui/input";
import { useGetExperimentAdaptiveQuery } from "@/lib/store/apis/adaptiveApi";
import { getBenchErrorMessage, useGetExperimentQuery, useGetSuitesQuery, useGetVerdictQuery } from "@/lib/store/apis/benchApi";
import { useGetBenchFaultsQuery } from "@/lib/store/apis/evaluationsApi";
import type { ExperimentStatus } from "@/lib/types/bench";
import { EXTEND_MAX_TASKS, EXTEND_MAX_TRIALS, type ExtendJobParams, type ProbeJobParams } from "@/lib/types/experimentActions";
import { GitBranch, GitCompareArrows, Layers, Radar } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import "@/components/verdict/hw09I18n";
import { usePartialSuiteRefetch } from "@/app/experiments_/new/views/partialRefetch";

export interface ActionsTabProps {
	expId: string;
	/** The manifest's suite id, and the version it froze. */
	suite?: string;
	suiteVersion?: string;
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
export function ActionsTab({ expId, suite, suiteVersion }: ActionsTabProps) {
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
			<p className="text-muted-foreground text-sm">{tu("actions.intro")}</p>
			<ExtendCard expId={expId} />
			<ProbeCard expId={expId} suite={suite} />
			<ActionCard icon={GitCompareArrows} title={t("migrate.title")} description={t("migrate.description")} testId="actions-migrate">
				<MigrateForm
					key={formKey}
					suite={suiteRef}
					initialAgent={first?.agent_file}
					initialFrom={first?.model}
					testId="actions-migrate-form"
				/>
			</ActionCard>
			<CompareRefsCard key={formKey} suite={suiteRef} initialAgent={first?.agent_file} />
		</div>
	);
}

function CompareRefsCard({ suite, initialAgent }: { suite: string; initialAgent?: string }) {
	const { t } = useTranslation("refCompare");
	return (
		<ActionCard icon={GitBranch} title={t("compare.title")} description={t("compare.description")} testId="actions-compare-refs">
			<CompareRefsForm suite={suite} initialAgent={initialAgent} testId="actions-compare-refs-form" />
		</ActionCard>
	);
}

function clamp(n: number, max: number): number {
	return Math.min(max, Math.max(1, Math.floor(n) || 1));
}

/**
 * Why Extend is off on a broken comparison (fix wave 3, N246): the first reason its test was broken,
 * as in the sentence the extend job refuses with.
 */
export function brokenParentReason(reasons: readonly string[] | undefined): string {
	return reasons?.find(Boolean) ?? "see its result";
}

/** The sentence on a broken comparison's Extend card, with the first reason from its verdict. */
function BrokenParentNote({ expId }: { expId: string }) {
	const { t } = useTranslation("experimentActions");
	const verdict = useGetVerdictQuery(expId);
	return (
		<p className="text-chart-error-ink text-sm" role="note" data-testid="actions-extend-broken">
			{t("extend.brokenParent", { reason: brokenParentReason(verdict.data?.verdict.invalid_reasons) })}
		</p>
	);
}

function ExtendCard({ expId }: { expId: string }) {
	const { t } = useTranslation("experimentActions");
	const { t: tHw09 } = useTranslation("hw09");
	const uid = useId();
	const adaptive = useGetExperimentAdaptiveQuery(expId);
	const [mode, setMode] = useState<"trials" | "tasks">("trials");
	const [count, setCount] = useState(2);
	const [force, setForce] = useState(false);
	const decision = adaptive.data?.decision ?? "";
	const more = adaptive.data?.more_evidence;
	const parent = adaptive.data?.extends;
	// A one-setup score run has no verdict to firm up or overturn: extending it is always allowed.
	const open = ["inconclusive", "score"].includes(decision.toLowerCase());
	// Fix wave 3, N246: a broken comparison is never extended, "anyway" or not; the server refuses too.
	const broken = decision.toLowerCase() === "invalid";
	const params: ExtendJobParams | null = parent
		? null
		: {
				exp_id: expId,
				[mode]: clamp(count, mode === "trials" ? EXTEND_MAX_TRIALS : EXTEND_MAX_TASKS),
				...(force ? { force: true } : {}),
			};
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
				{broken && !parent && <BrokenParentNote expId={expId} />}
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
									name={`${uid}-mode`}
									checked={mode === "tasks"}
									onChange={() => {
										setMode("tasks");
										if (more?.tasks) setCount(more.tasks);
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
								max={mode === "trials" ? EXTEND_MAX_TRIALS : EXTEND_MAX_TASKS}
								value={count}
								onChange={(e) => setCount(Number(e.target.value))}
								data-testid="actions-extend-count"
							/>
						</label>
						{decision && !open && !broken && (
							<label className="flex items-center gap-2 text-sm">
								<input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} data-testid="actions-extend-force" />
								{t("extend.force")}
							</label>
						)}
					</>
				)}
				<FollowUpJob
					kind="experiment.extend"
					params={params && !broken && (open || force || !decision) ? params : null}
					label={t("extend.run")}
					confirm={t("extend.confirm", { expId })}
					missing={parent || broken ? undefined : t("extend.notInconclusive")}
					testId="actions-extend-job"
				/>
			</div>
		</ActionCard>
	);
}

/**
 * The fault test: the shared fault catalog (GET /faults, the same faults an evaluation offers),
 * grouped, and below it the benchmark's own failure tests from its suite.yaml. Run stays off until
 * one fault or failure test is ticked.
 */
function ProbeCard({ expId, suite }: { expId: string; suite?: string }) {
	const { t } = useTranslation("experimentActions");
	const { t: th } = useTranslation("hw17");
	const uid = useId();
	const suites = useGetSuitesQuery();
	const catalog = useGetBenchFaultsQuery();
	const [faults, setFaults] = useState<string[]>([]);
	const [picked, setPicked] = useState<string[]>([]);
	const [trials, setTrials] = useState(1);
	const entry = suites.data?.suites.find((s) => s.id === suite);
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
	return (
		<ActionCard icon={Radar} title={t("probe.title")} description={t("probe.description")} testId="actions-probe">
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
												<label key={f.id} className={chip} title={f.help}>
													<input
														type="checkbox"
														checked={faults.includes(f.id)}
														onChange={() => setFaults((cur) => flip(cur, f.id))}
														data-testid={`actions-fault-${f.id}`}
													/>
													<span className="text-xs">{f.label}</span>
												</label>
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
									<label key={p.id} className={chip}>
										<input
											type="checkbox"
											checked={picked.includes(p.id)}
											onChange={() => setPicked((cur) => flip(cur, p.id))}
											data-testid={`actions-probe-${p.id}`}
										/>
										<span className="font-mono text-xs">{p.id}</span>
									</label>
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
					testId="actions-probe-job"
				/>
			</div>
		</ActionCard>
	);
}

export default ActionsTab;