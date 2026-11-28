import "./builderI18n";
import "@/app/experiments_/$expId/views/fw11I18n";

import { readWriteToken, saveWriteToken } from "@/components/experiments/experiments.utils";
import { useSession, usesWriteToken } from "@/lib/session/sessionContext";
import { PlanResult, planBlocksStart } from "@/components/experiments/planResult";
import { PaidButton } from "@/components/ui/paidButton";
import { useLeaveGuard } from "@/lib/hooks/useLeaveGuard";
import { draftOr, useDraftSync } from "./draftSync";
import { PlanEstimate } from "@/components/experiments/planEstimate";
import { LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
	getBenchErrorMessage,
	useCreateExperimentMutation,
	usePlanExperimentMutation,
	useStartExperimentMutation,
} from "@/lib/store/apis/benchApi";
import { SkillScanProblem, parseSkillRefusal } from "@/app/evaluations_/$evalId/views/skillScanProblem";
import type { Catalog, CatalogAgent, Experiment, ExperimentPlan } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { zodResolver } from "@hookform/resolvers/zod";
import { useEffect, useId, useRef, useState, type ComponentProps } from "react";
import {
	Controller,
	useFieldArray,
	useForm,
	useFormState,
	useWatch,
	type Control,
	type FieldError,
	type FieldErrors,
	type UseFormRegister,
} from "react-hook-form";
import { catalogProbeFor, probeLayerLabel } from "@/components/receipts/receipts.utils";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { Link } from "@tanstack/react-router";
import "@/app/leaderboard/views/uf4bI18n";
import "@/components/experiments/ufl2I18n";

/** Agent config keys a catalog agent always has (CatalogAgent): offered on the override path. */
export const OVERRIDE_KEYS = ["model", "adapter", "parser"] as const;
import {
	CHANGE_KINDS,
	conditionsFor,
	experimentFormSchema,
	type ChangeKind,
	planIsStale,
	suiteRef,
	toExperiment,
	type ExperimentFormValues,
	type ValidationKey,
} from "./experimentFormSchema";
import {
	builderAgentGroups,
	fillUntouched,
	readyIds,
	readyUnknown,
	someNotReady,
	type BuilderAgentIds,
	type ReadyTasks,
} from "./matrixBuilderSchema";
import { PlanInvalidAlert, invalidFields, type FormFieldName } from "./planInvalid";
import { SuiteNativeOptions } from "./suiteOptions";

export interface ExperimentFormProps {
	catalog: Catalog;
	defaults: ExperimentFormValues;
	/** Ready tasks per benchmark (GET /suites?fields=light): "all tasks" sends only these (N42). Absent counts every task. */
	ready?: ReadyTasks;
	/** The ready list is still loading: Plan and Create wait for it (N195). */
	readyPending?: boolean;
	/** The agent ids My agents lists (GET /agents split by splitAgents); absent lists every catalog agent. */
	agentGroups?: BuilderAgentIds;
	/** Called once the experiment is created and started, with its id and spend cap. */
	onStarted: (expId: string, capUsd: number) => void;
}

/** Brings a form field into view and focuses it when it can take focus (N43). */
function revealField(name: FormFieldName, setFocus: (name: FormFieldName) => void) {
	try {
		setFocus(name);
	} catch {
		// A field without a focusable element (the conditions list) is only scrolled to.
	}
	if (typeof document === "undefined") return;
	document.querySelector(`[data-form-field="${name}"]`)?.scrollIntoView({ block: "center", behavior: "smooth" });
}

interface Planned {
	experiment: Experiment;
	plan: ExperimentPlan;
}

/** Where the YAML-free form's draft is kept (Q-150). */
export const FORM_DRAFT_KEY = "newComparison.form";

