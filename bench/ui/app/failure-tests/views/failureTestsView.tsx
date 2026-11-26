import { DEFAULT_BENCHMARK_BUDGET, parseBudget } from "@/app/evaluate/views/step5Launch";
import { budgetLine } from "@/app/evaluate/views/budgetAdvice";
import { keepRunnable } from "@/app/evaluate/views/step3Benchmarks";
import { ESTIMATE_DEBOUNCE_MS, FAULTS, estimateText, groupFaults } from "@/app/evaluate/views/step4Setup";
import { useDebounced } from "@/app/evaluate/views/useDebounced";
import { BenchmarkChecklist } from "@/components/evaluations/benchmarkChecklist";
import { AgentPicker } from "@/components/agents/agentPicker";
import { GroupedModelPicker } from "@/components/models/groupedModelPicker";
import { faultChange, faultName } from "@/components/reportcard/reportCard.utils";
import "@/app/evaluations/views/gw08I18n";
import "@/app/evaluations_/$evalId/views/w4m2I18n";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Section } from "@/components/ui/section";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetAgentsQuery } from "@/lib/store";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import {
	useCreateEvaluationMutation,
	useEstimateEvaluationQuery,
	useGetAgentCompatibilityQuery,
	useGetAgentVersionsQuery,
	useGetBenchFaultsQuery,
	useGetEvaluationReportQuery,
	useListEvaluationsQuery,
	type Estimate,
	type Evaluation,
	type EvaluationSpec,
} from "@/lib/store/apis/evaluationsApi";
import { canUse, useSession } from "@/lib/session/sessionContext";
import "@/lib/types/agentFirstI18n";
import type { AgentSummary } from "@/lib/types/bench";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link, useNavigate } from "@tanstack/react-router";
import { ShieldAlert } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import "./gw07I18n";
import { evalArmLabel } from "@/lib/evaluationArms";

/** A fault a failure test can inject: any id of the fault catalog (GET /faults). */
export type FailureFault = string;
/** The faults ticked when the page opens: the three PS2 faults (decision D19). */
export const FAILURE_FAULTS: readonly FailureFault[] = ["model-down", "broken-tool", "misleading-instruction"];

/** Past failure tests listed. */
export const PAST_SHOWN = 10;

export interface FailureTestForm {
	agent: string;
	version: number;
	provider: string;
	model: string;
	benchmarks: string[];
	faults: FailureFault[];
	tasks: number;
	trials: number;
	/** The Budget (USD) box as typed; empty leaves the server's per-benchmark default. */
	budgetText?: string;
	/** The model picker was used: choosing an agent then keeps model and provider. */
	modelTouched?: boolean;
}

/** The model a failure test runs: the one picked, else the agent's default (fromDefault). */
export function resolvedModel(
	f: FailureTestForm,
	agent?: Pick<AgentSummary, "model" | "gateway"> | null,
): { model: string; provider: string; fromDefault: boolean } {
	if (f.modelTouched || f.model) return { model: f.model, provider: f.provider, fromDefault: false };
	if (agent?.model) return { model: agent.model, provider: agent.gateway?.provider ?? "", fromDefault: true };
	return { model: "", provider: "", fromDefault: false };
}

/** The form after choosing an agent: a model the user picked stays, otherwise the new agent's default applies. */
export function pickAgentForm(f: FailureTestForm, id: string): FailureTestForm {
	return { ...f, agent: id, version: 0, ...(f.modelTouched ? {} : { model: "", provider: "" }) };
}

/** The evaluation a failure test launches: one arm, a clean baseline plus the chosen faults. */
export function failureTestSpec(f: FailureTestForm, agent?: Pick<AgentSummary, "model" | "gateway"> | null): EvaluationSpec {
	const { model, provider } = resolvedModel(f, agent);
	const budget = parseBudget(f.budgetText ?? "");
	return {
		name: `Failure test: ${f.agent}${f.version ? ` v${f.version}` : ""}`,
		mode: "score",
		arms: [
			{
				label: evalArmLabel(0),
				agent: f.agent,
				...(f.version ? { version: f.version } : {}),
				model,
				...(provider ? { provider } : {}),
			},
		],
		benchmarks: f.benchmarks,
		tasks_per_benchmark: f.tasks,
		trials: f.trials,
		faults: ["clean", ...new Set(f.faults.filter((x) => x !== "clean"))],
		...(budget !== undefined ? { budget_usd: budget } : {}),
	};
}

