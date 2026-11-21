import { AgentPicker } from "@/components/agents/agentPicker";
import { GroupedModelPicker } from "@/components/models/groupedModelPicker";
import { faultName, points, usd } from "@/components/reportcard/reportCard.utils";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Section } from "@/components/ui/section";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetAgentsQuery, useGetSuitesQuery } from "@/lib/store";
import {
	useCreateEvaluationMutation,
	useEstimateEvaluationQuery,
	useGetAgentVersionsQuery,
	useGetEvaluationReportQuery,
	useListEvaluationsQuery,
	type Evaluation,
	type EvaluationFault,
	type EvaluationSpec,
} from "@/lib/store/apis/evaluationsApi";
import { canUse, useSession } from "@/lib/session/sessionContext";
import "@/lib/types/agentFirstI18n";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link, useNavigate } from "@tanstack/react-router";
import { ShieldAlert } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/** The PS2 faults a failure test can inject, in display order. */
export type FailureFault = Exclude<EvaluationFault, "clean">;
export const FAILURE_FAULTS: FailureFault[] = ["model-down", "broken-tool", "misleading-instruction"];

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
}

/** The evaluation a failure test launches: one arm, a clean baseline plus the chosen faults. */
export function failureTestSpec(f: FailureTestForm): EvaluationSpec {
	return {
		name: `Failure test: ${f.agent}${f.version ? ` v${f.version}` : ""}`,
		mode: "score",
		arms: [{ label: "A", agent: f.agent, ...(f.version ? { version: f.version } : {}), model: f.model, ...(f.provider ? { provider: f.provider } : {}) }],
		benchmarks: f.benchmarks,
		tasks_per_benchmark: f.tasks,
		trials: f.trials,
		faults: ["clean", ...FAILURE_FAULTS.filter((x) => f.faults.includes(x))],
	};
}

/** Why Launch is off, or null when the form is complete. */
export function formProblem(f: FailureTestForm): "agent" | "model" | "benchmarks" | "faults" | null {
	if (!f.agent) return "agent";
	if (!f.model) return "model";
	if (f.benchmarks.length === 0) return "benchmarks";
	if (f.faults.length === 0) return "faults";
	return null;
}

/** Evaluations that injected at least one fault, newest first, for one agent when given. */
export function pastFailureTests(evaluations: readonly Evaluation[], agent: string, n = PAST_SHOWN): Evaluation[] {
	return evaluations
		.filter((e) => (e.spec?.faults ?? []).some((x) => x !== "clean"))
		.filter((e) => !agent || (e.spec?.arms ?? []).some((a) => a.agent === agent || a.agent.replace(/^agents\//, "").replace(/\.ya?ml(@.*)?$/, "") === agent))
		.sort((a, b) => b.created_at.localeCompare(a.created_at))
		.slice(0, n);
}

/**
 * Failure tests (/failure-tests?agent=): run the PS2 faults (model goes down, broken tool,
 * misleading instruction) on any benchmark for one agent, then see how far its score drops.
 * Launch creates an evaluation with those faults; results come from its report card.
 */
export default function FailureTestsView() {
	const { t } = useTranslation("agentFirst");
	const navigate = useNavigate();
	const mayRun = canUse(useSession(), "member");
	const [agentParam, setAgentParam] = useQueryState("agent", parseAsString.withOptions({ history: "replace" }));
	const agents = useGetAgentsQuery();
	const suites = useGetSuitesQuery();
	const evals = useListEvaluationsQuery();
	const [form, setForm] = useState<FailureTestForm>({ agent: agentParam ?? "", version: 0, provider: "", model: "", benchmarks: [], faults: [...FAILURE_FAULTS], tasks: 3, trials: 1 });
	const versions = useGetAgentVersionsQuery(form.agent || skipToken);
	const [create, createState] = useCreateEvaluationMutation();
	const problem = formProblem(form);
	const spec = failureTestSpec(form);
	const estimate = useEstimateEvaluationQuery(problem === null ? spec : skipToken);
	const summary = (agents.data?.agents ?? []).find((a) => a.id === form.agent);

	// A new agent pre-fills its default model.
	useEffect(() => {
		if (summary?.model) setForm((f) => (f.model ? f : { ...f, model: summary.model, provider: summary.gateway?.provider ?? "" }));
	}, [summary?.model, summary?.gateway?.provider]);

	const pickAgent = (id: string) => {
		setForm((f) => ({ ...f, agent: id, version: 0, model: "", provider: "" }));
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

	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="failure-tests-screen">
			<PageHeader title={t("ft.title")} subtitle={t("ft.subtitle")} />

			<Section title={t("ft.newTitle")} description={t("ft.newHint")} testId="ft-form">
				<div className="bg-card flex min-w-0 flex-col gap-6 rounded-xl border p-5">
					<div className="flex flex-wrap items-end gap-4">
						<AgentPicker agents={agents.data?.agents ?? []} value={form.agent} onChange={pickAgent} testId="ft-agent" />
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
								value={form.model || form.provider ? { provider: form.provider, model: form.model } : null}
								onChange={(v) => setForm((f) => ({ ...f, provider: v.provider, model: v.model }))}
								testId="ft-model"
							/>
						</div>
					</div>

					<fieldset className="flex flex-col gap-2">
						<legend className="mb-2 text-sm font-semibold">{t("ft.faults")}</legend>
						<div className="flex flex-wrap gap-2">
							{FAILURE_FAULTS.map((fault) => (
								<label key={fault} className="inline-flex cursor-pointer items-start gap-2 rounded-md border px-3 py-2 text-sm">
									<input
										type="checkbox"
										className="mt-0.5"
										checked={form.faults.includes(fault)}
										onChange={(e) => setForm((f) => ({ ...f, faults: toggle(f.faults, fault, e.target.checked) }))}
										data-testid={`ft-fault-${fault}`}
									/>
									<span className="flex flex-col">
										<span className="font-medium">{faultName(fault)}</span>
										<span className="text-muted-foreground text-xs">{t(`ft.faultHint.${fault}`)}</span>
									</span>
								</label>
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
							<div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
								{suiteList.map((s) => (
									<label key={s.id} className="inline-flex cursor-pointer items-start gap-2 rounded-md border px-3 py-2 text-sm">
										<input
											type="checkbox"
											className="mt-0.5"
											checked={form.benchmarks.includes(s.id)}
											onChange={(e) => setForm((f) => ({ ...f, benchmarks: toggle(f.benchmarks, s.id, e.target.checked) }))}
											data-testid={`ft-benchmark-${s.id}`}
										/>
										<span className="flex min-w-0 flex-col">
											<span className="truncate font-mono text-xs font-medium">{s.id}</span>
											{s.description && <span className="text-muted-foreground line-clamp-2 text-xs">{s.description}</span>}
										</span>
									</label>
								))}
							</div>
						)}
					</fieldset>

					<div className="flex flex-wrap items-end gap-4">
						<NumberChoice label={t("ft.tasks")} value={form.tasks} options={[1, 3, 5, 10]} onPick={(n) => setForm((f) => ({ ...f, tasks: n }))} testId="ft-tasks" />
						<NumberChoice label={t("ft.trials")} value={form.trials} options={[1, 2, 3]} onPick={(n) => setForm((f) => ({ ...f, trials: n }))} testId="ft-trials" />
						<div className="text-muted-foreground ml-auto text-sm" data-testid="ft-estimate">
							{problem !== null
								? t(`ft.need.${problem}`)
								: estimate.data
									? t("ft.estimate", {
											runs: estimate.data.runs,
											cost: estimate.data.est_cost_usd.known
												? `${usd(estimate.data.est_cost_usd.low)}-${usd(estimate.data.est_cost_usd.high)}`
												: t("ft.costUnknown"),
										})
									: estimate.isError
										? getBenchErrorMessage(estimate.error)
										: t("ft.estimating")}
						</div>
						{mayRun && (
							<Button onClick={() => void launch()} disabled={problem !== null || createState.isLoading} dataTestId="ft-launch">
								<ShieldAlert aria-hidden="true" />
								{createState.isLoading ? t("ft.launching") : t("ft.launch")}
							</Button>
						)}
					</div>
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
					<ul className="flex flex-col gap-3" data-testid="ft-past-list">
						{past.map((ev) => (
							<PastTest key={ev.id} ev={ev} />
						))}
					</ul>
				)}
			</Section>
		</section>
	);
}

function NumberChoice({ label, value, options, onPick, testId }: { label: string; value: number; options: number[]; onPick: (n: number) => void; testId: string }) {
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
						className={value === n ? "bg-background rounded-md px-3 py-1 text-sm shadow-sm" : "text-muted-foreground rounded-md px-3 py-1 text-sm"}
					>
						{n}
					</button>
				))}
			</div>
		</div>
	);
}