/** The experiment form, the planner and the create-and-start action. */
export function ExperimentForm({ catalog, defaults, ready, readyPending, agentGroups, onStarted }: ExperimentFormProps) {
	const { t } = useTranslation("bench");
	const { t: tf } = useTranslation("fw11");
	const { t: tb } = useTranslation("builder");
	const form = useForm<ExperimentFormValues>({ resolver: zodResolver(experimentFormSchema), defaultValues: defaults, mode: "onTouched" });
	const { control, register, handleSubmit, setValue, setFocus, getValues, formState } = form;
	const { dirtyFields } = formState;
	const values = useWatch({ control });
	// L2: new defaults (the ready list arrived) fill only the fields nobody touched; the form never starts again.
	const shownDefaults = useRef(defaults);
	useEffect(() => {
		if (shownDefaults.current === defaults) return;
		shownDefaults.current = defaults;
		const current = getValues();
		const suiteTouched = touchedField(dirtyFields.suite);
		const touched = (k: keyof ExperimentFormValues) =>
			touchedField(dirtyFields[k]) || (suiteTouched && (k === "conditions" || k === "primaryCondition"));
		const next = fillUntouched(current, defaults, touched);
		for (const k of Object.keys(next) as (keyof ExperimentFormValues)[]) {
			if (JSON.stringify(next[k]) !== JSON.stringify(current[k])) {
				setValue(k, next[k] as never, { shouldDirty: false, shouldValidate: formState.isSubmitted });
			}
		}
	}, [defaults, dirtyFields, getValues, setValue, formState.isSubmitted]);
	const [token, setToken] = useState(readWriteToken);
	// Hosted mode has no token box: the session cookie and its CSRF header authorise the writes.
	const tokenBox = usesWriteToken(useSession());
	const [planned, setPlanned] = useState<Planned | null>(null);
	const [actionError, setActionError] = useState<{ title: string; error: unknown; createdId?: string } | null>(null);
	const { t: tu } = useTranslation("uf4b");
	// benchApi attaches the write token saved below to every mutation.
	const [plan, planState] = usePlanExperimentMutation();
	const [create, createState] = useCreateExperimentMutation();
	const [start, startState] = useStartExperimentMutation();
	// M4-88: leaving with changes asks first; a started comparison has nothing left to lose.
	const [started, setStarted] = useState(false);
	useLeaveGuard(formState.isDirty && !started);
	// Q-150: the typed input is kept as a draft beside the leave guard; a reload puts it back as changed fields.
	// The restore runs before the sync effect, which would otherwise drop the draft on the first, clean render.
	useEffect(() => {
		const draft = draftOr<Partial<ExperimentFormValues>>(FORM_DRAFT_KEY, {});
		for (const k of Object.keys(draft) as (keyof ExperimentFormValues)[]) setValue(k, draft[k] as never, { shouldDirty: true });
		// Once, on mount: later defaults fill only untouched fields (above).
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, []);

	// A created comparison (even one whose start failed) ends the draft, so a reopened form starts clean.
	useDraftSync(FORM_DRAFT_KEY, values, formState.isDirty, started || Boolean(actionError?.createdId));

	const stale = planned !== null && planIsStale(planned.experiment, values, ready);
	// N43: after a Plan press, every field still to fix is named next to the button.
	const invalid = formState.isSubmitted ? invalidFields(formState.errors) : [];
	const focusField = (name: FormFieldName) => revealField(name, setFocus);
	const busy = createState.isLoading || startState.isLoading;
	// Local mode: plan, estimate, create and start are all writes behind the token, so it is asked first.
	const needsToken = tokenBox && !token.trim();
	const suite = catalog.suites.find((s) => suiteRef(s) === values.suite);
	// N195: tasks are sent from the ready list only, so Plan waits for it and stops when it cannot be known.
	const readyBlock = readyPending
		? tb("ready.checking")
		: readyUnknown(suite, ready)
			? tb("ready.unknown", { benchmark: suite?.id })
			: null;
	const blockReason = needsToken
		? t("experiments.new.actions.needsToken")
		: readyBlock
			? readyBlock
			: !planned
				? t("experiments.new.actions.needsPlan")
				: stale
					? t("experiments.new.actions.planStale")
					: planBlocksStart(planned.plan)
						? t("experiments.new.actions.blocked")
						: null;

	const onInvalid = (errors: FieldErrors<ExperimentFormValues>) => {
		const first = invalidFields(errors)[0];
		if (first) focusField(first);
	};

	const onPlan = handleSubmit(async (formValues) => {
		if (needsToken || readyBlock) return;
		// M4-94: the token is saved on a submit (Plan), never on every keystroke; benchApi then sends it with each write.
		if (tokenBox) saveWriteToken(token);
		const experiment = toExperiment(formValues, ready);
		setActionError(null);
		setPlanned(null);
		try {
			const result = await plan(experiment).unwrap();
			setPlanned({ experiment, plan: result });
		} catch {
			// planState.error renders the failure.
		}
	}, onInvalid);

	const onCreateAndStart = async () => {
		if (!planned || blockReason) return;
		setActionError(null);
		let expId: string;
		try {
			expId = (await create(planned.experiment).unwrap()).exp_id;
		} catch (error) {
			setActionError({ title: t("experiments.new.actions.createError"), error });
			return;
		}
		// The plan is spent: a second experiment needs a new plan, so one click never creates two.
		setPlanned(null);
		try {
			await start(expId).unwrap();
		} catch (error) {
			// M4-81: a failed start keeps the form and links to the created, not started comparison.
			setActionError({ title: t("experiments.new.actions.startError", { id: expId }), error, createdId: expId });
			return;
		}
		setStarted(true);
		onStarted(expId, planned.experiment.budget.total_usd);
	};

	// The base agent menu lists what My agents shows (the org's own, then built-in); a chosen agent stays listed.
	const agentMenu = (() => {
		const g = builderAgentGroups(catalog, agentGroups);
		const known = new Set([...g.yours, ...g.builtIn].map((a) => a.path));
		const extra = (catalog.agents ?? []).filter((a) => a.path === values.base && !known.has(a.path));
		return { yours: [...g.yours, ...extra], builtIn: g.builtIn };
	})();
	const agentItem = (agent: CatalogAgent) => (
		<option key={agent.path} value={agent.path} data-testid={`experiment-base-option-${agent.id}`}>
			{t("experiments.new.fields.baseOption", { id: agent.id, model: agent.model })}
		</option>
	);

	return (
		<form
			className="grid items-start gap-6 xl:grid-cols-[minmax(0,1fr)_24rem]"
			onSubmit={(e) => {
				e.preventDefault();
				void onPlan();
			}}
			noValidate
			data-testid="new-experiment-form"
		>
			<div className="flex min-w-0 flex-col gap-6">
				<FormSection title={t("experiments.new.sections.what")}>
					<div className="grid gap-4 md:grid-cols-3">
						<Field label={t("experiments.new.fields.name")} error={formState.errors.name} name="name">
							{(id, describedBy) => (
								<Input
									id={id}
									aria-describedby={describedBy}
									aria-invalid={Boolean(formState.errors.name)}
									placeholder={t("experiments.new.fields.namePlaceholder")}
									data-testid="experiment-name-input"
									{...register("name")}
								/>
							)}
						</Field>
						<Field label={t("experiments.new.fields.suite")} error={formState.errors.suite} name="suite">
							{(id) => (
								<Controller
									control={control}
									name="suite"
									render={({ field }) => (
										// N233: native selects take every input method (mouse, keyboard, form-filling tools).
										<select
											ref={field.ref}
											id={id}
											className={NATIVE_SELECT}
											value={field.value}
											onChange={(e) => {
												const next = e.target.value;
												field.onChange(next);
												const nextSuite = catalog.suites.find((s) => suiteRef(s) === next);
												setValue("conditions", conditionsFor(nextSuite), { shouldValidate: formState.isSubmitted });
												setValue("primaryCondition", "clean");
											}}
											data-testid="experiment-suite-select"
										>
											{!field.value && <option value="" disabled />}
											<SuiteNativeOptions catalog={catalog} ready={ready} testIdPrefix="experiment-suite-option" />
										</select>
									)}
								/>
							)}
						</Field>
						<Field label={t("experiments.new.fields.base")} error={formState.errors.base} name="base">
							{(id) => (
								<Controller
									control={control}
									name="base"
									render={({ field }) => (
										<select
											ref={field.ref}
											id={id}
											className={NATIVE_SELECT}
											value={field.value}
											onChange={(e) => field.onChange(e.target.value)}
											data-testid="experiment-base-select"
										>
											{!field.value && <option value="" disabled />}
											{agentGroups ? (
												<>
													<optgroup label={tf("builder.yourAgents")}>{agentMenu.yours.map(agentItem)}</optgroup>
													{agentMenu.builtIn.length > 0 && (
														<optgroup label={tf("builder.builtInAgents")}>{agentMenu.builtIn.map(agentItem)}</optgroup>
													)}
												</>
											) : (
												agentMenu.yours.map(agentItem)
											)}
										</select>
									)}
								/>
							)}
						</Field>
					</div>
				</FormSection>

				<FormSection title={t("experiments.new.sections.arms")}>
					<div className="grid gap-4 md:grid-cols-2">
						<div className="flex flex-col gap-3 rounded-sm border p-3">
							<span className="text-sm font-semibold">{t("experiments.new.fields.armA")}</span>
							<Field
								label={t("experiments.new.fields.label")}
								error={formState.errors.armALabel}
								hint={t("experiments.new.fields.armAHint")}
								name="armALabel"
							>
								{(id, describedBy) => (
									<Input
										id={id}
										aria-describedby={describedBy}
										aria-invalid={Boolean(formState.errors.armALabel)}
										placeholder={t("experiments.new.fields.armAPlaceholder")}
										data-testid="experiment-arm-a-label-input"
										{...register("armALabel")}
									/>
								)}
							</Field>
						</div>
						<div className="border-primary/40 flex flex-col gap-3 rounded-sm border p-3">
							<span className="text-sm font-semibold">{t("experiments.new.fields.armB")}</span>
							<Field label={t("experiments.new.fields.label")} error={formState.errors.armBLabel} name="armBLabel">
								{(id, describedBy) => (
									<Input
										id={id}
										aria-describedby={describedBy}
										aria-invalid={Boolean(formState.errors.armBLabel)}
										placeholder={t("experiments.new.fields.armBPlaceholder")}
										data-testid="experiment-arm-b-label-input"
										{...register("armBLabel")}
									/>
								)}
							</Field>
							<ArmBChange register={register} errors={formState.errors} kind={values.changeKind ?? "custom"} strategy={values.routingStrategy ?? "weighted"}>
							<div className="grid gap-3">
								<Field label={t("experiments.new.fields.overridePath")} error={formState.errors.overridePath} name="overridePath">
									{(id, describedBy) => (
										<>
											<Input
												id={id}
												aria-describedby={describedBy}
												aria-invalid={Boolean(formState.errors.overridePath)}
												placeholder={t("experiments.new.fields.overridePathPlaceholder")}
												className="font-mono"
												autoComplete="off"
												spellCheck={false}
												list={`${id}-keys`}
												data-testid="experiment-override-path-input"
												{...register("overridePath")}
											/>
											{/* M4-87: the config keys the catalog knows as suggestions; any other dotted path still types in. */}
											<datalist id={`${id}-keys`} data-testid="experiment-override-path-keys">
												{OVERRIDE_KEYS.map((k) => (
													<option key={k} value={k} />
												))}
											</datalist>
										</>
									)}
								</Field>
								<Field label={t("experiments.new.fields.overrideValue")} error={formState.errors.overrideValue} name="overrideValue">
									{(id, describedBy) => (
										<Input
											id={id}
											aria-describedby={describedBy}
											aria-invalid={Boolean(formState.errors.overrideValue)}
											placeholder={t("experiments.new.fields.overrideValuePlaceholder")}
											className="font-mono"
											autoComplete="off"
											spellCheck={false}
											data-testid="experiment-override-value-input"
											{...register("overrideValue")}
										/>
									)}
								</Field>
							</div>
							<p className="text-muted-foreground text-xs">{t("experiments.new.fields.overrideHint")}</p>
							</ArmBChange>
						</div>
					</div>
				</FormSection>

				<FormSection title={t("experiments.new.sections.primary")} description={t("experiments.new.fields.primaryHint")}>
					<div className="grid gap-4 md:grid-cols-2">
						<Field label={t("experiments.new.fields.primaryCondition")} error={formState.errors.primaryCondition} name="primaryCondition">
							{(id, describedBy) => (
								<Controller
									control={control}
									name="primaryCondition"
									render={({ field }) => (
										<select
											ref={field.ref}
											id={id}
											aria-describedby={describedBy}
											className={cn(NATIVE_SELECT, "font-mono")}
											value={field.value}
											onChange={(e) => field.onChange(e.target.value)}
											data-testid="experiment-primary-condition-select"
										>
											{!field.value && <option value="" disabled />}
											{(values.conditions ?? []).map((c) =>
												c?.condition ? (
													<option key={c.condition} value={c.condition} data-testid={`experiment-primary-condition-option-${c.condition}`}>
														{c.condition}
													</option>
												) : null,
											)}
										</select>
									)}
								/>
							)}
						</Field>
						<Field label={t("experiments.new.fields.primaryMetric")} error={formState.errors.primaryMetric} name="primaryMetric">
							{(id) => (
								<Controller
									control={control}
									name="primaryMetric"
									render={({ field }) => (
										<select
											ref={field.ref}
											id={id}
											className={NATIVE_SELECT}
											value={field.value}
											onChange={(e) => field.onChange(e.target.value)}
											data-testid="experiment-primary-metric-select"
										>
											{!field.value && <option value="" disabled />}
											{catalog.metrics.map((m) => (
												<option key={m.name} value={m.name} data-testid={`experiment-primary-metric-option-${m.name}`}>
													{m.label}
												</option>
											))}
										</select>
									)}
								/>
							)}
						</Field>
					</div>
				</FormSection>

				<FormSection title={t("experiments.new.sections.conditions")}>
					<div className="flex flex-col gap-3" data-form-field="conditions">
						<ConditionsEditor
							control={control}
							register={register}
							catalog={catalog}
							suite={values.suite}
							tasks={readyIds(suite, ready)}
							partlyReady={someNotReady(suite, ready) || Boolean(suite && ready?.has(suiteRef(suite)))}
						/>
						{formState.errors.conditions?.message && (
							<ErrorText message={formState.errors.conditions.message} testId="experiment-conditions-error" />
						)}
						{formState.errors.conditions?.root?.message && (
							<ErrorText message={formState.errors.conditions.root.message} testId="experiment-conditions-error" />
						)}
					</div>
				</FormSection>

				<FormSection title={t("experiments.new.sections.run")}>
					<div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
						<Field label={t("experiments.new.fields.seed")} error={formState.errors.seed} name="seed">
							{(id, describedBy) => (
								<Input
									id={id}
									type="number"
									inputMode="numeric"
									aria-describedby={describedBy}
									aria-invalid={Boolean(formState.errors.seed)}
									data-testid="experiment-seed-input"
									{...register("seed", { valueAsNumber: true })}
								/>
							)}
						</Field>
						<Field label={t("experiments.new.fields.concurrency")} error={formState.errors.concurrency} name="concurrency">
							{(id, describedBy) => (
								<Input
									id={id}
									type="number"
									inputMode="numeric"
									min={1}
									max={64}
									aria-describedby={describedBy}
									aria-invalid={Boolean(formState.errors.concurrency)}
									data-testid="experiment-concurrency-input"
									{...register("concurrency", { valueAsNumber: true })}
								/>
							)}
						</Field>
						{!tokenBox && (
							<Field label={t("experiments.new.fields.runnerPool")} error={formState.errors.runnerPool} name="runnerPool">
								{(id, describedBy) => (
									<Input
										id={id}
										placeholder="default"
										aria-describedby={describedBy}
										data-testid="experiment-runner-pool-input"
										{...register("runnerPool")}
									/>
								)}
							</Field>
						)}
						<Field label={t("experiments.new.fields.totalUsd")} error={formState.errors.totalUsd} name="totalUsd">
							{(id, describedBy) => (
								<Input
									id={id}
									type="number"
									inputMode="decimal"
									min={0}
									step="0.01"
									aria-describedby={describedBy}
									aria-invalid={Boolean(formState.errors.totalUsd)}
									data-testid="experiment-total-usd-input"
									{...register("totalUsd", { valueAsNumber: true })}
								/>
							)}
						</Field>
					</div>
				</FormSection>
				<AdvancedSpec register={register} errors={formState.errors} />
			</div>

			<aside className="flex flex-col gap-4 xl:sticky xl:top-0">
				<Card className="gap-4 py-4 shadow-none" data-testid="plan-card">
					<CardHeader className="px-4">
						<CardTitle className="text-sm">{t("experiments.new.plan.title")}</CardTitle>
						<CardDescription className="text-xs">{t("experiments.new.plan.description")}</CardDescription>
					</CardHeader>
					<CardContent className="flex flex-col gap-4 px-4">
						{/* U2: the token comes first, because plan and estimate are writes behind it too (a 401 otherwise). */}
						{tokenBox && (
							<Field label={t("experiments.new.fields.token")} hint={t("experiments.new.fields.tokenHint")}>
								{(id, describedBy) => (
									<Input
										id={id}
										type="password"
										autoComplete="off"
										aria-describedby={describedBy}
										placeholder={t("experiments.new.fields.tokenPlaceholder")}
										value={token}
										onChange={(e) => setToken(e.target.value)}
										data-testid="experiment-write-token-input"
									/>
								)}
							</Field>
						)}
						<div className="flex flex-wrap gap-2">
							<Button
								type="submit"
								variant="outline"
								size="sm"
								disabled={needsToken || readyBlock !== null}
								isLoading={planState.isLoading}
								dataTestId="experiment-plan-button"
							>
								{t("experiments.new.actions.plan")}
							</Button>
							{/* M1-51: the start spends money, so it names the cap and asks first. */}
							<PaidButton
								size="sm"
								verb={t("experiments.new.actions.createAndStart")}
								capUsd={planned ? planned.experiment.budget.total_usd : null}
								estimateRuns={planned?.plan.slots}
								confirm="auto"
								kind="form"
								disabled={blockReason !== null || busy}
								isLoading={busy}
								onConfirm={onCreateAndStart}
								testId="experiment-create-start-button"
							/>
						</div>
						<PlanInvalidAlert fields={invalid} onFocus={focusField} />
						{blockReason && (
							<p
								className={cn("text-xs", stale ? "text-chart-warning-ink" : "text-muted-foreground")}
								data-testid="experiment-start-blocked-reason"
							>
								{blockReason}
							</p>
						)}
						{planState.error &&
							!planState.isLoading &&
							(() => {
								// wave 4, N22: a skill gate refusal offers the scan in place
								const refusal = parseSkillRefusal(getBenchErrorMessage(planState.error));
								return refusal ? (
									<SkillScanProblem refusal={refusal} testId="experiment-plan-skill-problem" />
								) : (
									<LoadError title={t("experiments.new.actions.planError")} error={planState.error} testId="experiment-plan-error" />
								);
							})()}
						{actionError && <LoadError title={actionError.title} error={actionError.error} testId="experiment-action-error" />}
						{actionError?.createdId && (
							<Link
								to="/experiments/$expId"
								params={{ expId: actionError.createdId }}
								className="text-primary text-sm hover:underline"
								data-testid="experiment-created-link"
							>
								{tu("form.openCreated")}
							</Link>
						)}
						{planned && <PlanResult plan={planned.plan} estimateShown className={cn(stale && "opacity-60")} />}
						{planned && <PlanEstimate experiment={planned.experiment} className={cn(stale && "opacity-60")} />}
					</CardContent>
				</Card>
			</aside>
		</form>
	);
}

function ConditionsEditor({
	control,
	register,
	catalog,
	suite,
	tasks,
	partlyReady,
}: {
	control: Control<ExperimentFormValues>;
	register: UseFormRegister<ExperimentFormValues>;
	catalog: Catalog;
	/** The selected suite reference: its probe_layers decide a probe id two suites share (QC2-27-10). */
	suite: string | undefined;
	/** The tasks that can run (the ready ones when known). */
	tasks: readonly string[];
	/** The ready tasks of the suite are known, so the counts say "ready tasks" (N195). */
	partlyReady: boolean;
}) {
	const { t } = useTranslation("bench");
	const { fields } = useFieldArray({ control, name: "conditions" });
	const conditions = useWatch({ control, name: "conditions" });
	const { errors } = useFormState({ control, name: "conditions" });

	return (
		<ul className="flex flex-col divide-y rounded-sm border">
			{fields.map((field, index) => {
				const current = conditions?.[index];
				const enabled = Boolean(current?.enabled);
				const probe = catalogProbeFor(catalog, suite, field.condition);
				return (
					<li
						key={field.id}
						className={cn("flex flex-col gap-3 px-3 py-3", !enabled && "bg-muted/30")}
						data-testid={`experiment-condition-${field.condition}`}
					>
						<div className="flex flex-wrap items-center justify-between gap-3">
							<label className="flex cursor-pointer items-center gap-2">
								<input
									type="checkbox"
									className="accent-primary size-4 cursor-pointer"
									data-testid={`experiment-condition-${field.condition}-enabled`}
									{...register(`conditions.${index}.enabled`)}
								/>
								<span className="font-mono text-sm">{field.condition}</span>
								<span className="text-muted-foreground text-xs">{conditionLayerText(field.condition, probe, t)}</span>
							</label>
							<TrialsField
								condition={field.condition}
								disabled={!enabled}
								error={errors.conditions?.[index]?.trials?.message}
								inputProps={register(`conditions.${index}.trials`, { valueAsNumber: true })}
							/>
						</div>
						{enabled && (
							<TaskSelection
								control={control}
								index={index}
								conditionId={field.condition}
								tasks={tasks}
								partlyReady={partlyReady}
								allTasks={Boolean(current?.allTasks)}
							/>
						)}
					</li>
				);
			})}
		</ul>
	);
}

function TaskSelection({
	control,
	index,
	conditionId,
	tasks,
	partlyReady,
	allTasks,
}: {
	control: Control<ExperimentFormValues>;
	index: number;
	conditionId: string;
	tasks: readonly string[];
	partlyReady: boolean;
	allTasks: boolean;
}) {
	const { t } = useTranslation("bench");
	const { t: tb } = useTranslation("builder");
	const groupName = useId();
	return (
		<div className="flex flex-col gap-2 pl-6">
			<Controller
				control={control}
				name={`conditions.${index}.allTasks`}
				render={({ field }) => (
					<div className="flex flex-wrap gap-4 text-xs" role="radiogroup">
						<label className="flex cursor-pointer items-center gap-1.5">
							<input
								ref={field.ref}
								type="radio"
								name={groupName}
								className="accent-primary cursor-pointer"
								checked={field.value}
								onChange={() => field.onChange(true)}
								data-testid={`experiment-condition-${conditionId}-tasks-all`}
							/>
							{partlyReady
								? tb("fields.tasksAllReady", { count: tasks.length })
								: t("experiments.new.fields.tasksAll", { count: tasks.length })}
						</label>
						<label className="flex cursor-pointer items-center gap-1.5">
							<input
								type="radio"
								name={groupName}
								className="accent-primary cursor-pointer"
								checked={!field.value}
								onChange={() => field.onChange(false)}
								data-testid={`experiment-condition-${conditionId}-tasks-chosen`}
							/>
							{t("experiments.new.fields.tasksChosen")}
						</label>
					</div>
				)}
			/>
			{!allTasks && (
				<Controller
					control={control}
					name={`conditions.${index}.taskIds`}
					render={({ field, fieldState }) => (
						<div className="flex flex-col gap-1">
							<div className="flex flex-wrap gap-1.5">
								{tasks.map((task, i) => {
									const checked = field.value.includes(task);
									return (
										<label
											key={task}
											className={cn(
												"flex cursor-pointer items-center gap-1.5 rounded-sm border px-2 py-1 font-mono text-xs",
												checked && "border-primary bg-primary/10",
											)}
										>
											<input
												ref={i === 0 ? field.ref : undefined}
												type="checkbox"
												className="accent-primary cursor-pointer"
												checked={checked}
												onChange={(e) =>
													field.onChange(e.target.checked ? [...field.value, task] : field.value.filter((id) => id !== task))
												}
												data-testid={`experiment-condition-${conditionId}-task-${task}`}
											/>
											{task}
										</label>
									);
								})}
							</div>
							{fieldState.error?.message && (
								<ErrorText message={fieldState.error.message} testId={`experiment-condition-${conditionId}-tasks-error`} />
							)}
						</div>
					)}
				/>
			)}
		</div>
	);
}

/** The look of the form's native selects (N233), as the Workers page's select. */
const NATIVE_SELECT = "border-input bg-background h-9 w-full min-w-0 rounded-md border px-2 text-sm";

/** True when a react-hook-form dirtyFields entry marks any part of a field as changed. */
function touchedField(dirty: unknown): boolean {
	if (Array.isArray(dirty)) return dirty.some(touchedField);
	if (dirty && typeof dirty === "object") return Object.values(dirty).some(touchedField);
	return dirty === true;
}

function FormSection({ title, description, children }: { title: string; description?: string; children: React.ReactNode }) {
	return (
		<section className="flex flex-col gap-3">
			<div className="flex flex-col gap-0.5">
				<h2 className="text-sm font-semibold tracking-tight">{title}</h2>
				{description && <p className="text-muted-foreground text-xs">{description}</p>}
			</div>
			{children}
		</section>
	);
}

function Field({
	label,
	error,
	hint,
	name,
	children,
}: {
	label: string;
	error?: FieldError;
	hint?: string;
	/** The form field inside, so Plan can bring the first invalid one into view (N43). */
	name?: FormFieldName | keyof ExperimentFormValues;
	children: (id: string, describedBy: string | undefined) => React.ReactNode;
}) {
	const id = useId();
	const hintId = `${id}-hint`;
	const errorId = `${id}-error`;
	const describedBy = [hint ? hintId : null, error?.message ? errorId : null].filter(Boolean).join(" ") || undefined;
	return (
		<div className="flex min-w-0 flex-col gap-1.5" data-form-field={name}>
			<label htmlFor={id} className="text-xs font-medium">
				{label}
			</label>
			{children(id, describedBy)}
			{hint && (
				<p id={hintId} className="text-muted-foreground text-[11px]">
					{hint}
				</p>
			)}
			{error?.message && <ErrorText id={errorId} message={error.message} />}
		</div>
	);
}

/** The tries box of one test row (p07-3): marked invalid, with its message below, when the schema refuses it. */
export function TrialsField({
	condition,
	disabled,
	error,
	inputProps,
}: {
	condition: string;
	disabled: boolean;
	error?: string;
	inputProps: ComponentProps<"input">;
}) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex flex-col items-end gap-1">
			<label className="flex items-center gap-2 text-xs">
				<span className="text-muted-foreground">{t("experiments.new.fields.trials")}</span>
				<Input
					type="number"
					inputMode="numeric"
					min={1}
					max={50}
					disabled={disabled}
					className="h-8 w-20"
					data-testid={`experiment-condition-${condition}-trials`}
					aria-invalid={error ? true : undefined}
					{...inputProps}
				/>
			</label>
			{error && <ErrorText message={error} testId={`experiment-condition-${condition}-trials-error`} />}
		</div>
	);
}