/** Why Launch is off, or null when the form is complete. */
export function formProblem(
	f: FailureTestForm,
	agent?: Pick<AgentSummary, "model" | "gateway"> | null,
): "agent" | "model" | "benchmarks" | "faults" | "budget" | null {
	if (!f.agent) return "agent";
	if (!resolvedModel(f, agent).model) return "model";
	if (f.benchmarks.length === 0) return "benchmarks";
	if (f.faults.length === 0) return "faults";
	if ((f.budgetText ?? "").trim() !== "" && parseBudget(f.budgetText ?? "") === undefined) return "budget";
	return null;
}

/** True when the loaded agent list contains the agent id; false while the list has not loaded. */
export function agentKnown(agents: readonly { id: string }[] | undefined, id: string): boolean {
	return Boolean(id) && (agents ?? []).some((a) => a.id === id);
}

/** Said when the agent named in the link does not exist. */
export function UnknownAgentNote({ name }: { name: string }) {
	const { t } = useTranslation("w4m2");
	return (
		<p className="text-chart-warning-ink w-full text-xs" role="status" data-testid="ft-unknown-agent">
			{t("unknownAgent", { name })}
		</p>
	);
}

/** Faults chosen for a past test that did not apply to one of its benchmarks. */
export function skippedFaultCount(ev: Pick<Evaluation, "children">): number {
	return (ev.children ?? []).reduce((n, c) => n + (c.faults_skipped?.length ?? 0), 0);
}

/** Before launch: the chosen faults a benchmark cannot take, which add no runs. */
export function SkippedFaultsNote({ estimate }: { estimate: Estimate }) {
	const { t } = useTranslation("agentFirst");
	const lines = (estimate.per_benchmark ?? []).flatMap((b) => {
		const byReason = new Map<string, string[]>();
		for (const s of b.faults_skipped ?? []) byReason.set(s.reason, [...(byReason.get(s.reason) ?? []), faultName(s.fault)]);
		return [...byReason].map(([reason, faults]) => ({ key: `${b.benchmark}-${reason}`, benchmark: b.benchmark, reason, faults }));
	});
	if (lines.length === 0) return null;
	return (
		<ul className="text-muted-foreground flex flex-col gap-1 text-xs" data-testid="ft-faults-skipped">
			{lines.map((l) => (
				<li key={l.key}>
					{t("ft.faultsSkipped", { count: l.faults.length, faults: l.faults.join(", "), benchmark: l.benchmark, reason: l.reason })}
				</li>
			))}
		</ul>
	);
}

