import { DEFAULT_BENCHMARK_BUDGET, parseBudget } from "@/app/evaluate/views/step5Launch";
import { pageWidthClass } from "@/components/shell/pageWidth";
import { budgetLine } from "@/app/evaluate/views/budgetAdvice";
import { compatFor, compatibilityMap, incompatibleReason, keepRunnable } from "@/app/evaluate/views/step3Benchmarks";
import { suiteDisplayName } from "@/components/suites/suiteDisplay";
import { PaidButton } from "@/components/ui/paidButton";
import { useFormDraft } from "@/lib/hooks/useFormDraft";
import { useLeaveGuard } from "@/lib/hooks/useLeaveGuard";
import "@/components/runs/uf5bI18n";
import { ftFaultName } from "./gw07I18n";
import { formatUsd } from "@/lib/money";
import "@/app/schedules/views/ufrjI18n";
import { ESTIMATE_DEBOUNCE_MS, FAULTS, estimateText, groupFaults } from "@/app/evaluate/views/step4Setup";
import { useDebounced } from "@/app/evaluate/views/useDebounced";
import { BenchmarkChecklist } from "@/components/evaluations/benchmarkChecklist";
import { AgentPicker } from "@/components/agents/agentPicker";
import { GroupedModelPicker } from "@/components/models/groupedModelPicker";
import { faultChange } from "@/components/reportcard/reportCard.utils";
import "@/app/evaluations/views/gw08I18n";
import "@/app/evaluations_/$evalId/views/w4m2I18n";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { PageHeader } from "@/components/shell";
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
	type AgentCompatibility,
	type Estimate,
	type Evaluation,
	type EvaluationSpec,
} from "@/lib/store/apis/evaluationsApi";
import { canUse, useSession } from "@/lib/session/sessionContext";
import "@/lib/types/agentFirstI18n";
import type { AgentSummary } from "@/lib/types/bench";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link, useNavigate } from "@tanstack/react-router";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { evalArmLabel } from "@/lib/evaluationArms";
import { cn } from "@/lib/utils";

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

/** The most runs the form can start: every task once clean and once per fault, times repeats, per benchmark. */
export function plannedRuns(f: Pick<FailureTestForm, "benchmarks" | "faults" | "tasks" | "trials">): number {
	const faults = new Set(f.faults.filter((x) => x !== "clean")).size;
	return f.benchmarks.length * f.tasks * f.trials * (1 + faults);
}

const usd = (n: number) => `$${n.toFixed(2)}`;

/**
 * The question asked before a failure test starts, with its locale key and values: the high cost
 * estimate when the price is known, else the spending cap that stops it, else plain "price not known".
 */
export function launchConfirm(
	estimate: Pick<Estimate, "runs" | "est_cost_usd" | "budget"> | undefined,
	planned: number,
	budgetText?: string,
): { key: "ft.confirmKnown" | "ft.confirmCap" | "ft.confirmUnknown"; values: { runs: number; cost?: string; cap?: string } } {
	const runs = estimate ? Number(estimate.runs) || 0 : planned;
	const c = estimate?.est_cost_usd;
	if (c?.known) return { key: "ft.confirmKnown", values: { runs, cost: usd(Math.max(c.high, c.low)) } };
	const typed = parseBudget(budgetText ?? "");
	const cap = typed ?? estimate?.budget?.budget_usd;
	if (cap !== undefined && cap > 0) return { key: "ft.confirmCap", values: { runs, cap: usd(cap) } };
	return { key: "ft.confirmUnknown", values: { runs } };
}

/**
 * What the shared PaidButton shows for Run: the spending cap (typed budget, else the server's),
 * the high estimate when the price is known, and the run count.
 */
export function launchCost(
	estimate: Pick<Estimate, "runs" | "est_cost_usd" | "budget"> | undefined,
	planned: number,
	budgetText?: string,
): { capUsd: number | null; estimateUsd: number | null; runs: number } {
	const runs = estimate ? Number(estimate.runs) || 0 : planned;
	const c = estimate?.est_cost_usd;
	const cap = parseBudget(budgetText ?? "") ?? estimate?.budget?.budget_usd;
	return { capUsd: cap !== undefined && cap > 0 ? cap : null, estimateUsd: c?.known ? Math.max(c.high, c.low) : null, runs };
}

/**
 * When the high estimate is above the spending cap the button names (Q-295): the cap and the high
 * estimate, both in the button's own format, so the line can say the run stops at the cap. Pure.
 */
