import "./builderI18n";
import "@/app/experiments_/$expId/views/fw11I18n";
import "@/components/migration/w4b06I18n";

import { readWriteToken, saveWriteToken } from "@/components/experiments/experiments.utils";
import { EstimateBox } from "@/components/experiments/estimateBox";
import { PlanResult, planBlocksStart } from "@/components/experiments/planResult";
import { PaidButton } from "@/components/ui/paidButton";
import { useLeaveGuard } from "@/lib/hooks/useLeaveGuard";
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
import { ChevronDown, Loader2, Plus, X } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Checkbox } from "@/components/ui/checkbox";
import { Link } from "@tanstack/react-router";
import "@/app/leaderboard/views/uf4bI18n";
import { suiteRef } from "./experimentFormSchema";
import "@/app/compare/views/w4m3I18n";
import {
	builderAgentGroups,
	builderPlanIsStale,
	catalogAgent,
	fillUntouched,
	guessProvider,
	matrixPreview,
	readyIds,
	readyUnknown,
	someNotReady,
	toMatrixExperiment,
	validateBuilder,
	type BuilderAgentIds,
	type ReadyTasks,
	type BuilderErrorKey,
	type BuilderValues,
	clearDefaultModels,
} from "./matrixBuilderSchema";
import { SuiteMenuItems } from "./suiteOptions";

export interface MatrixBuilderProps {
	catalog: Catalog;
	defaults: BuilderValues;
	/** The agent ids My agents lists (GET /agents split by splitAgents); absent lists every catalog agent. */
	agentGroups?: BuilderAgentIds;
	/** Ready tasks per benchmark (GET /suites?fields=light): task counts and the tasks sent use only these (N42). */
	ready?: ReadyTasks;
	/** The ready list is still loading: Estimate, Plan and Run wait for it (N195). */
	readyPending?: boolean;
	/** Called once the experiment is created and started (or created with a failed start), with its id and cap. */
	onStarted: (expId: string, capUsd: number) => void;
}

/**
 * Runs the estimate call and, when the server answers 503 (it is still reading a big benchmark's
 * tasks and sends Retry-After: 5), waits 5 seconds and tries once more. Any other error, or a
 * second 503, is thrown to the caller.
 */
export async function estimateWithOneRetry<T>(call: () => Promise<T>, wait: (ms: number) => Promise<void>): Promise<T> {
	try {
		return await call();
	} catch (err) {
		if ((err as { status?: unknown } | null)?.status !== 503) throw err;
		await wait(5000);
		return await call();
	}
}