function ErrorText({ message, id, testId }: { message: string; id?: string; testId?: string }) {
	const { t } = useTranslation("bench");
	const { t: tl } = useTranslation("ufl2");
	return (
		<p id={id} role="alert" className="text-chart-error-ink text-xs" data-testid={testId}>
			{message.startsWith("ufl2.") ? tl(message.slice(5) as "validation.specJson") : t(message as ValidationKey)}
		</p>
	);
}

/** The condition row's layer note: the suite's own probe layer, no probe for clean, else unresolved. */
export function conditionLayerText(condition: string, probe: { layer: string } | undefined, t: TFunction<"bench">): string {
	if (probe) return t("experiments.new.fields.conditionProbe", { layer: probeLayerLabel(probe.layer, t) });
	return condition === "clean" ? t("experiments.new.fields.conditionClean") : t("experiments.new.fields.conditionProbeUnresolved");
}
/** Arm B's change: a real control per gateway feature; the raw path and JSON value stay under "Any config field". */
function ArmBChange({
	register,
	errors,
	kind,
	strategy,
	children,
}: {
	register: UseFormRegister<ExperimentFormValues>;
	errors: FieldErrors<ExperimentFormValues>;
	kind: ChangeKind;
	strategy: ExperimentFormValues["routingStrategy"];
	children: React.ReactNode;
}) {
	const { t } = useTranslation("ufl2");
	return (
		<div className="grid gap-3" data-testid="experiment-arm-b-change">
			<Field label={t("change.kind")} name="changeKind">
				{(id) => (
					<select id={id} className={NATIVE_SELECT} data-testid="experiment-change-kind" {...register("changeKind")}>
						{CHANGE_KINDS.map((k) => (
							<option key={k} value={k}>
								{t(`change.kinds.${k}`)}
							</option>
						))}
					</select>
				)}
			</Field>
			{kind === "routing" && (
				<div className="grid gap-3">
					<Field label={t("change.strategy")} name="routingStrategy">
						{(id) => (
							<select id={id} className={NATIVE_SELECT} data-testid="experiment-routing-strategy" {...register("routingStrategy")}>
								{(["weighted", "fallback_chain", "fixed"] as const).map((k) => (
									<option key={k} value={k}>
										{t(`change.strategies.${k}`)}
									</option>
								))}
							</select>
						)}
					</Field>
					<Field label={t("change.models")} error={errors.changeModels} hint={strategy === "fixed" ? undefined : t("change.routingHint")} name="changeModels">
						{(id, d) => <Input id={id} aria-describedby={d} placeholder={t("change.modelsPlaceholder")} data-testid="experiment-change-models" {...register("changeModels")} />}
					</Field>
				</div>
			)}
			{kind === "failover" && (
				<Field label={t("change.fallbacks")} error={errors.changeModels} name="changeModels">
					{(id, d) => <Input id={id} aria-describedby={d} placeholder={t("change.modelsPlaceholder")} data-testid="experiment-change-models" {...register("changeModels")} />}
				</Field>
			)}
			{kind === "prompt_cache" && (
				<Field label={t("change.cache")} name="cacheMode">
					{(id) => (
						<select id={id} className={NATIVE_SELECT} data-testid="experiment-cache-mode" {...register("cacheMode")}>
							<option value="auto_inject">{t("change.cacheOn")}</option>
							<option value="off">{t("change.cacheOff")}</option>
						</select>
					)}
				</Field>
			)}
			{kind === "code_mode" && (
				<label className="flex items-center gap-2 text-xs">
					<input type="checkbox" data-testid="experiment-code-mode" {...register("codeMode")} />
					{t("change.codeMode")}
				</label>
			)}
			{kind === "team" && (
				<Field label={t("change.teamSize")} hint={t("change.teamHint")} name="teamSize">
					{(id, d) => (
						<Input id={id} aria-describedby={d} type="number" min={2} max={4} className="w-24" data-testid="experiment-team-size" {...register("teamSize", { valueAsNumber: true })} />
					)}
				</Field>
			)}
			{kind === "custom" && children}
		</div>
	);
}

