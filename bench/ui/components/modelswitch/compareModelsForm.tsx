import "@/lib/types/modelSwitchI18n";
import { VersionPicker } from "@/app/evaluate/views/step1Agent";
import { GroupedModelPicker } from "@/components/models/groupedModelPicker";
import { Button } from "@/components/ui/button";
import {
	type EvaluationSpec,
	useCreateEvaluationMutation,
	useGetAgentCompatibilityQuery,
	useGetEvaluationEstimateQuery,
} from "@/lib/store/apis/evaluationsApi";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
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

/** The first problem with the input as an i18n key (modelSwitch form.*), or null when it can launch. */
export function compareModelsProblem(i: CompareModelsInput): "needBenchmark" | "needModels" | "duplicate" | null {
	if (i.benchmarks.length === 0) return "needBenchmark";
	if (i.models.length < 2) return "needModels";
	const keys = i.models.map((m) => `${m.provider}/${m.model}`);
	if (new Set(keys).size !== keys.length) return "duplicate";
	return null;
}

/** The evaluation spec of mode model_switch: one arm per model, same agent and version. */
export function compareModelsSpec(i: CompareModelsInput): EvaluationSpec {
	return {
		name: "",
		mode: "model_switch" as EvaluationSpec["mode"],
		arms: i.models.map((m, n) => ({
			label: n === 0 ? "current" : `model-${n + 1}`,
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
 * "Compare models for this agent" (lane Z21): the agent and version, 2 to 6 models from the org's
 * gateway providers (open models on the org's vLLM or SGLang endpoints are listed under their
 * provider), the benchmarks this agent can run, and the server estimate before launch.
 */
export function CompareModelsForm({ agentId }: { agentId: string }) {
	const { t } = useTranslation("modelSwitch");
	const navigate = useNavigate();
	const compat = useGetAgentCompatibilityQuery(agentId);
	const runnable = (compat.data?.benchmarks ?? []).filter((b) => b.ok);
	const [version, setVersion] = useState<number | null>(null);
	const [models, setModels] = useState<PickedModel[]>([
		{ provider: "", model: "" },
		{ provider: "", model: "" },
	]);
	const [benchmarks, setBenchmarks] = useState<string[]>([]);
	const [tasks, setTasks] = useState(5);
	const [trials, setTrials] = useState(2);
	const [budget, setBudget] = useState(0);
	const [create, created] = useCreateEvaluationMutation();
	const input: CompareModelsInput = { agent: agentId, version, models, benchmarks, tasks, trials, budget };
	const problem = compareModelsProblem(input);
	const spec = compareModelsSpec(input);
	const estimate = useGetEvaluationEstimateQuery(spec, { skip: problem !== null });
	const setModel = (i: number, v: PickedModel) => setModels((ms) => ms.map((m, n) => (n === i ? v : m)));
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
						<GroupedModelPicker value={m.model || m.provider ? m : null} onChange={(v) => setModel(i, v)} testId={`compare-models-picker-${i}`} />
						{i === 0 && <span className="text-muted-foreground text-xs">{t("form.current")}</span>}
						{i >= 2 && (
							<Button size="sm" variant="ghost" onClick={() => setModels((ms) => ms.filter((_, n) => n !== i))}>
								{t("form.remove")}
							</Button>
						)}
					</div>
				))}
				{models.length < MODEL_SWITCH_MAX && (
					<Button size="sm" variant="outline" className="w-fit" onClick={() => setModels((ms) => [...ms, { provider: "", model: "" }])} dataTestId="compare-models-add">
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
				<NumberField label={t("form.budget")} value={budget} min={0} onChange={setBudget} testId="compare-models-budget" />
			</div>
			{problem ? (
				<p className="text-muted-foreground text-sm" data-testid="compare-models-problem">
					{t(`form.${problem}`)}
				</p>
			) : (
				estimate.data && (
					<p className="text-sm" data-testid="compare-models-estimate">
						{t("form.runs", { count: estimate.data.runs })}.{" "}
						{estimate.data.est_cost_usd.known
							? t("form.estimateCost", { low: estimate.data.est_cost_usd.low.toFixed(2), high: estimate.data.est_cost_usd.high.toFixed(2) })
							: t("form.estimateCostUnknown")}{" "}
						{t("form.estimateMinutes", { low: Math.round(estimate.data.est_minutes.low), high: Math.round(estimate.data.est_minutes.high) })}
					</p>
				)
			)}
			{err && (
				<p className="text-destructive text-sm" role="alert">
					{t("form.error", { message: err.data?.error?.message ?? "" })}
				</p>
			)}
			<Button className="w-fit" disabled={problem !== null || created.isLoading} onClick={launch} dataTestId="compare-models-launch">
				{created.isLoading ? t("form.launching") : t("form.launch")}
			</Button>
		</section>
	);
}

function NumberField({ label, value, min, onChange, testId }: { label: string; value: number; min: number; onChange(v: number): void; testId: string }) {
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
