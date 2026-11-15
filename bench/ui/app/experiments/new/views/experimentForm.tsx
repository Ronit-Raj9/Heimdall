import { readWriteToken, saveWriteToken } from "@/components/experiments/experiments.utils";
import { PlanResult, planBlocksStart } from "@/components/experiments/planResult";
import { PlanEstimate } from "@/components/experiments/planEstimate";
import { LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useCreateExperimentMutation, usePlanExperimentMutation, useStartExperimentMutation } from "@/lib/store/apis/benchApi";
import type { Catalog, Experiment, ExperimentPlan } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { zodResolver } from "@hookform/resolvers/zod";
import { useId, useState } from "react";
import { Controller, useFieldArray, useForm, useWatch, type Control, type FieldError, type UseFormRegister } from "react-hook-form";
import { useTranslation } from "react-i18next";
import {
	conditionsFor,
	experimentFormSchema,
	suiteRef,
	toExperiment,
	type ExperimentFormValues,
	type ValidationKey,
} from "./experimentFormSchema";

export interface ExperimentFormProps {
	catalog: Catalog;
	defaults: ExperimentFormValues;
	/** Called once the experiment is created and started, with its id and spend cap. */
	onStarted: (expId: string, capUsd: number) => void;
}

interface Planned {
	/** The form values the plan was made from, to tell when the plan went stale. */
	snapshot: string;
	experiment: Experiment;
	plan: ExperimentPlan;
}

