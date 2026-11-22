import "./builderI18n";
import "@/app/experiments_/$expId/views/fw11I18n";

import { readWriteToken, saveWriteToken } from "@/components/experiments/experiments.utils";
import { EstimateBox } from "@/components/experiments/estimateBox";
import { PlanResult, planBlocksStart } from "@/components/experiments/planResult";
import { ModelPicker, type ModelRef } from "@/components/models/modelPicker";
import { LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useSession, usesWriteToken } from "@/lib/session/sessionContext";
import {
	useCreateExperimentMutation,
	useEstimateExperimentMutation,
	usePlanExperimentMutation,
	useStartExperimentMutation,
} from "@/lib/store/apis/benchApi";
import type { Catalog, CatalogAgent, Experiment, ExperimentPlan } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ChevronDown, Plus, X } from "lucide-react";
import { useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { suiteRef } from "./experimentFormSchema";
import {
	builderAgentGroups,
	builderPlanIsStale,
	catalogAgent,
	guessProvider,
	matrixPreview,
	readyIds,
	someNotReady,
	toMatrixExperiment,
	validateBuilder,
	type BuilderAgentIds,
	type ReadyTasks,
	type BuilderErrorKey,
	type BuilderValues,
} from "./matrixBuilderSchema";
import { SuiteMenuItems } from "./suiteOptions";

export interface MatrixBuilderProps {
	catalog: Catalog;
	defaults: BuilderValues;
	/** The agent ids My agents lists (GET /agents split by splitAgents); absent lists every catalog agent. */
	agentGroups?: BuilderAgentIds;
	/** Ready tasks per benchmark (GET /suites): task counts and the tasks sent use only these (N42). */
	ready?: ReadyTasks;
	/** Called once the experiment is created and started (or created with a failed start), with its id and cap. */
	onStarted: (expId: string, capUsd: number) => void;
}

/** The no-YAML builder: agents x models x suite, adaptive toggle, preview, estimate, plan and run. */
export function MatrixBuilder({ catalog, defaults, agentGroups, ready, onStarted }: MatrixBuilderProps) {
	const { t } = useTranslation("builder");
	const { t: tf } = useTranslation("fw11");
	const [v, setV] = useState<BuilderValues>(defaults);
	const [touched, setTouched] = useState(false);
	const [token, setToken] = useState(readWriteToken);
	const tokenBox = usesWriteToken(useSession());
	const [planned, setPlanned] = useState<{
		experiment: Experiment;
		plan: ExperimentPlan;
	} | null>(null);
	const [estimated, setEstimated] = useState<Experiment | null>(null);
	const [actionError, setActionError] = useState<{
		title: string;
		error: unknown;
	} | null>(null);
	const [plan, planState] = usePlanExperimentMutation();
	const [estimate, estimateState] = useEstimateExperimentMutation();
	const [create, createState] = useCreateExperimentMutation();
	const [start, startState] = useStartExperimentMutation();

	const suite = catalog.suites.find((s) => suiteRef(s) === v.suite);
	const errors = useMemo(() => validateBuilder(v, suite, ready), [v, suite, ready]);
	const valid = Object.keys(errors).length === 0;
	const shown = touched ? errors : {};
	const preview = useMemo(() => matrixPreview(v, suite, ready), [v, suite, ready]);
	const runnable = readyIds(suite, ready).length;
	const partlyReady = someNotReady(suite, ready);
	const [extrasOpen, setExtrasOpen] = useState(false);
	// A field error inside the collapsed Extras opens it, so the message is never hidden.
	const extrasError = Boolean(shown.adaptiveMin || shown.concurrency || shown.seed);
	const set = <K extends keyof BuilderValues>(k: K, value: BuilderValues[K]) => setV((prev) => ({ ...prev, [k]: value }));

	// The agents My agents shows: the org's own, then the built-in ones; a chosen agent stays listed.
	const listed = useMemo(() => {
		const g = builderAgentGroups(catalog, agentGroups);
		const known = new Set([...g.yours, ...g.builtIn].map((a) => a.path));
		const extra = catalog.agents.filter((a) => v.agents.includes(a.path) && !known.has(a.path));
		return { yours: [...g.yours, ...extra], builtIn: g.builtIn };
	}, [catalog, agentGroups, v.agents]);
	const agentChoice = (a: CatalogAgent) => {
		const on = v.agents.includes(a.path);
		return (
			<li key={a.path}>
				<label
					className={cn("flex cursor-pointer items-center gap-2 rounded-sm border px-3 py-2 text-sm", on && "border-primary bg-primary/10")}
				>
					<input
						type="checkbox"
						className="accent-primary size-4"
						checked={on}
						onChange={(e) => toggleAgent(a.path, e.target.checked)}
						data-testid={`builder-agent-${a.id}`}
					/>
					<span className="font-mono">{a.id}</span>
					{a.model && <span className="text-muted-foreground text-xs">{t("fields.agentModel", { model: a.model })}</span>}
				</label>
			</li>
		);
	};
	const needsToken = tokenBox && !token.trim();
	const stale = planned !== null && builderPlanIsStale(planned.experiment, v, catalog, ready);
	const estimateFresh = estimated !== null && !builderPlanIsStale(estimated, v, catalog, ready);
	// N43: after a Plan or Estimate press on an invalid form, how many fields to fix, next to the buttons.
	const invalidCount = touched ? Object.keys(errors).length : 0;
	const busy = createState.isLoading || startState.isLoading;
	const blockReason = needsToken
		? t("actions.needsToken")
		: !valid && touched
			? t("actions.invalid")
			: !planned
				? t("actions.needsPlan")
				: stale
					? t("actions.planStale")
					: planBlocksStart(planned.plan)
						? t("actions.blocked")
						: null;

	const experimentOrNull = (): Experiment | null => {
		setTouched(true);
		if (!valid && typeof document !== "undefined") {
			// Bring the first field error into view once it renders (N43).
			setTimeout(() => {
				document
					.querySelector('[data-testid="matrix-builder"] [data-testid^="builder-error-"]')
					?.scrollIntoView({ block: "center", behavior: "smooth" });
			}, 0);
		}
		if (!valid || needsToken) return null;
		return toMatrixExperiment(v, catalog, ready);
	};

	const onEstimate = async () => {
		const exp = experimentOrNull();
		if (!exp) return;
		setEstimated(exp);
		try {
			await estimate(exp).unwrap();
		} catch {
			// estimateState.error renders it.
		}
	};

	const onPlan = async () => {
		const exp = experimentOrNull();
		if (!exp) return;
		setActionError(null);
		setPlanned(null);
		try {
			setPlanned({ experiment: exp, plan: await plan(exp).unwrap() });
		} catch {
			// planState.error renders it.
		}
	};

	const onRun = async () => {
		if (!planned || blockReason) return;
		setActionError(null);
		let expId: string;
		try {
			expId = (await create(planned.experiment).unwrap()).exp_id;
		} catch (error) {
			setActionError({ title: t("actions.createError"), error });
			return;
		}
		try {
			await start(expId).unwrap();
		} catch (error) {
			setActionError({ title: t("actions.startError", { id: expId }), error });
		}
		// One plan, one experiment: a second run needs a new plan, so a double click never creates two.
		onStarted(expId, planned.experiment.budget.total_usd);
		setPlanned(null);
	};

	const setModel = (i: number, m: ModelRef) =>
		set(
			"models",
			v.models.map((row, j) => (j === i ? m : row)),
		);
	const toggleAgent = (path: string, on: boolean) => set("agents", on ? [...v.agents, path] : v.agents.filter((p) => p !== path));
	const toggleProbe = (id: string, on: boolean) => set("probes", on ? [...v.probes, id] : v.probes.filter((p) => p !== id));

	return (
		<div
			className="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_22rem] xl:grid-cols-[minmax(0,1fr)_24rem]"
			data-testid="matrix-builder"
		>
			<div className="flex min-w-0 flex-col gap-4">
				<Step n={1} title={t("steps.what")} hint={t("steps.whatHint")}>
					<div className="grid gap-4 md:grid-cols-2">
						<Field label={t("fields.name")} error={shown.name}>
							{(id) => <Input id={id} value={v.name} onChange={(e) => set("name", e.target.value)} data-testid="builder-name-input" />}
						</Field>
						<Field label={t("fields.suite")} error={shown.suite}>
							{(id) => (
								<Select
									value={v.suite}
									onValueChange={(next) => {
										const s = catalog.suites.find((x) => suiteRef(x) === next);
										setV((prev) => ({
											...prev,
											suite: next,
											probes: [],
											taskCount: Math.min(prev.taskCount, Math.max(1, readyIds(s, ready).length)),
										}));
									}}
								>
									<SelectTrigger id={id} className="w-full" data-testid="builder-suite-select">
										<SelectValue />
									</SelectTrigger>
									<SelectContent>
										<SuiteMenuItems catalog={catalog} ready={ready} testIdPrefix="builder-suite-option" />
									</SelectContent>
								</Select>
							)}
						</Field>
					</div>
				</Step>

				<Step n={2} title={t("steps.who")} hint={t("steps.whoHint")}>
					<SubSection title={t("sections.agents")} description={t("sections.agentsHint")} error={shown.agents}>
						{agentGroups && <p className="text-muted-foreground text-xs font-medium">{tf("builder.yourAgents")}</p>}
						<ul className="flex flex-wrap gap-2" data-testid="builder-agents">
							{listed.yours.map((a) => agentChoice(a))}
						</ul>
						{listed.builtIn.length > 0 && (
							<>
								<p className="text-muted-foreground text-xs font-medium">{tf("builder.builtInAgents")}</p>
								<ul className="flex flex-wrap gap-2" data-testid="builder-agents-builtin">
									{listed.builtIn.map((a) => agentChoice(a))}
								</ul>
							</>
						)}
					</SubSection>
					<SubSection title={t("sections.models")} description={t("sections.modelsHint")} error={shown.models}>
						<ul className="flex flex-col gap-2" data-testid="builder-models">
							{v.models.map((m, i) => (
								<li key={i} className="flex flex-wrap items-center gap-2">
									<span className="text-muted-foreground w-5 text-xs">{i + 1}.</span>
									<ModelPicker value={m} purpose="agent" onChange={(next) => setModel(i, next)} testId={`builder-model-${i}`} />
									<Button
										type="button"
										variant="ghost"
										size="sm"
										aria-label={t("fields.removeModel", { n: i + 1 })}
										onClick={() =>
											set(
												"models",
												v.models.filter((_, j) => j !== i),
											)
										}
										dataTestId={`builder-model-remove-${i}`}
									>
										<X aria-hidden="true" />
									</Button>
									{shown[`models.${i}`] && <ErrorText code={shown[`models.${i}`]!} />}
								</li>
							))}
						</ul>
						<div className="flex flex-wrap items-center gap-4">
							<Button
								type="button"
								variant="outline"
								size="sm"
								className="gap-1.5"
								onClick={() => {
									const last = v.models[v.models.length - 1];
									const agentModel = catalogAgent(catalog, v.agents[0] ?? "")?.model ?? "";
									set("models", [
										...v.models,
										{
											provider: last?.provider ?? guessProvider(agentModel) ?? "openai",
											model: "",
										},
									]);
								}}
								dataTestId="builder-model-add"
							>
								<Plus aria-hidden="true" />
								{t("fields.addModel")}
							</Button>
							<label className="flex items-center gap-2 text-xs">
								<input
									type="checkbox"
									className="accent-primary"
									checked={v.allowUnpriced}
									onChange={(e) => set("allowUnpriced", e.target.checked)}
									data-testid="builder-allow-unpriced"
								/>
								{t("fields.allowUnpriced")}
							</label>
						</div>
					</SubSection>
				</Step>

				<Step n={3} title={t("steps.size")} hint={t("steps.sizeHint")}>
					<div className="grid gap-5 md:grid-cols-3">
						<div className="flex flex-col gap-1.5 md:col-span-3">
							<span className="text-xs font-medium">{t("fields.tasks")}</span>
							<div className="flex flex-col gap-2 text-sm" role="radiogroup" data-testid="builder-task-scope">
								<label className="flex items-center gap-2">
									<input
										type="radio"
										className="accent-primary"
										checked={v.taskScope === "first"}
										onChange={() => set("taskScope", "first")}
										data-testid="builder-tasks-first"
									/>
									{t("fields.tasksFirst")}
									<Input
										type="number"
										min={1}
										max={runnable}
										className="h-8 w-20"
										disabled={v.taskScope !== "first"}
										value={Number.isNaN(v.taskCount) ? "" : v.taskCount}
										onChange={(e) => set("taskCount", e.target.valueAsNumber)}
										data-testid="builder-task-count"
									/>
									<span className="text-muted-foreground text-xs">
										{partlyReady
											? t("fields.tasksFirstSuffixReady", { count: runnable })
											: t("fields.tasksFirstSuffix", { count: runnable })}
									</span>
								</label>
								<label className="flex items-center gap-2">
									<input
										type="radio"
										className="accent-primary"
										checked={v.taskScope === "all"}
										onChange={() => set("taskScope", "all")}
										data-testid="builder-tasks-all"
									/>
									{partlyReady ? t("fields.tasksAllReady", { count: runnable }) : t("fields.tasksAll", { count: runnable })}
								</label>
								{shown.taskCount && <ErrorText code={shown.taskCount} />}
							</div>
						</div>
						<Field label={t("fields.trials")} error={shown.trials}>
							{(id) => (
								<Input
									id={id}
									type="number"
									min={1}
									max={50}
									value={Number.isNaN(v.trials) ? "" : v.trials}
									onChange={(e) => set("trials", e.target.valueAsNumber)}
									data-testid="builder-trials"
								/>
							)}
						</Field>
						<NumberField
							label={t("fields.totalUsd")}
							value={v.totalUsd}
							step="0.01"
							error={shown.totalUsd}
							onChange={(n) => set("totalUsd", n)}
							testId="builder-total-usd"
						/>
					</div>
				</Step>

				<details
					className="group bg-card rounded-xl border"
					open={extrasOpen || extrasError}
					onToggle={(e) => setExtrasOpen(e.currentTarget.open)}
					data-testid="builder-extras"
				>
					<summary className="hover:bg-muted/40 flex cursor-pointer list-none items-center gap-3 rounded-xl px-5 py-4 select-none [&::-webkit-details-marker]:hidden">
						<span className="bg-muted text-muted-foreground flex size-7 shrink-0 items-center justify-center rounded-full text-xs font-semibold">
							4
						</span>
						<span className="flex min-w-0 flex-1 flex-col">
							<span className="text-sm font-semibold tracking-tight">{t("steps.extras")}</span>
							<span className="text-muted-foreground text-xs">{t("steps.extrasHint")}</span>
						</span>
						<ChevronDown className="text-muted-foreground size-4 transition-transform group-open:rotate-180" aria-hidden="true" />
					</summary>
					<div className="flex flex-col gap-5 border-t px-5 py-5">
						<label className="flex items-start gap-2 text-sm">
							<input
								type="checkbox"
								className="accent-primary mt-1"
								checked={v.adaptive}
								onChange={(e) => {
									const on = e.target.checked;
									setV((prev) => ({
										...prev,
										adaptive: on,
										trials: on && !(prev.trials >= 2) ? 3 : prev.trials,
										adaptiveMin: 1,
									}));
								}}
								data-testid="builder-adaptive"
							/>
							<span className="flex flex-col">
								{t("fields.adaptive")}
								<span className="text-muted-foreground text-xs">{t("fields.adaptiveHint")}</span>
							</span>
						</label>
						{v.adaptive && (
							<Field label={t("fields.adaptiveMin")} error={shown.adaptiveMin}>
								{(id) => (
									<Input
										id={id}
										type="number"
										min={1}
										value={Number.isNaN(v.adaptiveMin) ? "" : v.adaptiveMin}
										onChange={(e) => set("adaptiveMin", e.target.valueAsNumber)}
										data-testid="builder-adaptive-min"
									/>
								)}
							</Field>
						)}
						{(suite?.probes?.length ?? 0) > 0 && (
							<div className="flex flex-col gap-1.5">
								<span className="text-xs font-medium">{t("fields.probes")}</span>
								<div className="flex flex-wrap gap-1.5">
									{suite!.probes.map((p) => (
										<label
											key={p}
											className={cn(
												"flex cursor-pointer items-center gap-1.5 rounded-sm border px-2 py-1 font-mono text-xs",
												v.probes.includes(p) && "border-primary bg-primary/10",
											)}
										>
											<input
												type="checkbox"
												className="accent-primary"
												checked={v.probes.includes(p)}
												onChange={(e) => toggleProbe(p, e.target.checked)}
												data-testid={`builder-probe-${p}`}
											/>
											{p}
										</label>
									))}
								</div>
								<p className="text-muted-foreground text-[11px]">{t("fields.probesHint")}</p>
							</div>
						)}
						<div className="grid gap-4 sm:grid-cols-3">
							<NumberField
								label={t("fields.concurrency")}
								value={v.concurrency}
								error={shown.concurrency}
								onChange={(n) => set("concurrency", n)}
								testId="builder-concurrency"
							/>
							<NumberField
								label={t("fields.seed")}
								value={v.seed}
								error={shown.seed}
								onChange={(n) => set("seed", n)}
								testId="builder-seed"
							/>
							{!tokenBox && (
								<Field label={t("fields.runnerPool")}>
									{(id) => (
										<Input
											id={id}
											placeholder="default"
											value={v.runnerPool}
											onChange={(e) => set("runnerPool", e.target.value)}
											data-testid="builder-runner-pool"
										/>
									)}
								</Field>
							)}
						</div>
					</div>
				</details>
			</div>

			<aside className="flex flex-col gap-4 lg:sticky lg:top-4" data-testid="builder-summary">
				<Card className="gap-4 rounded-xl py-5 shadow-sm" data-testid="builder-preview">
					<CardHeader className="px-5">
						<CardTitle className="text-base">{t("preview.title")}</CardTitle>
						<CardDescription className="text-xs">{t("preview.description")}</CardDescription>
					</CardHeader>
					<CardContent className="flex flex-col gap-4 px-5">
						{preview.arms.length === 0 || (preview.arms.length === 1 && !preview.arms[0].label) ? (
							<p className="text-muted-foreground text-xs" data-testid="builder-preview-empty">
								{t("preview.empty")}
							</p>
						) : (
							<ol className="flex max-h-64 flex-col divide-y overflow-y-auto rounded-lg border text-xs" data-testid="builder-preview-arms">
								{preview.arms.map((arm) => (
									<li key={arm.id} className="flex items-center gap-2 px-2.5 py-1.5" data-testid={`builder-arm-${arm.id}`}>
										<span className="bg-muted rounded-sm px-1.5 font-mono font-semibold">{arm.id}</span>
										<span className="truncate font-mono" title={arm.label || arm.agent}>
											{arm.label || arm.agent}
										</span>
									</li>
								))}
							</ol>
						)}
						<dl className="grid grid-cols-2 gap-3 text-xs">
							<div className="bg-muted/50 flex flex-col gap-0.5 rounded-lg px-3 py-2">
								<dt className="text-muted-foreground">{t("summary.runs")}</dt>
								<dd className="text-base font-semibold tabular-nums">{preview.maxRuns}</dd>
							</div>
							<div className="bg-muted/50 flex flex-col gap-0.5 rounded-lg px-3 py-2">
								<dt className="text-muted-foreground">{t("summary.budget")}</dt>
								<dd className="text-base font-semibold tabular-nums">{Number.isFinite(v.totalUsd) ? `$${v.totalUsd}` : "-"}</dd>
							</div>
						</dl>
						<p className="text-muted-foreground text-[11px]" data-testid="builder-preview-runs">
							{t("preview.arms", { count: preview.arms.length })}
							{", "}
							{t("preview.runs", {
								runs: preview.maxRuns,
								arms: preview.arms.length,
								conditions: preview.conditions,
								tasks: preview.tasks,
								trials: v.trials,
							})}
						</p>
					</CardContent>
					<CardContent className="flex flex-col gap-3 border-t px-5 pt-4" data-testid="builder-actions">
						{tokenBox && (
							<Field label={t("fields.token")} hint={t("fields.tokenHint")}>
								{(id) => (
									<Input
										id={id}
										type="password"
										autoComplete="off"
										placeholder={t("fields.tokenPlaceholder")}
										value={token}
										onChange={(e) => {
											setToken(e.target.value);
											saveWriteToken(e.target.value);
										}}
										data-testid="builder-write-token"
									/>
								)}
							</Field>
						)}
						<div className="flex flex-wrap gap-2">
							<Button
								type="button"
								variant="outline"
								size="sm"
								disabled={needsToken}
								isLoading={estimateState.isLoading}
								onClick={() => void onEstimate()}
								dataTestId="builder-estimate"
							>
								{t("actions.estimate")}
							</Button>
							<Button
								type="button"
								variant="outline"
								size="sm"
								disabled={needsToken}
								isLoading={planState.isLoading}
								onClick={() => void onPlan()}
								dataTestId="builder-plan"
							>
								{t("actions.plan")}
							</Button>
							<Button
								type="button"
								size="sm"
								disabled={blockReason !== null || busy}
								isLoading={busy}
								onClick={() => void onRun()}
								dataTestId="builder-run"
							>
								{t("actions.run")}
							</Button>
						</div>
						{invalidCount > 0 && (
							<p role="alert" className="text-chart-error-ink text-xs" data-testid="builder-invalid">
								{t("actions.fixFields", { count: invalidCount })}
							</p>
						)}
						{blockReason && (
							<p className={cn("text-xs", stale ? "text-chart-warning-ink" : "text-muted-foreground")} data-testid="builder-blocked-reason">
								{blockReason}
							</p>
						)}
						{estimateState.error && !estimateState.isLoading && (
							<LoadError title={t("actions.estimateError")} error={estimateState.error} testId="builder-estimate-error" />
						)}
						{estimated && estimateState.data && !estimateState.isLoading && (
							<section
								className={cn("flex flex-col gap-2 border-t pt-3", !estimateFresh && "opacity-60")}
								data-testid="builder-estimate-result"
							>
								<h3 className="text-sm font-semibold">{t("estimate.title")}</h3>
								<EstimateBox forecast={estimateState.data} />
							</section>
						)}
						{planState.error && !planState.isLoading && (
							<LoadError title={t("actions.planError")} error={planState.error} testId="builder-plan-error" />
						)}
						{actionError && <LoadError title={actionError.title} error={actionError.error} testId="builder-action-error" />}
						{planned && (
							<PlanResult
								plan={planned.plan}
								estimateShown={estimateFresh && Boolean(estimateState.data) && !estimateState.isLoading}
								className={cn(stale && "opacity-60")}
							/>
						)}
					</CardContent>
				</Card>
			</aside>
		</div>
	);
}

/** One numbered step of the builder: a card with its number, a short title and a one-line hint. */
function Step({ n, title, hint, children }: { n: number; title: string; hint?: string; children: React.ReactNode }) {
	return (
		<section className="bg-card flex flex-col gap-5 rounded-xl border p-5" data-testid={`builder-step-${n}`}>
			<div className="flex items-center gap-3">
				<span className="bg-primary/10 text-primary flex size-7 shrink-0 items-center justify-center rounded-full text-xs font-semibold">
					{n}
				</span>
				<div className="flex min-w-0 flex-col">
					<h2 className="text-sm font-semibold tracking-tight">{title}</h2>
					{hint && <p className="text-muted-foreground text-xs">{hint}</p>}
				</div>
			</div>
			{children}
		</section>
	);
}

function SubSection({
	title,
	description,
	error,
	children,
}: {
	title: string;
	description?: string;
	error?: BuilderErrorKey;
	children: React.ReactNode;
}) {
	return (
		<div className="flex flex-col gap-2.5">
			<div className="flex flex-col gap-0.5">
				<h3 className="text-xs font-medium">{title}</h3>
				{description && <p className="text-muted-foreground text-xs">{description}</p>}
			</div>
			{children}
			{error && <ErrorText code={error} />}
		</div>
	);
}

function Field({
	label,
	hint,
	error,
	children,
}: {
	label: string;
	hint?: string;
	error?: BuilderErrorKey;
	children: (id: string) => React.ReactNode;
}) {
	const id = useId();
	return (
		<div className="flex min-w-0 flex-col gap-1.5">
			<label htmlFor={id} className="text-xs font-medium">
				{label}
			</label>
			{children(id)}
			{hint && <p className="text-muted-foreground text-[11px]">{hint}</p>}
			{error && <ErrorText code={error} />}
		</div>
	);
}

function NumberField({
	label,
	value,
	step,
	error,
	onChange,
	testId,
}: {
	label: string;
	value: number;
	step?: string;
	error?: BuilderErrorKey;
	onChange: (n: number) => void;
	testId: string;
}) {
	return (
		<Field label={label} error={error}>
			{(id) => (
				<Input
					id={id}
					type="number"
					step={step}
					value={Number.isNaN(value) ? "" : value}
					onChange={(e) => onChange(e.target.valueAsNumber)}
					data-testid={testId}
				/>
			)}
		</Field>
	);
}

function ErrorText({ code }: { code: BuilderErrorKey }) {
	const { t } = useTranslation("builder");
	return (
		<p role="alert" className="text-chart-error-ink text-xs" data-testid={`builder-error-${code}`}>
			{t(`errors.${code}`)}
		</p>
	);
}