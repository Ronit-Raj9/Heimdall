import { Button } from "@/components/ui/button";
import { EvaluationWorkerPicker } from "@/components/workers/whereRunsGo";
import {
	type EvaluationArm,
	type EvaluationSpec,
	type FaultCatalogItem,
	useCreateEvaluationMutation,
	useEstimateEvaluationQuery,
	useGetBenchFaultsQuery,
} from "@/lib/store/apis/evaluationsApi";
import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { BudgetAdvice } from "./budgetAdvice";
import { defaultName, useEvaluateDraft, type EvaluateDraftContextValue } from "./evaluateDraft";
import { fw05 } from "./fw05I18n";
import { OWN_MODEL_LABEL, useOwnModelAgents } from "./ownModel";
import { FAULTS, specProblem } from "./step4Setup";

/** Wizard step 5: a plain summary of the evaluation, an optional budget and the Launch button. */

function armText(a: EvaluationArm): string {
	const agent = a.agent || "your agent";
	// No version means the latest one; the number is not known here without asking the server.
	const version = a.version ? ` v${a.version}` : a.agent ? fw05("launch.latestVersion") : "";
	const model = a.model ? ` on ${a.provider ? `${a.provider}/` : ""}${a.model}` : a.agent ? ` on ${OWN_MODEL_LABEL}` : "";
	return `${agent}${version}${model}`;
}

/** A chosen fault by its catalog name in lower case; an id the catalog does not know prints as the id. */
function faultText(id: string, catalog: readonly FaultCatalogItem[]): string {
	const f = catalog.find((c) => c.id === id) ?? FAULTS.find((c) => c.id === id);
	return f ? f.label.toLowerCase() : id;
}

/**
 * One sentence per fact, in plain English, describing what the evaluation will do. `catalog` is
 * GET /faults; while it loads (or when it fails) the static list names the common faults.
 */
export function summaryLines(spec: EvaluationSpec, catalog: readonly FaultCatalogItem[] = FAULTS): string[] {
	const [a, b] = spec.arms;
	const lines: string[] = [];
	if (spec.mode === "score" || !b) lines.push(`Score ${a ? armText(a) : "your agent"}.`);
	else if (spec.mode === "versions") lines.push(`Compare two versions: ${armText(a)} against ${armText(b)}.`);
	else if (spec.mode === "models") lines.push(`Compare two models: ${armText(a)} against ${armText(b)}.`);
	else lines.push(`Compare ${armText(a)} against the baseline ${armText(b)}.`);

	const n = spec.benchmarks.length;
	if (n > 0) lines.push(`Run ${n} ${n === 1 ? "benchmark" : "benchmarks"}: ${spec.benchmarks.join(", ")}.`);
	else if (spec.pack) lines.push(`Run the ${spec.pack} pack of benchmarks.`);

	const trials = spec.trials === 1 ? "once" : `${spec.trials} times`;
	lines.push(`Use ${spec.tasks_per_benchmark} tasks from each benchmark and try each task ${trials}.`);

	const faults = spec.faults.filter((id) => id !== "clean").map((id) => faultText(id, catalog));
	lines.push(faults.length ? fw05("summary.faults", { faults: faults.join(", ") }) : "Run clean only, with no faults.");

	if (spec.mcp_route === "bifrost") lines.push(fw05("summary.mcpGateway"));
	else if (spec.mcp_route === "bifrost_code_mode") lines.push(fw05("summary.mcpCodeMode"));
	if (spec.bifrost_cache) lines.push(fw05("summary.cache"));
	if (spec.failover?.enabled) {
		const models = spec.failover.fallbacks.map((f) => (f.provider ? `${f.provider}/${f.model}` : f.model)).filter(Boolean);
		lines.push(fw05("summary.failover", { models: models.join(", ") }));
	}
	if (spec.gate) lines.push(fw05("summary.gate"));

	if (spec.budget_usd && spec.budget_usd > 0) lines.push(`Stop when spending reaches $${spec.budget_usd}.`);
	else lines.push(`Stop each benchmark when its spending reaches $${DEFAULT_BENCHMARK_BUDGET} (the default budget).`);
	return lines;
}

/** The server's spend cap per benchmark when an evaluation sets no budget (EvaluationDefaultBenchmarkBudget). */
export const DEFAULT_BENCHMARK_BUDGET = 5;

/** The most one evaluation may spend (the server's EvaluationMaxBudget). */
export const MAX_BUDGET = 1000;

