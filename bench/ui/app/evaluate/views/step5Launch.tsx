import { EvaluationWorkerPicker } from "@/components/workers/whereRunsGo";
import { imageWaitText } from "@/components/estimate/imageWait";
import type { TaskImageWait } from "@/lib/types/gateway";
import {
	type EvaluationArm,
	type EvaluationEstimate,
	type EvaluationSpec,
	type FaultCatalogItem,
	useCreateEvaluationMutation,
	useEstimateEvaluationQuery,
	useGetBenchFaultsQuery,
} from "@/lib/store/apis/evaluationsApi";
import { KeyMissingNote } from "@/components/models/keyMissingNote";
import { providerName } from "@/components/models/providerCatalog";
import { PaidButton } from "@/components/ui/paidButton";
import { formatUsd } from "@/lib/money";
import { notify } from "@/lib/notify";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { BudgetAdvice, perRunLowCost } from "./budgetAdvice";
import { defaultName, useEvaluateDraft, type EvaluateDraftContextValue } from "./evaluateDraft";
import { fw05 } from "./fw05I18n";
import { OWN_MODEL_LABEL, useOwnModelAgents } from "./ownModel";
import { estimateBasis, FAULTS, readyTasks, specProblem } from "./step4Setup";
import { FreeCheck, freeCheckPlan } from "./freeCheck";
import { firstMissingKey, KeyLines, useArmKeys } from "./armKeys";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import type { AgentWithCaps } from "./evaluate.utils";
import { isRoughEstimate } from "./evaluate.utils";

/** Wizard step 5: a plain summary of the evaluation, an optional budget and the Launch button. */