/** The experiment form, the planner and the create-and-start action. */
export function ExperimentForm({ catalog, defaults, onStarted }: ExperimentFormProps) {
	const { t } = useTranslation("bench");
	const form = useForm<ExperimentFormValues>({ resolver: zodResolver(experimentFormSchema), defaultValues: defaults, mode: "onTouched" });
	const { control, register, handleSubmit, setValue, formState } = form;
	const values = useWatch({ control });
	const [token, setToken] = useState(readWriteToken);
	const [planned, setPlanned] = useState<Planned | null>(null);
	const [actionError, setActionError] = useState<{ title: string; error: unknown } | null>(null);
	// benchApi attaches the write token saved below to every mutation.
	const [plan, planState] = usePlanExperimentMutation();
	const [create, createState] = useCreateExperimentMutation();
	const [start, startState] = useStartExperimentMutation();

	const stale = planned !== null && planned.snapshot !== JSON.stringify(values);
	const busy = createState.isLoading || startState.isLoading;
	const blockReason = !planned
		? t("experiments.new.actions.needsPlan")
		: stale
			? t("experiments.new.actions.planStale")
			: planBlocksStart(planned.plan)
				? t("experiments.new.actions.blocked")
				: !token.trim()
					? t("experiments.new.actions.needsToken")
					: null;

	const onPlan = handleSubmit(async (formValues) => {
		const experiment = toExperiment(formValues);
		setActionError(null);
		setPlanned(null);
		try {
			const result = await plan(experiment).unwrap();
			setPlanned({ snapshot: JSON.stringify(formValues), experiment, plan: result });
		} catch {
			// planState.error renders the failure.
		}
	});

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
		try {
			await start(expId).unwrap();
		} catch (error) {
			setActionError({ title: t("experiments.new.actions.startError", { id: expId }), error });
		}
		// Created is frozen either way, so show its status; a failed start shows as "created".
		// The plan is spent: a second experiment needs a new plan, so one click never creates two.
		onStarted(expId, planned.experiment.budget.total_usd);
		setPlanned(null);
	};

	const suite = catalog.suites.find((s) => suiteRef(s) === values.suite);

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
						<Field label={t("experiments.new.fields.name")} error={formState.errors.name}>
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
						<Field label={t("experiments.new.fields.suite")} error={formState.errors.suite}>
							{(id) => (
								<Controller
									control={control}
									name="suite"
									render={({ field }) => (
										<Select
											value={field.value}
											onValueChange={(next) => {
												field.onChange(next);
												const nextSuite = catalog.suites.find((s) => suiteRef(s) === next);
												setValue("conditions", conditionsFor(nextSuite), { shouldValidate: formState.isSubmitted });
												setValue("primaryCondition", nextSuite?.probes[0] ?? "clean");
											}}
										>
											<SelectTrigger id={id} className="w-full" data-testid="experiment-suite-select">
												<SelectValue />
											</SelectTrigger>
											<SelectContent>
												{catalog.suites.map((s) => (
													<SelectItem key={suiteRef(s)} value={suiteRef(s)} data-testid={`experiment-suite-option-${s.id}`}>
														{t("experiments.new.fields.suiteOption", { id: s.id, version: s.version, count: s.tasks.length })}
													</SelectItem>
												))}
											</SelectContent>
										</Select>
									)}
								/>
							)}
						</Field>
						<Field label={t("experiments.new.fields.base")} error={formState.errors.base}>
							{(id) => (
								<Controller
									control={control}
									name="base"
									render={({ field }) => (
										<Select value={field.value} onValueChange={field.onChange}>
											<SelectTrigger id={id} className="w-full" data-testid="experiment-base-select">
												<SelectValue />
											</SelectTrigger>
											<SelectContent>
												{catalog.agents.map((agent) => (
													<SelectItem key={agent.path} value={agent.path} data-testid={`experiment-base-option-${agent.id}`}>
														{t("experiments.new.fields.baseOption", { id: agent.id, model: agent.model })}
													</SelectItem>
												))}
											</SelectContent>
										</Select>
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
							<Field label={t("experiments.new.fields.label")} error={formState.errors.armBLabel}>
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
							<div className="grid gap-3">
								<Field label={t("experiments.new.fields.overridePath")} error={formState.errors.overridePath}>
									{(id, describedBy) => (
										<Input
											id={id}
											aria-describedby={describedBy}
											aria-invalid={Boolean(formState.errors.overridePath)}
											placeholder={t("experiments.new.fields.overridePathPlaceholder")}
											className="font-mono"
											autoComplete="off"
											spellCheck={false}
											data-testid="experiment-override-path-input"
											{...register("overridePath")}
										/>
									)}
								</Field>
								<Field label={t("experiments.new.fields.overrideValue")} error={formState.errors.overrideValue}>
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
						</div>
					</div>
				</FormSection>

				<FormSection title={t("experiments.new.sections.primary")} description={t("experiments.new.fields.primaryHint")}>
					<div className="grid gap-4 md:grid-cols-2">
						<Field label={t("experiments.new.fields.primaryCondition")} error={formState.errors.primaryCondition}>
							{(id, describedBy) => (
								<Controller
									control={control}
									name="primaryCondition"
									render={({ field }) => (
										<Select value={field.value} onValueChange={field.onChange}>
											<SelectTrigger
												id={id}
												aria-describedby={describedBy}
												className="w-full font-mono"
												data-testid="experiment-primary-condition-select"
											>
												<SelectValue />
											</SelectTrigger>
											<SelectContent>
												{(values.conditions ?? []).map((c) =>
													c?.condition ? (
														<SelectItem
															key={c.condition}
															value={c.condition}
															data-testid={`experiment-primary-condition-option-${c.condition}`}
														>
															{c.condition}
														</SelectItem>
													) : null,
												)}
											</SelectContent>
										</Select>
									)}
								/>
							)}
						</Field>
						<Field label={t("experiments.new.fields.primaryMetric")} error={formState.errors.primaryMetric}>
							{(id) => (
								<Controller
									control={control}
									name="primaryMetric"
									render={({ field }) => (
										<Select value={field.value} onValueChange={field.onChange}>
											<SelectTrigger id={id} className="w-full" data-testid="experiment-primary-metric-select">
												<SelectValue />
											</SelectTrigger>
											<SelectContent>
												{catalog.metrics.map((m) => (
													<SelectItem key={m.name} value={m.name} data-testid={`experiment-primary-metric-option-${m.name}`}>
														{m.label}
													</SelectItem>
												))}
											</SelectContent>
										</Select>
									)}
								/>
							)}
						</Field>
					</div>
				</FormSection>

				<FormSection title={t("experiments.new.sections.conditions")}>
					<ConditionsEditor control={control} register={register} catalog={catalog} tasks={suite?.tasks ?? []} />
					{formState.errors.conditions?.message && (
						<ErrorText message={formState.errors.conditions.message} testId="experiment-conditions-error" />
					)}
					{formState.errors.conditions?.root?.message && (
						<ErrorText message={formState.errors.conditions.root.message} testId="experiment-conditions-error" />
					)}
				</FormSection>

				<FormSection title={t("experiments.new.sections.run")}>
					<div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
						<Field label={t("experiments.new.fields.seed")} error={formState.errors.seed}>
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
						<Field label={t("experiments.new.fields.concurrency")} error={formState.errors.concurrency}>
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
						<Field label={t("experiments.new.fields.totalUsd")} error={formState.errors.totalUsd}>
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
						<Field label={t("experiments.new.fields.token")} hint={t("experiments.new.fields.tokenHint")}>
							{(id, describedBy) => (
								<Input
									id={id}
									type="password"
									autoComplete="off"
									aria-describedby={describedBy}
									placeholder={t("experiments.new.fields.tokenPlaceholder")}
									value={token}
									onChange={(e) => {
										setToken(e.target.value);
										saveWriteToken(e.target.value);
									}}
									data-testid="experiment-write-token-input"
								/>
							)}
						</Field>
					</div>
				</FormSection>
			</div>

			<aside className="flex flex-col gap-4 xl:sticky xl:top-0">
				<Card className="gap-4 py-4 shadow-none" data-testid="plan-card">
					<CardHeader className="px-4">
						<CardTitle className="text-sm">{t("experiments.new.plan.title")}</CardTitle>
						<CardDescription className="text-xs">{t("experiments.new.plan.description")}</CardDescription>
					</CardHeader>
					<CardContent className="flex flex-col gap-4 px-4">
						<div className="flex flex-wrap gap-2">
							<Button type="submit" variant="outline" size="sm" isLoading={planState.isLoading} dataTestId="experiment-plan-button">
								{t("experiments.new.actions.plan")}
							</Button>
							<Button
								type="button"
								size="sm"
								disabled={blockReason !== null || busy}
								isLoading={busy}
								onClick={() => void onCreateAndStart()}
								dataTestId="experiment-create-start-button"
							>
								{t("experiments.new.actions.createAndStart")}
							</Button>
						</div>
						{blockReason && (
							<p
								className={cn("text-xs", stale ? "text-chart-warning-ink" : "text-muted-foreground")}
								data-testid="experiment-start-blocked-reason"
							>
								{blockReason}
							</p>
						)}
						{planState.error && !planState.isLoading && (
							<LoadError title={t("experiments.new.actions.planError")} error={planState.error} testId="experiment-plan-error" />
						)}
						{actionError && <LoadError title={actionError.title} error={actionError.error} testId="experiment-action-error" />}
						{planned && <PlanResult plan={planned.plan} className={cn(stale && "opacity-60")} />}
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
	tasks,
}: {
	control: Control<ExperimentFormValues>;
	register: UseFormRegister<ExperimentFormValues>;
	catalog: Catalog;
	tasks: readonly string[];
}) {
	const { t } = useTranslation("bench");
	const { fields } = useFieldArray({ control, name: "conditions" });
	const conditions = useWatch({ control, name: "conditions" });

	return (
		<ul className="flex flex-col divide-y rounded-sm border">
			{fields.map((field, index) => {
				const current = conditions?.[index];
				const enabled = Boolean(current?.enabled);
				const probe = catalog.probes.find((p) => p.id === field.condition);
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
								<span className="text-muted-foreground text-xs">
									{probe
										? t("experiments.new.fields.conditionProbe", { layer: t(`receipts.layers.${probe.layer}`) })
										: t("experiments.new.fields.conditionClean")}
								</span>
							</label>
							<label className="flex items-center gap-2 text-xs">
								<span className="text-muted-foreground">{t("experiments.new.fields.trials")}</span>
								<Input
									type="number"
									inputMode="numeric"
									min={1}
									max={50}
									disabled={!enabled}
									className="h-8 w-20"
									data-testid={`experiment-condition-${field.condition}-trials`}
									{...register(`conditions.${index}.trials`, { valueAsNumber: true })}
								/>
							</label>
						</div>
						{enabled && (
							<TaskSelection
								control={control}
								index={index}
								conditionId={field.condition}
								tasks={tasks}
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
	allTasks,
}: {
	control: Control<ExperimentFormValues>;
	index: number;
	conditionId: string;
	tasks: readonly string[];
	allTasks: boolean;
}) {
	const { t } = useTranslation("bench");
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
								type="radio"
								name={groupName}
								className="accent-primary cursor-pointer"
								checked={field.value}
								onChange={() => field.onChange(true)}
								data-testid={`experiment-condition-${conditionId}-tasks-all`}
							/>
							{t("experiments.new.fields.tasksAll", { count: tasks.length })}
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
								{tasks.map((task) => {
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
	children,
}: {
	label: string;
	error?: FieldError;
	hint?: string;
	children: (id: string, describedBy: string | undefined) => React.ReactNode;
}) {
	const id = useId();
	const hintId = `${id}-hint`;
	const errorId = `${id}-error`;
	const describedBy = [hint ? hintId : null, error?.message ? errorId : null].filter(Boolean).join(" ") || undefined;
	return (
		<div className="flex min-w-0 flex-col gap-1.5">
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

function ErrorText({ message, id, testId }: { message: string; id?: string; testId?: string }) {
	const { t } = useTranslation("bench");
	return (
		<p id={id} role="alert" className="text-chart-error-ink text-xs" data-testid={testId}>
			{t(message as ValidationKey)}
		</p>
	);
}