/** What is wrong with the budget box text, in plain words; "" when it is blank (the default) or a valid amount. */
export function budgetProblem(text: string): string {
	const t = text.trim();
	if (t === "") return "";
	const n = Number(t);
	if (!Number.isFinite(n)) return fw05("launch.budgetNotNumber");
	if (n <= 0) return fw05("launch.budgetNotPositive", { cap: String(DEFAULT_BENCHMARK_BUDGET) });
	if (n > MAX_BUDGET) return fw05("launch.budgetTooHigh", { max: String(MAX_BUDGET) });
	return "";
}

/** Budget box text to a spec value: blank or not a positive number means no budget. */
export function parseBudget(text: string): number | undefined {
	const n = Number(text);
	return text.trim() !== "" && Number.isFinite(n) && n > 0 ? n : undefined;
}

/** The app page that fixes a launch error (the server's error.link), if any. */
export function errorLink(error: unknown): string | undefined {
	const data = (error as { data?: { error?: { link?: string } | string } } | undefined)?.data;
	const link = typeof data?.error === "object" ? data.error.link : undefined;
	return link?.startsWith("/") ? link : undefined;
}

function errorMessage(error: unknown): string {
	const data = (error as { data?: { error?: { message?: string } | string } } | undefined)?.data;
	if (typeof data?.error === "string") return data.error;
	return data?.error?.message ?? "Could not launch the evaluation.";
}

/** Longest evaluation name the Name box takes. */
export const NAME_MAX = 80;

/** The wizard draft when step 5 sits inside the /evaluate shell; null elsewhere (tests, previews). */
function useDraftOrNull(): EvaluateDraftContextValue | null {
	try {
		return useEvaluateDraft();
	} catch {
		return null;
	}
}

/** What the Name box calls: the typed name goes into the wizard draft, which carries it into the spec. */
export function draftNameSetter(draft: Pick<EvaluateDraftContextValue, "update">): (name: string) => void {
	return (name) => draft.update({ name });
}

/** The Name box at the top of step 5 (D8.name). Empty means the default name, shown as the placeholder. */
export function NameField({ value, placeholder, onName }: { value: string; placeholder: string; onName(name: string): void }) {
	return (
		<section className="flex flex-col gap-1.5">
			<label htmlFor="step5-name" className="text-sm font-medium">
				{fw05("name.label")}
			</label>
			<input
				id="step5-name"
				type="text"
				maxLength={NAME_MAX}
				className="border-input bg-background h-8 w-full max-w-md rounded-md border px-2 text-sm"
				placeholder={placeholder}
				value={value}
				onChange={(e) => onName(e.target.value.slice(0, NAME_MAX))}
				data-testid="step5-name"
			/>
		</section>
	);
}

export interface Step5LaunchProps {
	spec: EvaluationSpec;
	onChange(spec: EvaluationSpec): void;
}