/** One past failure test: who, when, status, and the drop per fault once it finished. */
function PastTest({ ev }: { ev: Evaluation }) {
	const { t } = useTranslation("agentFirst");
	const finished = ev.status === "done" || ev.status === "cancelled" || ev.status === "failed";
	const report = useGetEvaluationReportQuery(finished ? ev.id : skipToken);
	const label = report.data?.arms?.[0]?.label;
	const rows = (report.data?.reliability ?? []).filter((r) => label && r.arms?.[label]);
	const arm = ev.spec?.arms?.[0];
	return (
		<li className="bg-card flex min-w-0 flex-col gap-3 rounded-xl border p-4" data-testid={`ft-past-${ev.id}`}>
			<div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
				<Link to="/evaluations/$evalId" params={{ evalId: ev.id }} className="text-sm font-semibold hover:underline">
					{ev.spec?.name || ev.id}
				</Link>
				<span className="text-muted-foreground text-xs">
					{[arm?.version ? `${arm.agent} v${arm.version}` : arm?.agent, arm?.model, new Date(ev.created_at).toLocaleString()].filter(Boolean).join(" · ")}
				</span>
				<span className="bg-muted ml-auto rounded-full px-2 py-0.5 text-xs">{t(`ft.status.${ev.status}`, { defaultValue: ev.status })}</span>
			</div>
			{!finished ? (
				<p className="text-muted-foreground text-xs">{t("ft.running")}</p>
			) : report.isLoading ? (
				<BlockSkeleton rows={1} testId={`ft-past-${ev.id}-loading`} />
			) : rows.length === 0 ? (
				<p className="text-muted-foreground text-xs">{t("ft.noResults")}</p>
			) : (
				<ul className="flex flex-wrap gap-2" data-testid={`ft-past-${ev.id}-results`}>
					{rows.map((r) => {
						const s = r.arms[label as string];
						return (
							<li key={r.fault} className="rounded-md border px-2.5 py-1.5 text-xs">
								<span className="text-muted-foreground">{faultName(r.fault)}</span>{" "}
								<span className="font-semibold tabular-nums">{Math.round(s.score * 100)}%</span>{" "}
								<span className={s.drop > 0 ? "text-chart-error-ink tabular-nums" : "text-muted-foreground tabular-nums"}>({points(-s.drop)})</span>
							</li>
						);
					})}
				</ul>
			)}
		</li>
	);
}