/** The no-YAML builder: agents x models x suite, adaptive toggle, preview, estimate, plan and run. */
export function MatrixBuilder({ catalog, defaults, agentGroups, ready, readyPending, onStarted }: MatrixBuilderProps) {
	const { t } = useTranslation("builder");
	const { t: tf } = useTranslation("fw11");
	const [v, setV] = useState<BuilderValues>(defaults);
	// L2: the fields the person changed. New defaults (the ready list arrived) fill only the others; no remount.
	const touchedKeys = useRef(new Set<keyof BuilderValues>());
	const shownDefaults = useRef(defaults);
	useEffect(() => {
		if (shownDefaults.current === defaults) return;
		shownDefaults.current = defaults;
		const touched = touchedKeys.current;
		setV((prev) => {
			const out = fillUntouched(prev, defaults, (k) => touched.has(k) || (touched.has("suite") && k === "probes"));
			if (touched.has("suite") && !touched.has("taskCount")) {
				// A benchmark picked by hand keeps its own size: up to 5 of its ready tasks.
				const s = catalog.suites.find((x) => suiteRef(x) === out.suite);
				out.taskCount = Math.min(5, Math.max(1, readyIds(s, ready).length));
			}
			return out;
		});
	}, [defaults, catalog, ready]);
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
		/** A comparison that was created but did not start (M4-81): linked from the error. */
		createdId?: string;
	} | null>(null);
	const { t: tu } = useTranslation("uf4b");
	const [plan, planState] = usePlanExperimentMutation();
	const [estimate, estimateState] = useEstimateExperimentMutation();
	const [estimateRetrying, setEstimateRetrying] = useState(false);
	const { t: tw } = useTranslation("w4b06");
	const estimating = estimateState.isLoading || estimateRetrying;
	const [create, createState] = useCreateExperimentMutation();
	const [start, startState] = useStartExperimentMutation();
	// M4-88: leaving with changes asks first; a started comparison has nothing left to lose.
	const [started, setStarted] = useState(false);
	useLeaveGuard(!started && builderDirty(v, defaults));

	const suite = catalog.suites.find((s) => suiteRef(s) === v.suite);
	const errors = useMemo(() => validateBuilder(v, suite, ready), [v, suite, ready]);
	const valid = Object.keys(errors).length === 0;
	const shown = touched ? errors : {};
	const preview = useMemo(() => matrixPreview(v, suite, ready), [v, suite, ready]);
	const runnable = readyIds(suite, ready).length;
	// Ready tasks known (N195): the counts say "ready tasks", since only those are sent.
	const partlyReady = someNotReady(suite, ready) || Boolean(suite && ready?.has(suiteRef(suite)));
	const [extrasOpen, setExtrasOpen] = useState(false);
	// A field error inside the collapsed Extras opens it, so the message is never hidden.
	const extrasError = Boolean(shown.adaptiveMin || shown.concurrency || shown.seed);
	const set = <K extends keyof BuilderValues>(k: K, value: BuilderValues[K]) => {
		touchedKeys.current.add(k);
		setV((prev) => ({ ...prev, [k]: value }));
	};

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
					className={cn(
						"flex min-w-0 cursor-pointer items-start gap-2 rounded-md border px-3 py-2 text-sm",
						on && "border-primary bg-primary/10",
					)}
				>
					<input
						type="checkbox"
						className="accent-primary mt-0.5 size-4 shrink-0"
						checked={on}
						onChange={(e) => toggleAgent(a.path, e.target.checked)}
						data-testid={`builder-agent-${a.id}`}
					/>
					<span className="flex min-w-0 flex-col">
						<span className="truncate text-sm whitespace-nowrap">{a.id}</span>
						{a.model && <span className="text-muted-foreground truncate text-xs">{t("fields.agentModel", { model: a.model })}</span>}
					</span>
				</label>
			</li>
		);
	};
	const needsToken = tokenBox && !token.trim();
	// N195: tasks are sent from the ready list only, so the actions wait for it and stop when it cannot be known.
	const readyBlock = readyPending ? t("ready.checking") : readyUnknown(suite, ready) ? t("ready.unknown", { benchmark: suite?.id }) : null;
	const stale = planned !== null && builderPlanIsStale(planned.experiment, v, catalog, ready);
	const estimateFresh = estimated !== null && !builderPlanIsStale(estimated, v, catalog, ready);
	// N43: after a Plan or Estimate press on an invalid form, how many fields to fix, next to the buttons.
	const invalidCount = touched ? Object.keys(errors).length : 0;
	const busy = createState.isLoading || startState.isLoading;
	// M4-80: a comparison needs 2 setups; say so before any click, and Plan waits for it.
	const tooFew = preview.arms.length < 2;
	const blockReason = needsToken
		? t("actions.needsToken")
		: readyBlock
			? readyBlock
			: tooFew
				? t("errors.tooFewArms")
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
		if (!valid || needsToken || readyBlock) return null;
		// M4-94: the token is saved when it is used (Estimate, Plan, Run), never on every keystroke.
		if (tokenBox) saveWriteToken(token);
		return toMatrixExperiment(v, catalog, ready);
	};

	const onEstimate = async () => {
		const exp = experimentOrNull();
		if (!exp) return;
		setEstimated(exp);
		try {
			await estimateWithOneRetry(
				() => estimate(exp).unwrap(),
				(ms) => {
					setEstimateRetrying(true);
					return new Promise<void>((resolve) => setTimeout(resolve, ms)).finally(() => setEstimateRetrying(false));
				},
			);
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
		// One plan, one experiment: a second run needs a new plan, so a double click never creates two.
		setPlanned(null);
		try {
			await start(expId).unwrap();
		} catch (error) {
			// M4-81: the form stays as it was; the error links to the created, not started comparison.
			setActionError({ title: t("actions.startError", { id: expId }), error, createdId: expId });
			return;
		}
		setStarted(true);
		onStarted(expId, planned.experiment.budget.total_usd);
	};

	const setModel = (i: number, m: ModelRef) =>
		set(
			"models",
			v.models.map((row, j) => (j === i ? m : row)),
		);
	// p06-6: once two agents are ticked, an untouched prefilled model would override each agent's own model, so it is cleared.
	const toggleAgent = (path: string, on: boolean) => {
		const agents = on ? [...v.agents, path] : v.agents.filter((p) => p !== path);
		set("agents", agents);
		if (agents.length >= 2 && !touchedKeys.current.has("models"))
			setV((prev) => ({ ...prev, models: clearDefaultModels(prev.models, defaults.models) }));
	};
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
										touchedKeys.current.add("suite");
										setV((prev) => ({
											...prev,
											suite: next,
											probes: [],
											taskCount: Math.min(prev.taskCount, Math.max(1, readyIds(s, ready).length)),
										}));
									}}
								>
									<SelectTrigger id={id} className="w-full" data-testid="builder-suite-select">
										<SelectValue placeholder={t("fields.suitePlaceholder")} />
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
						<ul className="grid grid-cols-1 gap-2 sm:grid-cols-2" data-testid="builder-agents">
							{listed.yours.map((a) => agentChoice(a))}
						</ul>
						{listed.builtIn.length > 0 && (
							<>
								<p className="text-muted-foreground text-xs font-medium">{tf("builder.builtInAgents")}</p>
								<ul className="grid grid-cols-1 gap-2 sm:grid-cols-2" data-testid="builder-agents-builtin">
									{listed.builtIn.map((a) => agentChoice(a))}
								</ul>
							</>
						)}
					</SubSection>
					<SubSection title={t("sections.models")} description={t("sections.modelsHint")} error={shown.models}>
						<ul className="flex flex-col gap-2" data-testid="builder-models">
							{v.models.map((m, i) => (
								<li key={i} className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-start gap-2">
									<div className="w-full min-w-0">
										<ModelPicker value={m} purpose="agent" onChange={(next) => setModel(i, next)} testId={`builder-model-${i}`} />
									</div>
									<Button
										type="button"
										variant="ghost"
										size="icon"
										className="size-9"
										title={t("fields.removeModel", { n: i + 1 })}
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
									{shown[`models.${i}`] && (
										<div className="col-span-2">
											<ErrorText code={shown[`models.${i}`]!} />
										</div>
									)}
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
							<Checkbox
								className="text-xs"
								checked={v.allowUnpriced}
								onChange={(on) => set("allowUnpriced", on)}
								label={t("fields.allowUnpriced")}
								testId="builder-allow-unpriced"
							/>
						</div>
					</SubSection>
				</Step>

				<Step n={3} title={t("steps.size")} hint={t("steps.sizeHint")}>
					<div className="grid gap-5 md:grid-cols-3">
						<div className="flex flex-col gap-1.5 md:col-span-3">
							<span className="text-xs font-medium">{t("fields.tasks")}</span>
							<div className="flex flex-col gap-2 text-sm" role="radiogroup" data-testid="builder-task-scope">
								{/* M4-92: 16 px radios on 24 px rows; the count input sits after the label, outside it. */}
								<div className="flex min-h-6 flex-wrap items-center gap-2">
									<label className="flex items-center gap-2">
										<input
											type="radio"
											className="accent-primary size-4"
											checked={v.taskScope === "first"}
											onChange={() => set("taskScope", "first")}
											data-testid="builder-tasks-first"
										/>
										{t("fields.tasksFirst")}
									</label>
									<Input
										aria-label={t("fields.tasksFirst")}
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
								</div>
								<label className="flex min-h-6 items-center gap-2">
									<input
										type="radio"
										className="accent-primary size-4"
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
						<Checkbox
							checked={v.adaptive}
							label={t("fields.adaptive")}
							hint={t("fields.adaptiveHint")}
							testId="builder-adaptive"
							onChange={(on) => {
									setV((prev) => ({
										...prev,
										adaptive: on,
										trials: on && !(prev.trials >= 2) ? 3 : prev.trials,
										adaptiveMin: 1,
									}));
								}}
						/>
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
										<Checkbox
											key={p}
											className={cn(
												"items-center gap-1.5 rounded-sm border px-2 py-1 font-mono text-xs",
												v.probes.includes(p) && "border-primary bg-primary/10",
											)}
											checked={v.probes.includes(p)}
											onChange={(on) => toggleProbe(p, on)}
											label={p}
											testId={`builder-probe-${p}`}
										/>
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
						{tooFew && (
							<p className="text-foreground text-sm" data-testid="builder-preview-too-few">
								{t("errors.tooFewArms")}
							</p>
						)}
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
						<p className="text-muted-foreground text-xs" data-testid="builder-preview-runs">
							{t("preview.runs", {
								count: preview.maxRuns,
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
										onChange={(e) => setToken(e.target.value)}
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
								disabled={needsToken || readyBlock !== null || estimating}
								onClick={() => void onEstimate()}
								dataTestId="builder-estimate"
							>
								{estimating ? (
									<>
										<Loader2 className="size-4 animate-spin" aria-hidden="true" />
										{tw("estimate.estimating")}
									</>
								) : (
									t("actions.estimate")
								)}
							</Button>
							<Button
								type="button"
								variant="outline"
								size="sm"
								disabled={needsToken || readyBlock !== null || tooFew}
								title={tooFew ? t("errors.tooFewArms") : undefined}
								isLoading={planState.isLoading}
								onClick={() => void onPlan()}
								dataTestId="builder-plan"
							>
								{t("actions.plan")}
							</Button>
							{/* M1-51: the start spends money, so it names the cap and asks first. */}
							<PaidButton
								size="sm"
								verb={t("actions.run")}
								capUsd={planned ? planned.experiment.budget.total_usd : null}
								estimateRuns={planned?.plan.slots}
								confirm="auto"
								kind="form"
								disabled={blockReason !== null || busy}
								isLoading={busy}
								onConfirm={onRun}
								testId="builder-run"
							/>
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
						{estimateState.error && !estimating && (
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
						{actionError?.createdId && (
							<Link
								to="/experiments/$expId"
								params={{ expId: actionError.createdId }}
								className="text-primary text-sm hover:underline"
								data-testid="builder-created-link"
							>
								{tu("form.openCreated")}
							</Link>
						)}
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
			{/* M4-84: below lg the preview sits at the end of a long form, so a bar keeps the count, the cap and Plan in reach. */}
			<div
				className="bg-background/95 sticky bottom-0 z-10 -mx-4 flex items-center gap-3 border-t px-4 py-2 backdrop-blur lg:hidden"
				data-testid="builder-mobile-bar"
			>
				<span className="min-w-0 flex-1 text-xs tabular-nums">
					{tu("form.mobileBar", { count: preview.arms.length, runs: preview.maxRuns })}
					{Number.isFinite(v.totalUsd) ? `, ${tu("form.mobileCap", { usd: v.totalUsd })}` : ""}
				</span>
				<Button
					type="button"
					size="sm"
					variant="outline"
					disabled={needsToken || readyBlock !== null || tooFew}
					isLoading={planState.isLoading}
					onClick={() => void onPlan()}
					dataTestId="builder-plan-mobile"
				>
					{t("actions.plan")}
				</Button>
			</div>
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
/** True when the builder differs from its defaults (M4-88: the leave guard asks first). */
export function builderDirty(values: BuilderValues, defaults: BuilderValues): boolean {
	return JSON.stringify(values) !== JSON.stringify(defaults);
}
