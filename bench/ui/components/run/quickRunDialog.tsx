import { ModelPicker, type ModelRef } from "@/components/models/modelPicker";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SkillScanProblem, parseSkillRefusal } from "@/app/evaluations_/$evalId/views/skillScanProblem";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import { useQuickRunMutation } from "@/lib/store/apis/quickrunApi";
import type { QuickRunRequest, QuickRunResponse } from "@/lib/types/quickrun";
import "@/lib/types/wizardI18n";
import { Link } from "@tanstack/react-router";
import { CircleCheck, Play, SlidersHorizontal, X, Zap } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

export const QUICKRUN_DEFAULT_TASKS = 5;

export interface QuickRunDialogProps {
	suite?: string;
	agent?: string;
	model?: ModelRef;
	tasks?: number;
	onClose(): void;
}

/** Message of a failed RTK mutation: the server's error.message, else a status line. */
export function quickRunError(err: unknown): string {
	if (!err || typeof err !== "object") return String(err ?? "");
	const e = err as { status?: unknown; data?: { error?: { message?: string } }; error?: string };
	return e.data?.error?.message ?? e.error ?? `HTTP ${String(e.status ?? "error")}`;
}

/** Builds the POST /quickrun body; null while the model is missing. */
export function buildQuickRunRequest(v: {
	suite: string;
	agent: string;
	model: ModelRef | null;
	compare: ModelRef | null;
	tasks: number;
	trials: number;
	budget: number;
}): QuickRunRequest | null {
	if (!v.model || !v.model.model.trim()) return null;
	const req: QuickRunRequest = {
		model: { ...v.model, model: v.model.model.trim() },
		tasks: v.tasks,
		trials: v.trials,
		budget_usd: v.budget,
	};
	if (v.suite) req.suite = v.suite;
	if (v.agent) req.agent = v.agent;
	if (v.compare && v.compare.model.trim()) req.compare_model = { ...v.compare, model: v.compare.model.trim() };
	return req;
}

/** The limits of the three numbers (the server refuses more than 50 tasks), fix wave 3, N229. */
export const QUICKRUN_LIMITS = { tasks: [1, 50], trials: [1, 5], budget: [0.5, 100] } as const;

/** What is wrong with each typed number, as a quickrun key; an empty object when all three are fine. */
export function quickRunProblems(v: { tasks: string; trials: string; budget: string }): {
	tasks?: string;
	trials?: string;
	budget?: string;
} {
	const out: { tasks?: string; trials?: string; budget?: string } = {};
	const whole = (text: string, [lo, hi]: readonly [number, number]) => {
		const n = Number(text);
		return text.trim() !== "" && Number.isInteger(n) && n >= lo && n <= hi;
	};
	if (!whole(v.tasks, QUICKRUN_LIMITS.tasks)) out.tasks = "limits.tasks";
	if (!whole(v.trials, QUICKRUN_LIMITS.trials)) out.trials = "limits.trials";
	const b = Number(v.budget);
	if (v.budget.trim() === "" || !Number.isFinite(b) || b < QUICKRUN_LIMITS.budget[0] || b > QUICKRUN_LIMITS.budget[1])
		out.budget = "limits.budget";
	return out;
}

/** Escape closes the dialog, as GovDialog does (fix wave 3, N229). */
export function closeOnEscape(onClose: () => void): (e: { key: string }) => void {
	return (e) => {
		if (e.key === "Escape") onClose();
	};
}

function formatCost(res: QuickRunResponse): string | null {
	const c = res.estimate?.cost_usd;
	if (!c || c.median == null) return null;
	const lo = c.low != null ? `$${c.low.toFixed(2)}` : null;
	const hi = c.high != null ? `$${c.high.toFixed(2)}` : null;
	return lo && hi && lo !== hi ? `$${c.median.toFixed(2)} (${lo} to ${hi})` : `$${c.median.toFixed(2)}`;
}