export function capNote(estimate: Pick<Estimate, "est_cost_usd"> | undefined, capUsd: number | null): { cap: string; high: string } | null {
	const c = estimate?.est_cost_usd;
	if (!c?.known || capUsd === null || !(capUsd > 0)) return null;
	const high = Math.max(c.high, c.low);
	return high > capUsd ? { cap: formatUsd(capUsd), high: formatUsd(high) } : null;
}

/** The faults the estimate says one or more picked benchmarks cannot take (Q-292). Pure. */
export function faultsSkippedAny(estimate: Pick<Estimate, "per_benchmark"> | undefined): Set<string> {
	return new Set((estimate?.per_benchmark ?? []).flatMap((b) => (b.faults_skipped ?? []).map((s) => s.fault)));
}

/** The empty form of a new failure test. */
export const EMPTY_FAILURE_TEST: FailureTestForm = {
	agent: "",
	version: 0,
	provider: "",
	model: "",
	benchmarks: [],
	faults: [...FAILURE_FAULTS],
	tasks: 3,
	trials: 1,
};

/** True when the form differs from the empty one (the leave guard asks only then). */
export function failureTestDirty(f: FailureTestForm, start: FailureTestForm = EMPTY_FAILURE_TEST): boolean {
	return JSON.stringify({ ...start, ...f }) !== JSON.stringify(start);
}

/** The benchmarks the checklist shows: matching the search, and only runnable ones when asked (once compatibility is known). */
export function visibleSuites<S extends { id: string; description?: string }>(
	suites: readonly S[],
	q: string,
	compatibility: AgentCompatibility | undefined,
	onlyRunnable: boolean,
	selected: readonly string[] = [],
): S[] {
	const needle = q.trim().toLowerCase();
	const compat = compatibilityMap(compatibility);
	return suites.filter((s) => {
		if (selected.includes(s.id)) return true;
		if (needle && !`${s.id} ${suiteDisplayName(s.id)} ${s.description ?? ""}`.toLowerCase().includes(needle)) return false;
		if (onlyRunnable && compatibility && incompatibleReason(compatFor(compat, s.id))) return false;
		return true;
	});
}

/** True while the ticked faults are exactly the ones the page opens with. */
export function isDefaultFaults(faults: readonly string[]): boolean {
	return faults.length === FAILURE_FAULTS.length && FAILURE_FAULTS.every((f) => faults.includes(f));
}