export function Step5Launch({ spec, onChange }: Step5LaunchProps) {
	const navigate = useNavigate();
	const [create, state] = useCreateEvaluationMutation();
	const [budget, setBudget] = useState(spec.budget_usd ? String(spec.budget_usd) : "");
	const problem = specProblem(spec, useOwnModelAgents());
	const ready = problem === "";
	const budgetIssue = budgetProblem(budget);
	const estimate = useEstimateEvaluationQuery(spec, { skip: !ready });
	const catalog = useGetBenchFaultsQuery();
	const faultCatalog = catalog.data && catalog.data.length > 0 ? catalog.data : FAULTS;
	// A bad budget is not the default budget: the summary leaves the budget line out until it is fixed.
	const summary = budgetIssue ? summaryLines(spec, faultCatalog).slice(0, -1) : summaryLines(spec, faultCatalog);
	const draft = useDraftOrNull();
	const [ownName, setOwnName] = useState("");
	const nameValue = draft ? draft.draft.name : ownName;
	const namePlaceholder = draft ? defaultName(draft.draft) : spec.name;
	const onName = draft ? draftNameSetter(draft) : (name: string) => (setOwnName(name), onChange({ ...spec, name }));
	const advice = estimate.data?.budget;
	const cost = estimate.data?.est_cost_usd;
	const typedBudget = parseBudget(budget);
	const aboveSuggested =
		!budgetIssue && typedBudget !== undefined && advice && advice.max_usd > 0 && typedBudget > advice.max_usd ? advice.max_usd : 0;
	const fetching = Boolean(estimate.isFetching);
	const basis =
		estimate.data && !fetching && (cost?.known || estimate.data.from_library) ? (estimate.data.from_library ? "library" : "own") : null;
	const setBudgetUsd = (n: number | undefined) => {
		setBudget(n ? String(n) : "");
		onChange({ ...spec, budget_usd: n });
	};
	// Start the budget box from the suggestion (it covers the expected cost), once, when none is set.
	const [seeded, setSeeded] = useState(Boolean(spec.budget_usd));
	useEffect(() => {
		if (seeded || !advice || !(advice.suggested_usd > 0)) return;
		setSeeded(true);
		setBudgetUsd(advice.suggested_usd);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [seeded, advice]);

	const launch = async () => {
		const typed = nameValue.trim();
		const body: EvaluationSpec = { ...spec, name: typed || spec.name.trim() || summaryLines(spec, faultCatalog)[0].replace(/\.$/, "") };
		const res = await create(body);
		if ("data" in res && res.data) void navigate({ href: `/evaluations/${encodeURIComponent(res.data.id)}` });
	};

	return (
		<div className="flex flex-col gap-5" data-testid="step5-launch">
			<NameField value={nameValue} placeholder={namePlaceholder} onName={onName} />

			<section className="flex flex-col gap-2">
				<h3 className="text-base font-semibold">What will happen</h3>
				<ul className="list-disc space-y-1 pl-5 text-sm" data-testid="step5-summary">
					{summary.map((line) => (
						<li key={line}>{line}</li>
					))}
				</ul>
			</section>

			<section className="flex flex-col gap-1.5">
				<label htmlFor="step5-budget" className="text-sm font-medium">
					Budget (USD)
				</label>
				<input
					id="step5-budget"
					type="text"
					inputMode="decimal"
					aria-invalid={budgetIssue ? true : undefined}
					aria-describedby={budgetIssue ? "step5-budget-problem" : undefined}
					className="border-input bg-background h-8 w-32 rounded-md border px-2 text-sm"
					placeholder={`$${DEFAULT_BENCHMARK_BUDGET} per benchmark`}
					value={budget}
					onChange={(e) => {
						setSeeded(true);
						setBudget(e.target.value);
						onChange({ ...spec, budget_usd: parseBudget(e.target.value) });
					}}
					data-testid="step5-budget"
				/>
				{budgetIssue && (
					<span id="step5-budget-problem" className="text-destructive text-xs" role="alert" data-testid="step5-budget-problem">
						{budgetIssue}
					</span>
				)}
				{aboveSuggested > 0 && (
					<span className="text-chart-warning-ink text-xs" role="status" data-testid="step5-budget-above-max">
						{fw05("launch.budgetAboveSuggested", { max: `$${aboveSuggested}` })}
					</span>
				)}
				<span className="text-muted-foreground text-xs" data-testid="step5-budget-help">
					{spec.budget_usd
						? "Split evenly over the benchmarks; each one stops starting runs when its share is spent."
						: `Empty means the default: each benchmark stops at $${DEFAULT_BENCHMARK_BUDGET}.`}
				</span>
				{advice && <BudgetAdvice budget={advice} cost={cost} isFetching={fetching} onSet={(n) => (setSeeded(true), setBudgetUsd(n))} />}
				{basis && (
					<span className="text-muted-foreground text-xs" data-testid="step5-estimate-basis">
						{fw05(basis === "library" ? "basis.library" : "basis.own")}
					</span>
				)}
			</section>

			<EvaluationWorkerPicker value={spec.worker ?? ""} onChange={(worker) => onChange({ ...spec, worker: worker || undefined })} />

			{state.error ? (
				<p className="text-chart-error-ink text-sm" role="alert" data-testid="step5-error">
					{errorMessage(state.error)}
					{errorLink(state.error) === "/models" ? (
						<>
							{" "}
							<Link to="/models" className="underline underline-offset-2" data-testid="step5-error-link">
								Add a model key
							</Link>
						</>
					) : null}
				</p>
			) : null}

			<div>
				<Button onClick={() => void launch()} disabled={!ready || budgetIssue !== "" || state.isLoading} data-testid="step5-launch-button">
					{state.isLoading ? "Launching..." : "Launch evaluation"}
				</Button>
				{!ready && (
					<p className="text-muted-foreground mt-1 text-xs" data-testid="step5-not-ready">
						{problem}
					</p>
				)}
			</div>
		</div>
	);
}

export default Step5Launch;