/** The QuickRun dialog: asks only for what the caller did not give, then one POST /quickrun. */
export function QuickRunDialog({ suite, agent, model, tasks, onClose }: QuickRunDialogProps) {
	const { t } = useTranslation("quickrun");
	const { t: tz } = useTranslation("wizard");
	const suites = useGetSuitesLightQuery(undefined, { skip: !!suite });
	const agents = useGetAgentsQuery(undefined, { skip: !!agent });
	const [run, state] = useQuickRunMutation();

	const [suiteV, setSuite] = useState(suite ?? "");
	const [agentV, setAgent] = useState(agent ?? "");
	const [modelV, setModel] = useState<ModelRef | null>(model ?? null);
	const [compareOn, setCompareOn] = useState(false);
	const [compareV, setCompare] = useState<ModelRef | null>(null);
	const [tasksT, setTasks] = useState(String(tasks ?? QUICKRUN_DEFAULT_TASKS));
	const [trialsT, setTrials] = useState("1");
	const [budgetT, setBudget] = useState("5");
	const [missingModel, setMissingModel] = useState(false);
	const problems = quickRunProblems({ tasks: tasksT, trials: trialsT, budget: budgetT });
	const invalid = Object.keys(problems).length > 0;
	const tasksV = Number(tasksT);
	const trialsV = Number(trialsT);
	const budgetV = Number(budgetT);
	useEffect(() => {
		const onKey = closeOnEscape(onClose);
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [onClose]);

	const submit = () => {
		if (invalid) return;
		const req = buildQuickRunRequest({
			suite: suiteV,
			agent: agentV,
			model: modelV,
			compare: compareOn ? compareV : null,
			tasks: tasksV,
			trials: trialsV,
			budget: budgetV,
		});
		if (!req) {
			setMissingModel(true);
			return;
		}
		setMissingModel(false);
		void run(req);
	};

	const done = state.data;
	const suiteList = suites.data?.suites ?? [];
	const agentList = agents.data?.agents ?? [];
	const fieldCls =
		"border-input bg-background h-9 w-full rounded-lg border px-2.5 text-sm transition-shadow focus-visible:ring-ring/50 focus-visible:ring-2 focus-visible:outline-none";

	return (
		<div
			className="animate-in fade-in fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm duration-200"
			data-testid="quickrun-backdrop"
		>
			<div
				role="dialog"
				aria-modal="true"
				aria-labelledby="quickrun-title"
				className="bg-card text-card-foreground animate-in zoom-in-95 slide-in-from-bottom-2 max-h-[90vh] w-full max-w-lg space-y-5 overflow-y-auto rounded-2xl border p-6 shadow-2xl duration-200"
				data-testid="quickrun-dialog"
			>
				<div className="flex items-start gap-3">
					<span
						className="bg-primary text-primary-foreground flex size-10 shrink-0 items-center justify-center rounded-xl shadow-sm"
						aria-hidden="true"
					>
						<Zap className="size-5" />
					</span>
					<div className="flex min-w-0 flex-1 flex-col gap-0.5">
						<h2 id="quickrun-title" className="text-lg font-semibold tracking-tight">
							{tz("quick.title")}
						</h2>
						<p className="text-muted-foreground text-sm">{tz("quick.intro")}</p>
					</div>
					<button
						type="button"
						onClick={onClose}
						className="text-muted-foreground hover:bg-accent hover:text-foreground -mt-1 -mr-1 rounded-lg p-1.5 transition-colors"
						aria-label={t("close")}
					>
						<X className="size-4" aria-hidden="true" />
					</button>
				</div>

				{done ? (
					<div className="animate-in fade-in zoom-in-95 space-y-4 text-sm duration-300" data-testid="quickrun-done">
						<div className="bg-chart-success/10 border-chart-success/40 flex flex-col items-center gap-2 rounded-xl border p-5 text-center">
							<span className="bg-chart-success/15 text-chart-success-ink flex size-12 items-center justify-center rounded-full">
								<CircleCheck className="size-6" aria-hidden="true" />
							</span>
							<p className="text-base font-semibold">{done.started ? tz("quick.startedTitle") : tz("quick.createdTitle")}</p>
							<p className="text-muted-foreground">
								{done.started ? t("started", { id: done.exp_id }) : t("created", { id: done.exp_id })}
							</p>
						</div>
						<p className="text-center">{formatCost(done) ? t("estimate", { cost: formatCost(done) }) : t("estimateUnknown")}</p>
						<details className="text-muted-foreground text-xs">
							<summary className="cursor-pointer">{tz("quick.details")}</summary>
							<p className="mt-1 font-mono">{t("savedAs", { path: done.path })}</p>
						</details>
						<div className="flex justify-end gap-2 pt-2">
							<Button variant="outline" onClick={onClose}>
								{t("close")}
							</Button>
							<Button asChild>
								<Link to="/experiments/$expId" params={{ expId: done.exp_id }} data-testid="quickrun-open">
									{t("open")}
								</Link>
							</Button>
						</div>
					</div>
				) : (
					<form
						className="space-y-4"
						onSubmit={(e) => {
							e.preventDefault();
							submit();
						}}
					>
						{!suite && (
							<label className="block space-y-1 text-sm">
								<span className="font-medium">{t("suite")}</span>
								<select className={fieldCls} value={suiteV} onChange={(e) => setSuite(e.target.value)} data-testid="quickrun-suite">
									<option value="">{t("suiteDefault")}</option>
									{suiteList.map((s) => (
										<option key={s.id} value={s.id}>
											{s.id}@{s.version} ({s.task_count})
										</option>
									))}
								</select>
								{suites.isSuccess && suiteList.length === 0 && (
									<Link to="/catalog" className="text-primary text-xs underline">
										{t("noSuites")}
									</Link>
								)}
							</label>
						)}
						{!agent && (
							<label className="block space-y-1 text-sm">
								<span className="font-medium">{t("agent")}</span>
								<select className={fieldCls} value={agentV} onChange={(e) => setAgent(e.target.value)} data-testid="quickrun-agent">
									<option value="">{t("agentDefault")}</option>
									{agentList.map((a) => (
										<option key={a.path} value={a.path}>
											{a.id}
										</option>
									))}
								</select>
								{agents.isSuccess && agentList.length === 0 && (
									<Link to="/agents/new" className="text-primary text-xs underline">
										{t("noAgents")}
									</Link>
								)}
							</label>
						)}
						{!model && (
							<div className="space-y-1 text-sm">
								<span className="font-medium">{t("model")}</span>
								<ModelPicker value={modelV} onChange={setModel} purpose="agent" testId="quickrun-model" />
							</div>
						)}
						<div className="bg-muted/30 space-y-2 rounded-xl border p-3 text-sm">
							<label className="flex cursor-pointer items-center gap-2 font-medium">
								<input
									type="checkbox"
									className="accent-primary size-4"
									checked={compareOn}
									onChange={(e) => setCompareOn(e.target.checked)}
									data-testid="quickrun-compare-toggle"
								/>
								<span>{t("compare")}</span>
							</label>
							{compareOn ? (
								<ModelPicker value={compareV} onChange={setCompare} purpose="agent" showAddKeyLink={false} testId="quickrun-compare" />
							) : (
								<p className="text-muted-foreground text-xs">{t("compareHint")}</p>
							)}
						</div>
						<details className="group rounded-xl border">
							<summary className="hover:bg-accent/40 flex cursor-pointer list-none items-center gap-2 rounded-xl px-3 py-2.5 text-sm font-medium">
								<SlidersHorizontal className="text-muted-foreground size-4" aria-hidden="true" />
								{tz("quick.advanced")}
								<span className="text-muted-foreground ml-auto text-xs tabular-nums">
									{tasksV} x {trialsV}, ${budgetV}
								</span>
							</summary>
							<div className="grid grid-cols-3 gap-2 border-t p-3">
								<label className="space-y-1 text-sm">
									<span className="font-medium">{t("tasks")}</span>
									<Input
										type="number"
										min={1}
										max={50}
										value={tasksT}
										onChange={(e) => setTasks(e.target.value)}
										aria-invalid={!!problems.tasks}
										data-testid="quickrun-tasks"
									/>
								</label>
								<label className="space-y-1 text-sm">
									<span className="font-medium">{t("trials")}</span>
									<Input
										type="number"
										min={1}
										max={5}
										value={trialsT}
										onChange={(e) => setTrials(e.target.value)}
										aria-invalid={!!problems.trials}
										data-testid="quickrun-trials"
									/>
								</label>
								<label className="space-y-1 text-sm">
									<span className="font-medium">{t("budget")}</span>
									<Input
										type="number"
										min={0.5}
										max={100}
										step={0.5}
										value={budgetT}
										onChange={(e) => setBudget(e.target.value)}
										aria-invalid={!!problems.budget}
										data-testid="quickrun-budget"
									/>
								</label>
							</div>
						</details>
						{invalid && (
							<div className="space-y-1 text-sm" role="alert" data-testid="quickrun-limits">
								{(["tasks", "trials", "budget"] as const).map(
									(k) =>
										problems[k] && (
											<p key={k} className="text-destructive">
												{t(problems[k] as "limits.tasks")}
											</p>
										),
								)}
							</div>
						)}
						{missingModel && (
							<p className="text-destructive text-sm" role="alert">
								{t("modelRequired")}
							</p>
						)}
						{state.error &&
							(() => {
								// wave 4, N22: a skill gate refusal offers the scan in place
								const refusal = parseSkillRefusal(quickRunError(state.error));
								return refusal ? (
									<SkillScanProblem refusal={refusal} testId="quickrun-skill-problem" />
								) : (
									<p className="text-destructive text-sm" role="alert" data-testid="quickrun-error">
										{t("failed", { message: quickRunError(state.error) })}
									</p>
								);
							})()}
						{!invalid && (
							<p className="text-muted-foreground text-right text-xs tabular-nums" data-testid="quickrun-estimate">
								{t("startEstimate", { runs: tasksV * trialsV * 2, budget: budgetV })}
							</p>
						)}
						<div className="flex justify-end gap-2 pt-2">
							<Button type="button" variant="outline" onClick={onClose}>
								{t("cancel")}
							</Button>
							<Button type="submit" disabled={state.isLoading || invalid} className="min-w-32 shadow-sm" data-testid="quickrun-submit">
								<Play className={state.isLoading ? "size-4 animate-pulse" : "size-4"} aria-hidden="true" />
								{state.isLoading ? t("submitting") : t("submit")}
							</Button>
						</div>
					</form>
				)}
			</div>
		</div>
	);
}

export default QuickRunDialog;