/** U2 section 2: spec options no other form sets. Folded; Plan checks them on the server. */
function AdvancedSpec({ register, errors }: { register: UseFormRegister<ExperimentFormValues>; errors: FieldErrors<ExperimentFormValues> }) {
	const { t } = useTranslation("ufl2");
	const box = (name: "allowUncalibratedJudge" | "heldOut" | "aa" | "fairness" | "sandbagging" | "attribution", label: string) => (
		<label className="flex items-center gap-2 text-xs">
			<input type="checkbox" data-testid={`experiment-adv-${name}`} {...register(name)} />
			{label}
		</label>
	);
	return (
		<details className="rounded-sm border p-3" open={errors.alpha || errors.extraSpec ? true : undefined} data-testid="experiment-advanced">
			<summary className="cursor-pointer text-sm font-semibold">{t("advanced.title")}</summary>
			<div className="mt-3 grid gap-4">
				<div className="grid gap-4 sm:grid-cols-3">
					<Field label={t("advanced.alpha")} error={errors.alpha} name="alpha">
						{(id, d) => (
							<Input id={id} aria-describedby={d} type="number" step="0.01" min={0} max={1} className="h-9" data-testid="experiment-adv-alpha" {...register("alpha", { valueAsNumber: true })} />
						)}
					</Field>
					<Field label={t("advanced.realism")} name="realism">
						{(id) => (
							<select id={id} className={NATIVE_SELECT} data-testid="experiment-adv-realism" {...register("realism")}>
								<option value="">{t("advanced.realismStandard")}</option>
								<option value="neutral">{t("advanced.realismNeutral")}</option>
							</select>
						)}
					</Field>
					<Field label={t("advanced.egress")} name="egress">
						{(id) => (
							<select id={id} className={NATIVE_SELECT} data-testid="experiment-adv-egress" {...register("egress")}>
								<option value="">{t("advanced.egressDefault")}</option>
								<option value="offline">{t("advanced.egressOffline")}</option>
								<option value="replay">{t("advanced.egressReplay")}</option>
								<option value="live-allowlisted">{t("advanced.egressLive")}</option>
							</select>
						)}
					</Field>
				</div>
				<div className="grid gap-2 sm:grid-cols-2">
					{box("heldOut", t("advanced.heldOut"))}
					{box("aa", t("advanced.aa"))}
					{box("fairness", t("advanced.fairness"))}
					{box("sandbagging", t("advanced.sandbagging"))}
					{box("attribution", t("advanced.attribution"))}
					{box("allowUncalibratedJudge", t("advanced.uncalibrated"))}
				</div>
				<Field label={t("advanced.extraSpec")} error={errors.extraSpec} hint={t("advanced.extraSpecHint")} name="extraSpec">
					{(id, d) => (
						<textarea
							id={id}
							aria-describedby={d}
							rows={4}
							spellCheck={false}
							className="border-input bg-background w-full min-w-0 rounded-sm border px-3 py-2 font-mono text-xs"
							placeholder='{"safety": "trifecta"}'
							data-testid="experiment-adv-extra-spec"
							{...register("extraSpec")}
						/>
					)}
				</Field>
			</div>
		</details>
	);
}
