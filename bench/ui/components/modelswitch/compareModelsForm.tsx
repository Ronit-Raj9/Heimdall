import "@/lib/types/modelSwitchI18n";
import "@/app/agents/views/w4m1I18n";
import i18n from "@/lib/i18n";
import { WhyDisabled } from "@/components/ui/whyDisabled";
import { VersionPicker } from "@/app/evaluate/views/step1Agent";
import { estimateText } from "@/app/evaluate/views/step4Setup";
import { GroupedModelPicker } from "@/components/models/groupedModelPicker";
import { Button } from "@/components/ui/button";
import {
	type EvaluationSpec,
	useCreateEvaluationMutation,
	useGetAgentCompatibilityQuery,
	useGetEvaluationEstimateQuery,
} from "@/lib/store/apis/evaluationsApi";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { evalArmLabel } from "@/lib/evaluationArms";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/** The most models one comparison takes (schemas.EvaluationModelSwitchMaxArms). */
export const MODEL_SWITCH_MAX = 6;

export interface PickedModel {
	provider: string;
	model: string;
}

export interface CompareModelsInput {
	agent: string;
	version: number | null;
	models: PickedModel[];
	benchmarks: string[];
	tasks: number;
	trials: number;
	budget: number;
}

/** The rows with a model picked; the form starts with two empty rows, which never count (M4-82). */
export function filledModels(models: readonly PickedModel[]): PickedModel[] {
	return models.filter((m) => m.model.trim() !== "");
}

/** The first problem with the input as an i18n key (modelSwitch form.*), or null when it can launch. */
export function compareModelsProblem(i: CompareModelsInput): "needBenchmark" | "needModels" | "duplicate" | null {
	if (i.benchmarks.length === 0) return "needBenchmark";
	const models = filledModels(i.models);
	if (models.length < 2) return "needModels";
	const keys = models.map((m) => `${m.provider}/${m.model}`);
	if (new Set(keys).size !== keys.length) return "duplicate";
	return null;
}

/** The evaluation spec of mode model_switch: one arm per model, same agent and version. */
export function compareModelsSpec(i: CompareModelsInput): EvaluationSpec {
	return {
		name: "",
		mode: "model_switch" as EvaluationSpec["mode"],
		arms: filledModels(i.models).map((m, n) => ({
			label: evalArmLabel(n), // N211: the server takes only A, B, C ... (evalArmIDRe)
			agent: i.agent,
			...(i.version ? { version: i.version } : {}),
			model: m.model,
			...(m.provider ? { provider: m.provider } : {}),
		})),
		benchmarks: i.benchmarks,
		tasks_per_benchmark: i.tasks,
		trials: i.trials,
		faults: ["clean"],
		...(i.budget > 0 ? { budget_usd: i.budget } : {}),
	};
}

/**
 * Row 0 filled with the agent's default model (fix wave 3, N211), only while it is untouched; the
 * same list when there is nothing to fill.
 */
export function prefillCurrent(models: PickedModel[], agentModel: string | undefined): PickedModel[] {
	const first = models[0];
	if (!agentModel || !first || first.model || first.provider) return models;
	return [{ provider: "", model: agentModel }, ...models.slice(1)];
}

/**
 * "Compare models for this agent" (lane Z21): the agent and version, 2 to 6 models from the org's
 * gateway providers (open models on the org's vLLM or SGLang endpoints are listed under their
 * provider), the benchmarks this agent can run, and the server estimate before launch.
 */