/** Faults the estimate says apply to none of the picked benchmarks: the form greys them out (M5-48). */
export function faultsNotApplicable(estimate: Pick<Estimate, "per_benchmark"> | undefined): Set<string> {
	const per = estimate?.per_benchmark ?? [];
	if (per.length === 0) return new Set();
	const sets = per.map((b) => new Set((b.faults_skipped ?? []).map((s) => s.fault)));
	return new Set([...sets[0]].filter((f) => sets.every((s) => s.has(f))));
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
export function SkippedFaultsNote({ estimate, catalog }: { estimate: Estimate; catalog?: readonly { id: string; label: string }[] }) {
	const { t } = useTranslation("ufrj");
	const lines = (estimate.per_benchmark ?? []).flatMap((b) => {
		const byReason = new Map<string, string[]>();
		for (const s of b.faults_skipped ?? []) byReason.set(s.reason, [...(byReason.get(s.reason) ?? []), ftFaultName(s.fault, catalog)]);
		// The benchmark by its display name, the raw id only as the row's title (M5-48).
		return [...byReason].map(([reason, faults]) => ({
			key: `${b.benchmark}-${reason}`,
			id: b.benchmark,
			benchmark: suiteDisplayName(b.benchmark),
			reason,
			faults,
		}));
	});
	if (lines.length === 0) return null;
	return (
		<ul className="text-muted-foreground flex flex-col gap-1 text-xs" data-testid="ft-faults-skipped">
			{lines.map((l) => (
				// The server's reason names the raw benchmark id, so it is only the row's title (Q-292).
				<li key={l.key} title={`${l.id}: ${l.reason}`}>
					{t("ft.faultsSkipped", { count: l.faults.length, faults: l.faults.join(", "), benchmark: l.benchmark })}
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
	capUsd = null,
}: {
	problem: ReturnType<typeof formProblem>;
	estimate: { data?: Estimate; isFetching?: boolean; isError?: boolean; error?: unknown };
	pending: boolean;
	budgetTyped: boolean;
	/** The cap the Run button names; the line says the run stops there when the estimate is higher. */
	capUsd?: number | null;
}) {
	const { t: tr } = useTranslation("ufrj");
	const over = problem === null && !estimate.isError ? capNote(estimate.data, capUsd) : null;
	const { t } = useTranslation("agentFirst");
	const { t: tg } = useTranslation("gw07");
	const { t: tw } = useTranslation("w4m2");
	const updating = pending || Boolean(estimate.isFetching);
	return (
		<div className="flex min-w-0 flex-1 flex-col gap-1 text-sm" data-testid="ft-estimate" aria-live="polite">
			{problem === "agent" ? (
				<span className="text-muted-foreground">{tw("toSeeCostAgent")}</span>
			) : problem !== null ? (
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
			{over && (
				<span className="text-chart-warning-ink text-xs" data-testid="ft-cap-note">
					{tr("ft.capNote", over)}
				</span>
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
	// The form is kept as a draft, so a reload, Back or a sign-in does not lose it (X-82).
	const start = { ...EMPTY_FAILURE_TEST, agent: agentParam ?? "" };
	const [draft, updateDraft, clearDraft] = useFormDraft<FailureTestForm>("failureTest", start);
	const form = draft;
	const setForm = (fn: (f: FailureTestForm) => FailureTestForm) => updateDraft(fn(form));
	const [createdId, setCreatedId] = useState<string | null>(null);
	useLeaveGuard(failureTestDirty(form, start) && createdId === null);
	useEffect(() => {
		// An agent named in the link wins over the agent of a kept draft.
		if (agentParam && agentParam !== form.agent) setForm((f) => pickAgentForm(f, agentParam));
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, []);
	useEffect(() => {
		// Navigate only after the draft is cleared, so the leave guard does not ask.
		if (createdId && !failureTestDirty(form, start)) void navigate({ href: `/evaluations/${encodeURIComponent(createdId)}` });
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [createdId, form]);
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
	const { t: tu } = useTranslation("uf5b");
	const { t: tr } = useTranslation("ufrj");
	const noAgents = agents.isSuccess && (agents.data?.agents ?? []).length === 0;
	const planned = plannedRuns(form);
	const [query, setQuery] = useState("");
	const [onlyRunnable, setOnlyRunnable] = useState(true);
	// Run starts paid work: the shared PaidButton names the cap and always asks first (M5-44, M2-111).
	const cost = launchCost(problem === null && !estimate.isError ? estimate.currentData : undefined, planned, form.budgetText);
	const launch = async () => {
		const res = await create(spec);
		if ("data" in res && res.data) {
			clearDraft();
			setCreatedId(res.data.id);
		}
	};
	const versionList = [...(versions.data?.versions ?? [])].sort((a, b) => b.version - a.version);
	const suiteList = suites.data?.suites ?? [];
	const past = pastFailureTests(evals.data ?? [], form.agent);
	const faultGroups = groupFaults(catalog.data && catalog.data.length > 0 ? catalog.data : FAULTS);
	const budgetTyped = (form.budgetText ?? "").trim() !== "";
	const shownSuites = visibleSuites(suiteList, query, compatibility, onlyRunnable, form.benchmarks);
	const faultCatalog = catalog.data && catalog.data.length > 0 ? catalog.data : FAULTS;
	// Faults a picked benchmark cannot take are greyed and unticked (Q-292). They are remembered for the
	// benchmark set, so unticking them (which changes the estimate) does not bring them back.
	const benchKey = [...form.benchmarks].sort().join(",");
	const [skipped, setSkipped] = useState<{ key: string; faults: string[] }>({ key: "", faults: [] });
	const skippedNow = problem === null && !pending ? faultsSkippedAny(estimate.currentData) : new Set<string>();
	const skippedKnown = skipped.key === benchKey ? skipped.faults : [];
	const skippedNew = [...skippedNow].filter((f) => !skippedKnown.includes(f));
	useEffect(() => {
		if (skippedNew.length === 0 && skipped.key === benchKey) return;
		const all = [...new Set([...skippedKnown, ...skippedNew])];
		setSkipped({ key: benchKey, faults: all });
		if (all.some((f) => form.faults.includes(f))) setForm((f) => ({ ...f, faults: f.faults.filter((x) => !all.includes(x)) }));
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [benchKey, skippedNew.join(",")]);
	const notApplicable = new Set([...faultsNotApplicable(problem === null ? estimate.currentData : undefined), ...skippedKnown]);
	const budgetTotal = estimate.currentData?.budget?.budget_usd || undefined;

	return (
		// On phones the Run bar is fixed to the bottom of the screen (Q-296): the padding keeps the page's end above it.
		<section className={cn(pageWidthClass("read"), "flex flex-col gap-8 pb-40 sm:pb-0")} data-page-width="read" data-testid="failure-tests-screen">
			<PageHeader title={t("ft.title")} subtitle={t("ft.subtitle")} />

			{noAgents ? (
				// Q-307: with no agent the form cannot start anything, so it shows the way forward instead.
				<EmptyState
					title={tr("ft.noAgentTitle")}
					body={tr("ft.noAgentBody")}
					action={{ label: tr("ft.connectAgent"), to: "/connect" }}
					testId="ft-no-agent"
				/>
			) : (
			<Section title={t("ft.newTitle")} description={t("ft.newHint")} testId="ft-form">
				{/* No nested card inside the section (BD-13): the fields sit on the page. */}
				<div className="flex min-w-0 flex-col gap-6">
					<div className="flex min-w-0 flex-wrap items-end gap-4">
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
						<div className="flex w-full min-w-0 flex-col gap-1 text-xs font-medium sm:w-auto sm:min-w-64">
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

					<fieldset className="flex min-w-0 flex-col gap-2">
						<legend className="mb-1 flex w-full flex-wrap items-baseline gap-x-3 text-base font-semibold">
							{t("ft.faults")}
							<span className="text-muted-foreground text-xs font-normal" data-testid="ft-faults-picked">
								{tu("ft.faultsPicked", { count: form.faults.length })}
							</span>
						</legend>
						{isDefaultFaults(form.faults) && (
							<p className="text-muted-foreground text-xs" data-testid="ft-faults-defaults">
								{tu("ft.defaultsTicked", { count: FAILURE_FAULTS.length })}
							</p>
						)}
						<div className="flex flex-col gap-2">
							{faultGroups.map((g) => {
								const ids = g.faults.map((x) => x.id);
								const all = ids.every((id) => form.faults.includes(id));
								return (
									<div key={g.group} className="flex min-w-0 flex-col gap-1 sm:flex-row sm:gap-3" data-testid={`ft-fault-group-${g.group}`}>
										<div className="flex shrink-0 items-center gap-2 sm:w-24 sm:flex-col sm:items-start sm:gap-0 sm:pt-1.5">
											<span className="text-muted-foreground text-xs font-medium">{g.group}</span>
											<button
												type="button"
												className="text-primary text-xs underline-offset-4 hover:underline"
												onClick={() =>
													setForm((f) => ({
														...f,
														faults: all ? f.faults.filter((x) => !ids.includes(x)) : [...new Set([...f.faults, ...ids])],
													}))
												}
												data-testid={`ft-fault-group-${g.group}-all`}
											>
												{all ? tu("ft.clearGroup") : tu("ft.selectAll")}
											</button>
										</div>
										<div className="grid min-w-0 flex-1 grid-cols-1 gap-x-4 gap-y-1 min-[420px]:grid-cols-2 md:grid-cols-4">
											{g.faults.map((fault) => {
												const helpId = `ft-fault-help-${fault.id}`;
												const off = notApplicable.has(fault.id) && !form.faults.includes(fault.id);
												return (
													<label
														key={fault.id}
														title={fault.help}
														className={cn(
															"inline-flex min-h-7 min-w-0 cursor-pointer items-center gap-2 text-sm",
															off && "text-muted-foreground opacity-60",
														)}
														data-not-applicable={off || undefined}
													>
														<input
															type="checkbox"
															className="accent-primary size-4 shrink-0"
															checked={form.faults.includes(fault.id)}
															disabled={off}
															onChange={(e) => setForm((f) => ({ ...f, faults: toggle(f.faults, fault.id, e.target.checked) }))}
															aria-describedby={helpId}
															data-testid={`ft-fault-${fault.id}`}
														/>
														<span className="truncate">{fault.label}</span>
														<span id={helpId} className="sr-only">
															{fault.help}
														</span>
													</label>
												);
											})}
										</div>
									</div>
								);
							})}
						</div>
						<p className="text-muted-foreground text-xs">{t("ft.cleanNote")}</p>
					</fieldset>

					<fieldset className="flex min-w-0 flex-col gap-2">
						<legend className="mb-1 flex w-full flex-wrap items-baseline gap-x-3 text-base font-semibold">
							{t("ft.benchmarks")}
							{form.benchmarks.length > 0 && (
								<span className="text-muted-foreground text-xs font-normal" data-testid="ft-benchmarks-picked">
									{tu("ft.benchmarksPicked", { count: form.benchmarks.length })}
								</span>
							)}
						</legend>
						{suites.isLoading ? (
							<BlockSkeleton rows={2} testId="ft-suites-loading" />
						) : suites.isError ? (
							<LoadError title={tu("ft.benchmarksLoadError")} error={suites.error} onRetry={() => void suites.refetch()} testId="ft-suites-error" />
						) : suiteList.length === 0 ? (
							<p className="text-muted-foreground text-sm">
								{t("ft.noBenchmarks")}{" "}
								<Link to="/catalog" className="text-primary underline-offset-4 hover:underline">
									{t("ft.addBenchmark")}
								</Link>
							</p>
						) : !form.agent ? (
							// Benchmarks wait for an agent: which ones it can run decides the list (M5-46).
							<p className="text-muted-foreground text-sm" data-testid="ft-benchmarks-pick-agent">
								{tg("ft.pickAgentFirst")}
							</p>
						) : (
							<>
								<div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
									<input
										type="search"
										value={query}
										onChange={(e) => setQuery(e.target.value)}
										placeholder={tu("ft.searchBenchmarks")}
										aria-label={tu("ft.searchBenchmarks")}
										className="border-input bg-background h-9 w-full max-w-md min-w-0 rounded-md border px-2 text-sm"
										data-testid="ft-benchmark-search"
									/>
									<label className="inline-flex items-center gap-2 py-1 text-sm">
										<input
											type="checkbox"
											className="accent-primary size-4"
											checked={onlyRunnable}
											onChange={(e) => setOnlyRunnable(e.target.checked)}
											data-testid="ft-only-runnable"
										/>
										{tu("ft.onlyRunnable")}
									</label>
								</div>
								{shownSuites.length === 0 ? (
									<p className="text-muted-foreground text-sm" data-testid="ft-benchmarks-no-match">
										{tu("ft.noMatch")}
									</p>
								) : (
									<div className="max-h-[28rem] min-w-0 overflow-y-auto rounded-md border p-1" data-testid="ft-benchmark-list">
										<BenchmarkChecklist
											suites={shownSuites}
											compatibility={compatibility}
											checking={checking}
											selected={form.benchmarks}
											onToggle={(id) => setForm((f) => ({ ...f, benchmarks: toggle(f.benchmarks, id, !f.benchmarks.includes(id)) }))}
											testIdPrefix="ft"
										/>
									</div>
								)}
							</>
						)}
					</fieldset>

					<div className="flex min-w-0 flex-wrap items-end gap-4">
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
						{planned > 0 && (
							<span className="text-sm font-medium tabular-nums sm:pb-1.5" data-testid="ft-runs-planned" aria-live="polite">
								{tu("ft.runsPlanned", { count: planned })}
							</span>
						)}
						<label className="flex max-w-56 min-w-0 flex-col gap-1 text-xs font-medium">
							{t("ft.budget")}
							<span className="relative inline-flex w-32 items-center">
								<span aria-hidden="true" className="text-muted-foreground pointer-events-none absolute left-2 text-sm">
									$
								</span>
								<input
									type="number"
									inputMode="decimal"
									min={0}
									step={0.01}
									className="border-input bg-background h-9 w-full rounded-md border pr-2 pl-5 text-sm"
									value={form.budgetText ?? ""}
									onChange={(e) => setForm((f) => ({ ...f, budgetText: e.target.value }))}
									aria-invalid={problem === "budget" || undefined}
									data-testid="ft-budget"
								/>
							</span>
							<span className="text-muted-foreground font-normal">
								{t("ft.budgetHint", { amount: DEFAULT_BENCHMARK_BUDGET })}
								{budgetTotal !== undefined && <> {tu("ft.budgetTotal", { amount: usd(budgetTotal) })}</>}
							</span>
						</label>
					</div>
					{problem === null && estimate.data && <SkippedFaultsNote estimate={estimate.data} catalog={faultCatalog} />}
					{createState.isError && (
						<p role="alert" className="text-destructive text-sm" data-testid="ft-launch-error">
							{getBenchErrorMessage(createState.error)}
						</p>
					)}
					{/* The estimate and Run stay in view while the long form scrolls (M5-44, M2-111). */}
					<div
						className="bg-background fixed inset-x-0 bottom-0 z-20 flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 border-t px-4 py-3 shadow-sm sm:sticky sm:z-10 sm:rounded-lg sm:border"
						data-testid="ft-launch-bar"
					>
						<FailureTestEstimate
							problem={problem}
							estimate={{ data: estimate.currentData, isFetching: estimate.isFetching, isError: estimate.isError, error: estimate.error }}
							pending={pending}
							budgetTyped={budgetTyped}
capUsd={cost.capUsd}
						/>
						{mayRun && (
							<PaidButton
								verb={tu("ft.runVerb")}
								capUsd={cost.capUsd}
								estimateUsd={cost.estimateUsd}
								estimateRuns={cost.runs}
								confirm="always"
								kind="form"
								estimateAt="title"
								onConfirm={launch}
								disabled={problem !== null}
								isLoading={createState.isLoading}
								testId="ft-launch"
							/>
						)}
					</div>
				</div>
			</Section>
			)}

			<Section title={t("ft.pastTitle")} description={t("ft.pastHint")} testId="ft-past">
				{evals.isLoading ? (
					<BlockSkeleton rows={3} testId="ft-past-loading" />
				) : evals.isError ? (
					<LoadError title={t("ft.pastError")} error={evals.error} onRetry={() => void evals.refetch()} testId="ft-past-error" />
				) : past.length === 0 ? (
					<EmptyState compact body={t("ft.pastEmpty")} testId="ft-past-empty" />
				) : (
					<PastTestList evs={past} catalog={faultCatalog} />
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
export function PastTestList({ evs, catalog }: { evs: readonly Evaluation[]; catalog?: readonly { id: string; label: string }[] }) {
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
					<PastTest key={ev.id} ev={ev} catalog={catalog} />
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
export function PastTest({ ev, catalog }: { ev: Evaluation; catalog?: readonly { id: string; label: string }[] }) {
	const { t } = useTranslation("agentFirst");
	const { t: tg } = useTranslation("gw08");
	const { t: tu } = useTranslation("uf5b");
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
				{/* Titled by agent and date, the whole title a link to its evaluation (M5-47). */}
				<Link
					to="/evaluations/$evalId"
					params={{ evalId: ev.id }}
					className="text-sm font-semibold hover:underline"
					title={ev.spec?.name || ev.id}
				>
					{tu("ft.pastTitle", {
						agent: arm?.version ? `${arm.agent} v${arm.version}` : arm?.agent || ev.spec?.name || ev.id,
						date: new Date(ev.created_at).toLocaleString(),
					})}
				</Link>
				{arm?.model && <span className="text-muted-foreground text-xs">{arm.model}</span>}
				<span className="bg-muted ml-auto rounded-full px-2 py-0.5 text-xs">
					{t(`ft.status.${ev.status}`, { defaultValue: ev.status })}
				</span>
			</div>
			{faults.length > 0 && (
				<p className="text-muted-foreground text-xs" data-testid={`ft-past-${ev.id}-faults`}>
					{tg("ft.faults", { faults: faults.map((f) => ftFaultName(String(f), catalog)).join(", ") })}
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
				<table className="w-full max-w-lg text-xs" data-testid={`ft-past-${ev.id}-results`}>
					<thead className="text-muted-foreground text-left">
						<tr>
							<th className="py-1 pr-3 font-medium">{tu("ft.pastFault")}</th>
							<th className="py-1 pr-3 text-right font-medium">{tu("ft.pastScore")}</th>
							<th className="py-1 text-right font-medium">{tu("ft.pastChange")}</th>
						</tr>
					</thead>
					<tbody>
						{rows.map((r) => {
							const s = r.arms[label as string];
							const change = faultChange(s);
							const tone = change.hurt ? "text-chart-error-ink" : change.helped ? "text-chart-success-ink" : "text-muted-foreground";
							return (
								<tr key={r.fault} className="border-t">
									<td className="py-1 pr-3">{ftFaultName(r.fault, catalog)}</td>
									<td className="py-1 pr-3 text-right font-semibold tabular-nums">{Math.round(s.score * 100)}%</td>
									<td className={`${tone} py-1 text-right tabular-nums`} data-testid={`ft-past-${ev.id}-change-${r.fault}`}>
										{change.hurt || change.helped ? tg("ft.vsClean", { change: change.text }) : tg("ft.noChange")}
									</td>
								</tr>
							);
						})}
					</tbody>
				</table>
			)}
		</li>
	);
}