/** Evaluations that injected at least one fault, newest first, for one agent when given. */
export function pastFailureTests(evaluations: readonly Evaluation[], agent: string, n = PAST_SHOWN): Evaluation[] {
	return evaluations
		.filter((e) => (e.spec?.faults ?? []).some((x) => x !== "clean"))
		.filter(
			(e) =>
				!agent ||
				(e.spec?.arms ?? []).some((a) => a.agent === agent || a.agent.replace(/^agents\//, "").replace(/\.ya?ml(@.*)?$/, "") === agent),
		)
		.sort((a, b) => b.created_at.localeCompare(a.created_at))
		.slice(0, n);
}

/**
 * The estimate box above Run (fix wave 2, N125): runs, cost and minutes from POST /evaluations/estimate,
 * "Updating..." while a change waits or loads, a plain line when it fails (the server's text only as a
 * title), and the budget line once a budget is typed. Before the form is complete it says what is missing.
 */
export function FailureTestEstimate({
	problem,
	estimate,
	pending,
	budgetTyped,
}: {
	problem: ReturnType<typeof formProblem>;
	estimate: { data?: Estimate; isFetching?: boolean; isError?: boolean; error?: unknown };
	pending: boolean;
	budgetTyped: boolean;
}) {
	const { t } = useTranslation("agentFirst");
	const { t: tg } = useTranslation("gw07");
	const updating = pending || Boolean(estimate.isFetching);
	return (
		<div className="flex min-w-0 flex-1 flex-col gap-1 text-sm" data-testid="ft-estimate" aria-live="polite">
			{problem !== null ? (
				<span className="text-muted-foreground">{tg("ft.toSeeCost", { need: t(`ft.need.${problem}`) })}</span>
			) : estimate.isError && !estimate.isFetching ? (
				<span className="text-muted-foreground" title={getBenchErrorMessage(estimate.error)} data-testid="ft-estimate-error">
					{tg("ft.estimateFailed")}
				</span>
			) : estimate.data ? (
				<span className="font-medium">
					{tg("ft.estimateLine", estimateText(estimate.data))}
					{updating && <span className="text-muted-foreground font-normal"> {tg("ft.updating")}</span>}
				</span>
			) : (
				<span className="text-muted-foreground">{t("ft.estimating")}</span>
			)}
			{problem === null && !estimate.isError && budgetTyped && estimate.data?.budget && (
				<span className="text-muted-foreground text-xs" data-testid="ft-budget-line">
					{budgetLine(estimate.data.budget, estimate.data.est_cost_usd, updating)}
				</span>
			)}
		</div>
	);
}

/**
 * Failure tests (/failure-tests?agent=): run faults from the shared fault catalog (GET /faults, the
 * same list the evaluate wizard offers) on any benchmark for one agent, then see how far its score
 * drops. Launch creates an evaluation with those faults; results come from its report card.
 */
export default function FailureTestsView() {
	const { t } = useTranslation("agentFirst");
	const { t: tg } = useTranslation("gw07");
	const navigate = useNavigate();
	const mayRun = canUse(useSession(), "member");
	const [agentParam, setAgentParam] = useQueryState("agent", parseAsString.withOptions({ history: "replace" }));
	const agents = useGetAgentsQuery();
	const suites = useGetSuitesLightQuery();
	const evals = useListEvaluationsQuery();
	const catalog = useGetBenchFaultsQuery();
	const [form, setForm] = useState<FailureTestForm>({
		agent: agentParam ?? "",
		version: 0,
		provider: "",
		model: "",
		benchmarks: [],
		faults: [...FAILURE_FAULTS],
		tasks: 3,
		trials: 1,
	});
	// The agent from the link is trusted only once the agent list has loaded and contains it.
	const known = agentKnown(agents.data?.agents, form.agent);
	const [unknownAgent, setUnknownAgent] = useState<string | null>(null);
	useEffect(() => {
		if (!agents.data || !form.agent || known) return;
		setUnknownAgent(form.agent);
		setForm((f) => ({ ...f, agent: "" }));
		void setAgentParam(null);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [agents.data, form.agent, known]);
	const versions = useGetAgentVersionsQuery(known ? form.agent : skipToken);
	const compat = useGetAgentCompatibilityQuery(form.agent, { skip: !known });
	const [create, createState] = useCreateEvaluationMutation();
	const summary = (agents.data?.agents ?? []).find((a) => a.id === form.agent);
	// The agent's default model applies only while the user has not picked one, and the form says so.
	const chosen = resolvedModel(form, summary);
	const problem = formProblem(form, summary);
	const spec = failureTestSpec(form, summary);
	// The estimate waits until the form has been still for a moment, like the wizard's step 4.
	const settled = useDebounced(spec, ESTIMATE_DEBOUNCE_MS);
	const pending = JSON.stringify(settled) !== JSON.stringify(spec);
	const estimate = useEstimateEvaluationQuery(problem === null ? settled : skipToken);
	// currentData, not data: data keeps the previous agent's answer while the new one loads.
	const compatibility = compat.currentData;
	const checking = Boolean(form.agent) && !compat.currentData && !compat.isError;
	const runnable = keepRunnable(form.benchmarks, compatibility);
	const answered = compatibility?.benchmarks.map((b) => `${b.id}:${b.ok}`).join(",");
	useEffect(() => {
		// Benchmarks picked before the answer came (or for another agent) that this agent cannot run leave the selection.
		if (runnable.length !== form.benchmarks.length) setForm((f) => ({ ...f, benchmarks: keepRunnable(f.benchmarks, compatibility) }));
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [answered, form.benchmarks.join(",")]);

	const pickAgent = (id: string) => {
		setUnknownAgent(null);
		setForm((f) => pickAgentForm(f, id));
		void setAgentParam(id || null);
	};
	const toggle = <T,>(list: T[], item: T, on: boolean) => (on ? [...list, item] : list.filter((x) => x !== item));
	const launch = async () => {
		const res = await create(spec);
		if ("data" in res && res.data) void navigate({ href: `/evaluations/${encodeURIComponent(res.data.id)}` });
	};
	const versionList = [...(versions.data?.versions ?? [])].sort((a, b) => b.version - a.version);
	const suiteList = suites.data?.suites ?? [];
	const past = pastFailureTests(evals.data ?? [], form.agent);
	const faultGroups = groupFaults(catalog.data && catalog.data.length > 0 ? catalog.data : FAULTS);
	const budgetTyped = (form.budgetText ?? "").trim() !== "";

	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="failure-tests-screen">
			<PageHeader title={t("ft.title")} subtitle={t("ft.subtitle")} />

			<Section title={t("ft.newTitle")} description={t("ft.newHint")} testId="ft-form">
				<div className="bg-card flex min-w-0 flex-col gap-6 rounded-xl border p-5">
					<div className="flex flex-wrap items-end gap-4">
						<AgentPicker agents={agents.data?.agents ?? []} value={form.agent} onChange={pickAgent} testId="ft-agent" />
						{unknownAgent && <UnknownAgentNote name={unknownAgent} />}
						<label className="flex flex-col gap-1 text-xs font-medium">
							{t("ft.version")}
							<select
								className="border-input bg-background h-9 rounded-md border px-2 text-sm"
								value={form.version}
								onChange={(e) => setForm((f) => ({ ...f, version: Number(e.target.value) }))}
								disabled={!form.agent}
								data-testid="ft-version"
							>
								<option value={0}>{t("ft.currentConfig")}</option>
								{versionList.map((v) => (
									<option key={v.version} value={v.version}>
										v{v.version}
										{v.note ? ` - ${v.note}` : ""}
									</option>
								))}
							</select>
						</label>
						<div className="flex min-w-64 flex-col gap-1 text-xs font-medium">
							{t("ft.model")}
							<GroupedModelPicker
								value={chosen.model || chosen.provider ? { provider: chosen.provider, model: chosen.model } : null}
								onChange={(v) => setForm((f) => ({ ...f, provider: v.provider, model: v.model, modelTouched: true }))}
								testId="ft-model"
							/>
							{chosen.fromDefault && form.agent && (
								<span className="text-muted-foreground font-normal" data-testid="ft-model-default">
									{t("ft.modelDefault", { agent: form.agent, model: chosen.model })}
								</span>
							)}
						</div>
					</div>

					<fieldset className="flex flex-col gap-2">
						<legend className="mb-2 text-sm font-semibold">{t("ft.faults")}</legend>
						<div className="flex flex-col gap-3">
							{faultGroups.map((g) => (
								<div key={g.group} className="flex flex-col gap-1.5" data-testid={`ft-fault-group-${g.group}`}>
									<span className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{g.group}</span>
									<div className="flex flex-wrap gap-2">
										{g.faults.map((fault) => (
											<label
												key={fault.id}
												className="inline-flex max-w-sm cursor-pointer items-start gap-2 rounded-md border px-3 py-2 text-sm"
											>
												<input
													type="checkbox"
													className="mt-0.5"
													checked={form.faults.includes(fault.id)}
													onChange={(e) => setForm((f) => ({ ...f, faults: toggle(f.faults, fault.id, e.target.checked) }))}
													data-testid={`ft-fault-${fault.id}`}
												/>
												<span className="flex flex-col">
													<span className="font-medium">{fault.label}</span>
													<span className="text-muted-foreground text-xs">{fault.help}</span>
												</span>
											</label>
										))}
									</div>
								</div>
							))}
						</div>
						<p className="text-muted-foreground text-xs">{t("ft.cleanNote")}</p>
					</fieldset>

					<fieldset className="flex flex-col gap-2">
						<legend className="mb-2 text-sm font-semibold">{t("ft.benchmarks")}</legend>
						{suites.isLoading ? (
							<BlockSkeleton rows={2} testId="ft-suites-loading" />
						) : suiteList.length === 0 ? (
							<p className="text-muted-foreground text-sm">
								{t("ft.noBenchmarks")}{" "}
								<Link to="/catalog" className="text-primary underline-offset-4 hover:underline">
									{t("ft.addBenchmark")}
								</Link>
							</p>
						) : (
							<>
								{!form.agent && (
									<p className="text-muted-foreground text-xs" data-testid="ft-benchmarks-pick-agent">
										{tg("ft.pickAgentFirst")}
									</p>
								)}
								<BenchmarkChecklist
									suites={suiteList}
									compatibility={compatibility}
									checking={checking}
									selected={form.benchmarks}
									onToggle={(id) => setForm((f) => ({ ...f, benchmarks: toggle(f.benchmarks, id, !f.benchmarks.includes(id)) }))}
									testIdPrefix="ft"
								/>
							</>
						)}
					</fieldset>

					<div className="flex flex-wrap items-end gap-4">
						<NumberChoice
							label={t("ft.tasks")}
							value={form.tasks}
							options={[1, 3, 5, 10]}
							onPick={(n) => setForm((f) => ({ ...f, tasks: n }))}
							testId="ft-tasks"
						/>
						<NumberChoice
							label={t("ft.trials")}
							value={form.trials}
							options={[1, 2, 3]}
							onPick={(n) => setForm((f) => ({ ...f, trials: n }))}
							testId="ft-trials"
						/>
						<label className="flex max-w-56 flex-col gap-1 text-xs font-medium">
							{t("ft.budget")}
							<input
								type="number"
								inputMode="decimal"
								min={0}
								step={0.01}
								className="border-input bg-background h-9 w-32 rounded-md border px-2 text-sm"
								value={form.budgetText ?? ""}
								onChange={(e) => setForm((f) => ({ ...f, budgetText: e.target.value }))}
								aria-invalid={problem === "budget" || undefined}
								data-testid="ft-budget"
							/>
							<span className="text-muted-foreground font-normal">{t("ft.budgetHint", { amount: DEFAULT_BENCHMARK_BUDGET })}</span>
						</label>
					</div>
					<div className="bg-muted/40 flex flex-wrap items-center gap-4 rounded-lg border px-4 py-3">
						<FailureTestEstimate
							problem={problem}
							estimate={{ data: estimate.currentData, isFetching: estimate.isFetching, isError: estimate.isError, error: estimate.error }}
							pending={pending}
							budgetTyped={budgetTyped}
						/>
						{mayRun && (
							<Button onClick={() => void launch()} disabled={problem !== null || createState.isLoading} dataTestId="ft-launch">
								<ShieldAlert aria-hidden="true" />
								{createState.isLoading ? t("ft.launching") : t("ft.launch")}
							</Button>
						)}
					</div>
					{problem === null && estimate.data && <SkippedFaultsNote estimate={estimate.data} />}
					{createState.isError && (
						<p role="alert" className="text-destructive text-sm" data-testid="ft-launch-error">
							{getBenchErrorMessage(createState.error)}
						</p>
					)}
				</div>
			</Section>

			<Section title={t("ft.pastTitle")} description={t("ft.pastHint")} testId="ft-past">
				{evals.isLoading ? (
					<BlockSkeleton rows={3} testId="ft-past-loading" />
				) : evals.isError ? (
					<LoadError title={t("ft.pastError")} error={evals.error} onRetry={() => void evals.refetch()} testId="ft-past-error" />
				) : past.length === 0 ? (
					<EmptyNote testId="ft-past-empty">{t("ft.pastEmpty")}</EmptyNote>
				) : (
					<PastTestList evs={past} />
				)}
			</Section>
		</section>
	);
}

function NumberChoice({
	label,
	value,
	options,
	onPick,
	testId,
}: {
	label: string;
	value: number;
	options: number[];
	onPick: (n: number) => void;
	testId: string;
}) {
	return (
		<div className="flex flex-col gap-1 text-xs font-medium">
			{label}
			<div role="radiogroup" aria-label={label} className="bg-muted inline-flex rounded-lg p-0.5" data-testid={testId}>
				{options.map((n) => (
					<button
						key={n}
						type="button"
						role="radio"
						aria-checked={value === n}
						onClick={() => onPick(n)}
						className={
							value === n ? "bg-background rounded-md px-3 py-1 text-sm shadow-sm" : "text-muted-foreground rounded-md px-3 py-1 text-sm"
						}
					>
						{n}
					</button>
				))}
			</div>
		</div>
	);
}

/** A stopped (failed or cancelled) failure test with no finished benchmark, so no fault result. */
function stoppedWithoutResults(ev: Evaluation): boolean {
	return (ev.status === "failed" || ev.status === "cancelled") && !(ev.children ?? []).some((c) => c.status === "done");
}

/** The past failure tests; stopped ones with no results are hidden by default (D19), with their count. */
export function PastTestList({ evs }: { evs: readonly Evaluation[] }) {
	const { t } = useTranslation("gw08");
	const [hideStopped, setHideStopped] = useState(true);
	const stopped = evs.filter(stoppedWithoutResults).length;
	const shown = hideStopped ? evs.filter((ev) => !stoppedWithoutResults(ev)) : evs;
	return (
		<div className="flex flex-col gap-3">
			{stopped > 0 && (
				<div className="text-muted-foreground flex flex-wrap items-center gap-3 text-xs">
					<label className="flex items-center gap-1.5">
						<input
							type="checkbox"
							checked={hideStopped}
							onChange={(e) => setHideStopped(e.target.checked)}
							data-testid="ft-past-hide-stopped"
						/>
						{t("ft.hideStopped")}
					</label>
					{hideStopped && <span data-testid="ft-past-hidden-count">{t("ft.stoppedHidden", { count: stopped })}</span>}
				</div>
			)}
			<ul className="flex flex-col gap-3" data-testid="ft-past-list">
				{shown.map((ev) => (
					<PastTest key={ev.id} ev={ev} />
				))}
			</ul>
		</div>
	);
}

/** Why a finished failure test shows no fault result, by its state. */
function noResultsKey(ev: Evaluation): "ft.emptyFailed" | "ft.emptyCancelled" | "ft.emptyAllSkipped" | "ft.emptyDone" {
	if (ev.status === "failed") return "ft.emptyFailed";
	if (ev.status === "cancelled") return "ft.emptyCancelled";
	const chosen = (ev.spec?.faults ?? []).filter((f) => f !== "clean").length;
	const benches = (ev.children ?? []).length;
	if (chosen > 0 && benches > 0 && skippedFaultCount(ev) >= chosen * benches) return "ft.emptyAllSkipped";
	return "ft.emptyDone";
}

/** One past failure test: who, when, status, its faults, and the change from clean per fault once it finished. */
export function PastTest({ ev }: { ev: Evaluation }) {
	const { t } = useTranslation("agentFirst");
	const { t: tg } = useTranslation("gw08");
	const faults = (ev.spec?.faults ?? []).filter((f) => f !== "clean");
	const finished = ev.status === "done" || ev.status === "cancelled" || ev.status === "failed";
	const report = useGetEvaluationReportQuery(finished ? ev.id : skipToken);
	const label = report.data?.arms?.[0]?.label;
	const rows = (report.data?.reliability ?? []).filter((r) => label && r.arms?.[label]);
	const arm = ev.spec?.arms?.[0];
	const skipped = skippedFaultCount(ev);
	return (
		<li className="bg-card flex min-w-0 flex-col gap-3 rounded-xl border p-4" data-testid={`ft-past-${ev.id}`}>
			<div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
				<Link to="/evaluations/$evalId" params={{ evalId: ev.id }} className="text-sm font-semibold hover:underline">
					{ev.spec?.name || ev.id}
				</Link>
				<span className="text-muted-foreground text-xs">
					{[arm?.version ? `${arm.agent} v${arm.version}` : arm?.agent, arm?.model, new Date(ev.created_at).toLocaleString()]
						.filter(Boolean)
						.join(" · ")}
				</span>
				<span className="bg-muted ml-auto rounded-full px-2 py-0.5 text-xs">
					{t(`ft.status.${ev.status}`, { defaultValue: ev.status })}
				</span>
			</div>
			{faults.length > 0 && (
				<p className="text-muted-foreground text-xs" data-testid={`ft-past-${ev.id}-faults`}>
					{tg("ft.faults", { faults: faults.map((f) => faultName(String(f))).join(", ") })}
				</p>
			)}
			{skipped > 0 && (
				<p className="text-muted-foreground text-xs" data-testid={`ft-past-${ev.id}-skipped`}>
					{t("ft.pastSkipped", { count: skipped })}
				</p>
			)}
			{!finished ? (
				<p className="text-muted-foreground text-xs">{t("ft.running")}</p>
			) : report.isLoading ? (
				<BlockSkeleton rows={1} testId={`ft-past-${ev.id}-loading`} />
			) : rows.length === 0 ? (
				<p className="text-muted-foreground text-xs" data-testid={`ft-past-${ev.id}-empty`}>
					{tg(noResultsKey(ev))}
				</p>
			) : (
				<ul className="flex flex-wrap gap-2" data-testid={`ft-past-${ev.id}-results`}>
					{rows.map((r) => {
						const s = r.arms[label as string];
						const change = faultChange(s);
						const tone = change.hurt ? "text-chart-error-ink" : change.helped ? "text-chart-success-ink" : "text-muted-foreground";
						return (
							<li key={r.fault} className="rounded-md border px-2.5 py-1.5 text-xs">
								<span className="text-muted-foreground">{faultName(r.fault)}</span>{" "}
								<span className="font-semibold tabular-nums">{Math.round(s.score * 100)}%</span>{" "}
								<span className={`${tone} tabular-nums`} data-testid={`ft-past-${ev.id}-change-${r.fault}`}>
									({change.hurt || change.helped ? tg("ft.vsClean", { change: change.text }) : tg("ft.noChange")})
								</span>
							</li>
						);
					})}
				</ul>
			)}
		</li>
	);
}