export function CompareModelsForm({ agentId }: { agentId: string }) {
	const { t } = useTranslation("modelSwitch");
	const { t: tm } = useTranslation("w4m1");
	const navigate = useNavigate();
	const compat = useGetAgentCompatibilityQuery(agentId);
	const runnable = (compat.data?.benchmarks ?? []).filter((b) => b.ok);
	const [version, setVersion] = useState<number | null>(null);
	const [models, setModels] = useState<PickedModel[]>([
		{ provider: "", model: "" },
		{ provider: "", model: "" },
	]);
	const agents = useGetAgentsQuery();
	const agentModel = agents.data?.agents?.find((a) => a.id === agentId)?.model;
	const [touched, setTouched] = useState(false);
	useEffect(() => {
		if (!touched) setModels((ms) => prefillCurrent(ms, agentModel));
	}, [agentModel, touched]);
	const [benchmarks, setBenchmarks] = useState<string[]>([]);
	const [tasks, setTasks] = useState(5);
	const [trials, setTrials] = useState(2);
	const [budget, setBudget] = useState(0);
	const [budgetTouched, setBudgetTouched] = useState(false);
	const { t: tu } = useTranslation("uf4b");
	const [create, created] = useCreateEvaluationMutation();
	const input: CompareModelsInput = { agent: agentId, version, models, benchmarks, tasks, trials, budget };
	const problem = compareModelsProblem(input);
	const spec = compareModelsSpec(input);
	const estimate = useGetEvaluationEstimateQuery(spec, { skip: problem !== null });
	// Launch waits for the estimate of the current settings, never an older one (p02-5).
	const waiting = problem === null && estimate.isFetching;
	// M4-82: the budget starts at the estimate's suggestion; 0 never launches as "no cap".
	const suggested = estimate.data?.budget?.suggested_usd;
	useEffect(() => {
		if (!budgetTouched && suggested && suggested > 0) setBudget(Math.round(suggested * 100) / 100);
	}, [suggested, budgetTouched]);
	const noBudget = !(budget > 0);
	const setModel = (i: number, v: PickedModel) => {
		if (i === 0) setTouched(true);
		setModels((ms) => ms.map((m, n) => (n === i ? v : m)));
	};
	const launch = async () => {
		const res = await create(spec);
		if ("data" in res && res.data) navigate({ to: "/evaluations/$evalId", params: { evalId: res.data.id } });
	};
	const err = created.error as { data?: { error?: { message?: string } } } | undefined;
	return (
		<section className="flex flex-col gap-4" data-testid="compare-models-form">
			<div className="flex flex-col gap-1">
				<h2 className="text-base font-semibold">{t("form.title")}</h2>
				<p className="text-muted-foreground max-w-prose text-sm">{t("form.description")}</p>
			</div>
			<VersionPicker agent={agentId} value={version} onChange={setVersion} testId="compare-models-version" />
			<div className="flex flex-col gap-2">
				<span className="text-sm font-medium">{t("form.models")}</span>
				<span className="text-muted-foreground text-xs">{t("form.modelsHint", { max: MODEL_SWITCH_MAX - 1 })}</span>
				{models.map((m, i) => (
					<div key={i} className="flex items-center gap-2" data-testid={`compare-models-model-${i}`}>
						<GroupedModelPicker
							value={m.model || m.provider ? m : null}
							onChange={(v) => setModel(i, v)}
							testId={`compare-models-picker-${i}`}
							label={i === 0 ? t("form.currentModel") : t("form.modelN", { n: i + 1 })}
						/>
						{i === 0 && <span className="text-muted-foreground text-xs">{t("form.current")}</span>}
						{i >= 2 && (
							<Button size="sm" variant="ghost" onClick={() => setModels((ms) => ms.filter((_, n) => n !== i))}>
								{t("form.remove")}
							</Button>
						)}
					</div>
				))}
				{models.length < MODEL_SWITCH_MAX && (
					<Button
						size="sm"
						variant="outline"
						className="w-fit"
						onClick={() => setModels((ms) => [...ms, { provider: "", model: "" }])}
						dataTestId="compare-models-add"
					>
						{t("form.addModel")}
					</Button>
				)}
			</div>
			<fieldset className="flex flex-col gap-1 text-sm" data-testid="compare-models-benchmarks">
				<legend className="font-medium">{t("form.benchmarks")}</legend>
				<span className="text-muted-foreground text-xs">{t("form.benchmarksHint")}</span>
				{!compat.isLoading && runnable.length === 0 && <span className="text-muted-foreground">{t("form.noBenchmarks")}</span>}
				{runnable.map((b) => (
					<label key={b.id} className="flex items-center gap-2">
						<input
							type="checkbox"
							checked={benchmarks.includes(b.id)}
							onChange={(e) => setBenchmarks((bs) => (e.target.checked ? [...bs, b.id] : bs.filter((x) => x !== b.id)))}
						/>
						{b.id}
					</label>
				))}
			</fieldset>
			<div className="flex flex-wrap gap-4 text-sm">
				<NumberField label={t("form.tasks")} value={tasks} min={1} onChange={setTasks} testId="compare-models-tasks" />
				<NumberField label={t("form.trials")} value={trials} min={1} onChange={setTrials} testId="compare-models-trials" />
				<NumberField
					label={t("form.budget")}
					value={budget}
					min={0.01}
					onChange={(n) => {
						setBudgetTouched(true);
						setBudget(n);
					}}
					testId="compare-models-budget"
				/>
			</div>
			{problem ? (
				<p className="text-muted-foreground text-sm" data-testid="compare-models-problem">
					{t(`form.${problem}`)}
				</p>
			) : (
				<EstimateLine state={estimate} />
			)}
			{problem === null && noBudget && !waiting && (
				<p className="text-muted-foreground text-sm" data-testid="compare-models-need-budget">
					{tu("models.needBudget")}
				</p>
			)}
			{err && (
				<p className="text-destructive text-sm" role="alert">
					{t("form.error", { message: err.data?.error?.message ?? "" })}
				</p>
			)}
			<Button
				className="w-fit"
				disabled={problem !== null || noBudget || created.isLoading || waiting}
				aria-describedby={waiting ? "compare-models-launch-why" : undefined}
				onClick={launch}
				dataTestId="compare-models-launch"
			>
				{created.isLoading ? t("form.launching") : t("form.launch")}
			</Button>
			<WhyDisabled reason={waiting ? tm("compareModels.waitReason") : null} testId="compare-models-launch-why" />
		</section>
	);
}