export function armText(a: EvaluationArm): string {
	const agent = a.agent || fw05("summary.yourAgent");
	// No version means the latest one; the number is not known here without asking the server.
	const version = a.version ? ` v${a.version}` : a.agent ? fw05("launch.latestVersion") : "";
	// The model by its own name, as every other screen shows it (Q-256): no provider prefix.
	const model = a.model ? fw05("summary.onModel", { model: a.model }) : a.agent ? fw05("summary.onModel", { model: OWN_MODEL_LABEL }) : "";
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
export function summaryLines(
	spec: EvaluationSpec,
	catalog: readonly FaultCatalogItem[] = FAULTS,
	est?: Pick<EvaluationEstimate, "power">,
): string[] {
	const [a, b] = spec.arms;
	const lines: string[] = [];
	if (spec.mode === "score" || !b) lines.push(fw05("summary.score", { arm: a ? armText(a) : fw05("summary.yourAgent") }));
	else if (spec.mode === "versions") lines.push(fw05("summary.versions", { a: armText(a), b: armText(b) }));
	else if (spec.mode === "models") lines.push(fw05("summary.models", { a: armText(a), b: armText(b) }));
	else lines.push(fw05("summary.opponent", { a: armText(a), b: armText(b) }));

	const n = spec.benchmarks.length;
	if (n > 0) lines.push(fw05(n === 1 ? "summary.benchmarks_one" : "summary.benchmarks_other", { n: String(n), list: spec.benchmarks.join(", ") }));
	else if (spec.pack) lines.push(fw05("summary.pack", { pack: spec.pack }));

	const trials = spec.trials === 1 ? fw05("summary.once") : fw05("summary.times", { n: String(spec.trials) });
	// The task count agrees with the runs (Q-8): a benchmark with fewer ready tasks than asked says how many it has.
	const ready = readyTasks(spec, est);
	lines.push(
		ready === null
			? fw05("summary.tasks", { n: String(spec.tasks_per_benchmark), trials })
			: fw05("summary.tasksReady", { n: String(spec.tasks_per_benchmark), ready: String(ready), trials }),
	);

	const faults = spec.faults.filter((id) => id !== "clean").map((id) => faultText(id, catalog));
	lines.push(faults.length ? fw05("summary.faults", { faults: faults.join(", ") }) : fw05("summary.clean"));

	if (spec.mcp_route === "bifrost") lines.push(fw05("summary.mcpGateway"));
	else if (spec.mcp_route === "bifrost_code_mode") lines.push(fw05("summary.mcpCodeMode"));
	if (spec.bifrost_cache) lines.push(fw05("summary.cache"));
	if (spec.failover?.enabled) {
		const models = spec.failover.fallbacks.map((f) => (f.provider ? `${f.provider}/${f.model}` : f.model)).filter(Boolean);
		lines.push(fw05("summary.failover", { models: models.join(", ") }));
	}
	if (spec.gate) lines.push(fw05("summary.gate"));

	// Rounded as the Launch label rounds it, so the two never differ (round 2 rules: one number, one rounding).
	if (spec.budget_usd && spec.budget_usd > 0) lines.push(fw05("summary.budget", { usd: formatUsd(spec.budget_usd) }));
	else lines.push(fw05("summary.defaultBudget", { usd: formatUsd(DEFAULT_BENCHMARK_BUDGET) }));
	return lines;
}

/**
 * The budget to start the box from (Q-12): the server's suggestion, once, when none is set and the estimate has
 * settled with a known cost; undefined otherwise (never while it loads, never from an unknown price).
 */
export function seedBudget(p: {
	seeded: boolean;
	fetching: boolean;
	advice: { suggested_usd: number } | undefined;
	costKnown: boolean;
}): number | undefined {
	if (p.seeded || p.fetching || !p.costKnown || !p.advice || !(p.advice.suggested_usd > 0)) return undefined;
	return p.advice.suggested_usd;
}

/** The server's spend cap per benchmark when an evaluation sets no budget (EvaluationDefaultBenchmarkBudget). */
export const DEFAULT_BENCHMARK_BUDGET = 5;

/** The most one evaluation may spend (the server's EvaluationMaxBudget). */
export const MAX_BUDGET = 1000;

/** What is wrong with the budget box text, in plain words; "" when it is blank (the default) or a valid amount. */
export function budgetProblem(text: string, benchmarks = 1): string {
	const t = text.trim();
	if (t === "") return "";
	const n = Number(t);
	if (!Number.isFinite(n)) return fw05("launch.budgetNotNumber");
	if (n <= 0) return fw05("launch.budgetNotPositive", { cap: String(DEFAULT_BENCHMARK_BUDGET) });
	if (n > MAX_BUDGET) return fw05("launch.budgetTooHigh", { max: String(MAX_BUDGET) });
	const count = Math.max(1, benchmarks);
	if (n < MIN_BENCHMARK_BUDGET * count - 1e-9) {
		// The server refuses the same budget (fix wave 3, N190: EvaluationMinBenchmarkBudget).
		return fw05("launch.budgetBelowFloor", {
			min: `$${(MIN_BENCHMARK_BUDGET * count).toFixed(2)}`,
			count: `${count} ${count === 1 ? "benchmark" : "benchmarks"}`,
		});
	}
	return "";
}

/** The least budget per benchmark the server takes (EvaluationMinBenchmarkBudget). */
export const MIN_BENCHMARK_BUDGET = 0.01;

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

function errorMessage(error: unknown, fallback: string): string {
	const data = (error as { data?: { error?: { message?: string } | string } } | undefined)?.data;
	if (typeof data?.error === "string") return data.error;
	return data?.error?.message ?? (getBenchErrorMessage(error) || fallback);
}

/** Where a missing key is added: Settings, Model keys, with the provider set and a way back to step 5 (M3-27, M8-12). */
export function addKeyHref(provider: string | undefined): string {
	const q = new URLSearchParams({ tab: "keys" });
	if (provider) q.set("add", provider);
	q.set("return", "/evaluate?step=5");
	return `/settings?${q.toString()}`;
}

/** The extra step 5 lines from the estimate (M3-28): runs and minutes, and what happens after Launch. */
export function estimateLines(
	t: (key: string, opts?: Record<string, unknown>) => string,
	est:
		| {
				runs: number;
				est_minutes?: { low: number; high: number };
				est_cost_usd?: { low: number; high: number; known?: boolean };
				per_benchmark?: readonly object[] | null;
				image_wait?: TaskImageWait | null;
		  }
		| undefined,
): string[] {
	const out: string[] = [];
	if (est && est.runs > 0 && est.est_minutes) {
		const min = Math.max(1, Math.round(est.est_minutes.low));
		const max = Math.max(1, Math.round(est.est_minutes.high));
		// "1 min", never "1 to 1 min" (Q-11): the same rounding as step 4.
		out.push(min === max ? t("launch.runsTimeOne", { runs: est.runs, min }) : t("launch.runsTime", { runs: est.runs, min, max }));
	}
	// Task images are built before any run starts (lane H2): the same estimate answer, no second request.
	const images = est && est.runs > 0 ? imageWaitText(t, est.image_wait) : null;
	if (images) out.push(images);
	// A rough list price guess is labelled as rough (G1 estimate_basis, M3-28).
	if (est?.est_cost_usd && isRoughEstimate(est)) {
		out.push(t("launch.rough", { low: formatUsd(est.est_cost_usd.low), high: formatUsd(est.est_cost_usd.high) }));
	}
	out.push(t("launch.landing"));
	return out;
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
				className="border-input bg-background h-(--control-h) w-full max-w-md rounded-md border px-2 text-sm"
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
	/** The wizard's sticky footer slot: the Launch button renders there when given (M3-26); inline otherwise. */
	launchSlot?: HTMLElement | null;
}

export function Step5Launch({ spec, onChange, launchSlot }: Step5LaunchProps) {
	const { t } = useTranslation("evaluate");
	const navigate = useNavigate();
	const [create, state] = useCreateEvaluationMutation();
	const [budget, setBudget] = useState(spec.budget_usd ? String(spec.budget_usd) : "");
	const problem = specProblem(spec, useOwnModelAgents());
	const ready = problem === "";
	const budgetIssue = budgetProblem(budget, spec.benchmarks.length);
	const estimate = useEstimateEvaluationQuery(spec, { skip: !ready });
	const catalog = useGetBenchFaultsQuery();
	const faultCatalog = catalog.data && catalog.data.length > 0 ? catalog.data : FAULTS;
	// A bad budget is not the default budget: the summary leaves the budget line out until it is fixed.
	const summary = budgetIssue ? summaryLines(spec, faultCatalog, estimate.data).slice(0, -1) : summaryLines(spec, faultCatalog, estimate.data);
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
	// "Estimated from past runs" only with real history; a rough list price guess has its own line (Q-10).
	const basisKind = estimate.data && !fetching ? estimateBasis(estimate.data) : null;
	const basis = basisKind === "rough" ? null : basisKind;
	const setBudgetUsd = (n: number | undefined) => {
		setBudget(n ? String(n) : "");
		onChange({ ...spec, budget_usd: n });
	};
	// The box follows the spec when the budget changes from outside (the seed, the draft): the Launch label and the
	// box always show the same amount (Q-12).
	useEffect(() => {
		if (parseBudget(budget) !== spec.budget_usd) setBudget(spec.budget_usd ? String(spec.budget_usd) : "");
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [spec.budget_usd]);
	// Start the budget box from the suggestion, once, when none is set, and only from a settled estimate with a
	// known cost: never while it loads or when the price is not known (Q-12).
	const [seeded, setSeeded] = useState(Boolean(spec.budget_usd));
	const seed = seedBudget({ seeded, fetching, advice, costKnown: Boolean(cost?.known) });
	useEffect(() => {
		if (seed === undefined) return;
		setSeeded(true);
		setBudgetUsd(seed);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [seed]);

	const runs = estimate.data?.runs ?? 0;
	const defaultCap = DEFAULT_BENCHMARK_BUDGET * Math.max(1, spec.benchmarks.length);
	// The most the launch can spend: the spec's budget (what the box shows), else the default cap of each benchmark.
	const capUsd = spec.budget_usd && spec.budget_usd > 0 ? spec.budget_usd : defaultCap;
	const suggestedShown = Boolean(advice && advice.suggested_usd > 0 && spec.budget_usd === advice.suggested_usd);
	// Each setup's model key (Q-11): "Uses your ... key", or the missing note; a missing key blocks Launch.
	const keys = useArmKeys(spec.arms);
	const missingKey = firstMissingKey(keys.rows);
	const agentsQ = useGetAgentsQuery();
	// The free check panel only shows when a check can run for this agent (Q-38).
	const freeCheckOk = freeCheckPlan(spec, ((agentsQ.data?.agents ?? []) as AgentWithCaps[])).ok;
	const blocked = !ready ? problem : missingKey ? t("nextReason.needKey", { provider: missingKey.providerName }) : "";
	const launch = async () => {
		const typed = nameValue.trim();
		const body: EvaluationSpec = { ...spec, name: typed || spec.name.trim() || summaryLines(spec, faultCatalog)[0].replace(/\.$/, "") };
		const res = await create(body);
		if ("data" in res && res.data) {
			// The draft is done once the evaluation exists (M4-88).
			draft?.reset();
			notify({ text: t("started", { ns: "toast", name: body.name }) });
			void navigate({ href: `/evaluations/${encodeURIComponent(res.data.id)}` });
		}
	};

	return (
		<div className="flex flex-col gap-5" data-testid="step5-launch">
			<NameField value={nameValue} placeholder={namePlaceholder} onName={onName} />

			<section className="flex flex-col gap-2">
				<h3 className="text-base font-semibold">{t("launch.whatHappens")}</h3>
				<ul className="list-disc space-y-1 pl-5 text-sm" data-testid="step5-summary">
					{[...summary, ...estimateLines(t as never, estimate.data)].map((line) => (
						<li key={line}>{line}</li>
					))}
				</ul>
				<KeyLines rows={keys.rows} returnTo="/evaluate?step=5" testId="step5-key" />
			</section>

			<section className="flex flex-col gap-1.5">
				<label htmlFor="step5-budget" className="text-sm font-medium">
					{t("launch.budget")}
				</label>
				<span className="relative w-fit">
				<span className="text-muted-foreground pointer-events-none absolute inset-y-0 left-2 flex items-center text-sm" aria-hidden="true">
					$
				</span>
				<input
					id="step5-budget"
					type="text"
					inputMode="decimal"
					aria-invalid={budgetIssue ? true : undefined}
					aria-describedby={budgetIssue ? "step5-budget-problem" : undefined}
					className="border-input bg-background h-(--control-h) w-40 rounded-md border pr-2 pl-6 text-sm"
					placeholder={fw05("launch.budgetPlaceholder", { cap: String(DEFAULT_BENCHMARK_BUDGET) })}
					value={budget}
					onChange={(e) => {
						setSeeded(true);
						setBudget(e.target.value);
						onChange({ ...spec, budget_usd: parseBudget(e.target.value) });
					}}
					data-testid="step5-budget"
				/>
				</span>
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
				{suggestedShown && (
					// Why the box holds this amount, right at the box (Q-12), and the default cap one click away.
					<span className="flex flex-wrap items-center gap-x-2 text-xs" data-testid="step5-budget-why">
						<span>{fw05("launch.suggestedWhy", { runs: String(runs) })}</span>
						<button
							type="button"
							className="text-primary underline-offset-2 hover:underline"
							onClick={() => (setSeeded(true), setBudgetUsd(undefined))}
							data-testid="step5-budget-default"
						>
							{fw05("launch.useDefaultCap", { cap: formatUsd(DEFAULT_BENCHMARK_BUDGET), total: formatUsd(defaultCap) })}
						</button>
					</span>
				)}
				<span className="text-muted-foreground text-xs" data-testid="step5-budget-help">
					{spec.budget_usd ? fw05("launch.budgetSplit") : fw05("launch.budgetEmpty", { cap: formatUsd(DEFAULT_BENCHMARK_BUDGET) })}
				</span>
				{advice && (
					<BudgetAdvice
						budget={advice}
						cost={cost}
						isFetching={fetching}
						onSet={(n) => (setSeeded(true), setBudgetUsd(n))}
						perRunLow={perRunLowCost(estimate.data?.per_benchmark)}
						benchmarks={Math.max(1, spec.benchmarks.length)}
					/>
				)}
				{basis && (
					<span className="text-muted-foreground text-xs" data-testid="step5-estimate-basis">
						{fw05(basis === "library" ? "basis.library" : "basis.own")}
					</span>
				)}
			</section>

			<EvaluationWorkerPicker value={spec.worker ?? ""} onChange={(worker) => onChange({ ...spec, worker: worker || undefined })} />

			{/* Decision 2: the free agent check, offered where a new user decides to pay. */}
			{freeCheckOk && <FreeCheck spec={spec} />}

			{state.error ? (
				<div className="text-chart-error-ink space-y-2 text-sm" role="alert" data-testid="step5-error">
					<p>{errorMessage(state.error, t("launch.error"))}</p>
					{errorLink(state.error) === "/models" ? (
						spec.arms[0]?.provider ? (
							// The missing key is said here, with a way back to this step (M8-12, M3-27).
							<span data-testid="step5-error-link">
								<KeyMissingNote
									provider={spec.arms[0].provider}
									providerName={providerName(spec.arms[0].provider, [])}
									returnTo="/evaluate?step=5"
								/>
							</span>
						) : (
							<Link to={addKeyHref(undefined) as never} className="underline underline-offset-2" data-testid="step5-error-link">
								{t("launch.addKeyPlain")}
							</Link>
						)
					) : null}
				</div>
			) : null}

			{(() => {
				const button = (
					// Launch starts paid work: the shared paid button names the cap and asks first over $1 (M3-23, Spec 11, M1-51).
					<PaidButton
						verb={t("launchVerb")}
						capUsd={capUsd}
						estimateRuns={runs > 0 ? runs : undefined}
						estimateUsd={cost?.known ? cost.high : null}
						estimateAt="title"
						confirm="auto"
						kind="form"
						confirmText={spec.budget_usd ? t("launchConfirm", { runs, usd: formatUsd(capUsd) }) : t("launchConfirmDefault", { runs })}
						disabled={blocked !== "" || budgetIssue !== ""}
						isLoading={state.isLoading}
						onConfirm={launch}
						testId="step5-launch-button"
					/>
				);
				const reason = blocked ? (
					<p className="text-muted-foreground text-xs" data-testid="step5-not-ready">
						{blocked}
					</p>
				) : null;
				// In the wizard the button and its reason sit in the sticky footer (M3-26).
				if (launchSlot)
					return createPortal(
						<span className="flex items-center gap-3">
							{reason}
							{button}
						</span>,
						launchSlot,
					);
				return (
					<div className="flex flex-col gap-1">
						{button}
						{reason}
					</div>
				);
			})()}
		</div>
	);
}

export default Step5Launch;