/** The estimate as one sentence from the wizard's shared phrases (N211): "4 runs. $0.02. About 1 min." */
export function estimateSentence(e: Parameters<typeof estimateText>[0]): string {
	const p = estimateText(e);
	const time =
		p.minutes === "time not known"
			? i18n.t("compareModels.timeUnknown", { ns: "w4m1" })
			: i18n.t("compareModels.about", { ns: "w4m1", minutes: p.minutes });
	const runs = i18n.t("compareModels.runs", { ns: "w4m1", count: Number(e.runs) || 0 });
	return i18n.t("compareModels.estimate", { ns: "w4m1", runs, cost: `${p.cost[0].toUpperCase()}${p.cost.slice(1)}`, time });
}

/** The estimate line: the waiting text while the current settings are estimated (never the old answer), the error, or the sentence. */
export function EstimateLine({
	state,
}: {
	state: { currentData?: Parameters<typeof estimateText>[0]; isFetching: boolean; isError: boolean };
}) {
	const { t } = useTranslation("w4m1");
	if (state.isFetching) {
		return (
			<p className="text-muted-foreground text-sm" role="status" data-testid="compare-models-estimate-waiting">
				{t("compareModels.waiting")}
			</p>
		);
	}
	if (state.isError) {
		return (
			<p className="text-chart-error-ink text-sm" data-testid="compare-models-estimate-error">
				{t("compareModels.error")}
			</p>
		);
	}
	if (!state.currentData) return null;
	return (
		<p className="text-sm" data-testid="compare-models-estimate">
			{estimateSentence(state.currentData)}
		</p>
	);
}

function NumberField({
	label,
	value,
	min,
	onChange,
	testId,
}: {
	label: string;
	value: number;
	min: number;
	onChange(v: number): void;
	testId: string;
}) {
	return (
		<label className="flex flex-col gap-1">
			<span className="font-medium">{label}</span>
			<input
				type="number"
				min={min}
				className="border-input bg-background h-9 w-28 rounded-lg border px-2"
				value={value}
				onChange={(e) => onChange(Math.max(min, Number(e.target.value) || 0))}
				data-testid={testId}
			/>
		</label>
	);
}