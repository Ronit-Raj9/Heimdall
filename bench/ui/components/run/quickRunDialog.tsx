import { ModelPicker, type ModelRef } from "@/components/models/modelPicker";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useGetAgentsQuery, useGetSuitesQuery } from "@/lib/store/apis/benchApi";
import { useQuickRunMutation } from "@/lib/store/apis/quickrunApi";
import type { QuickRunRequest, QuickRunResponse } from "@/lib/types/quickrun";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
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
	const req: QuickRunRequest = { model: { ...v.model, model: v.model.model.trim() }, tasks: v.tasks, trials: v.trials, budget_usd: v.budget };
	if (v.suite) req.suite = v.suite;
	if (v.agent) req.agent = v.agent;
	if (v.compare && v.compare.model.trim()) req.compare_model = { ...v.compare, model: v.compare.model.trim() };
	return req;
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
	const suites = useGetSuitesQuery(undefined, { skip: !!suite });
	const agents = useGetAgentsQuery(undefined, { skip: !!agent });
	const [run, state] = useQuickRunMutation();

	const [suiteV, setSuite] = useState(suite ?? "");
	const [agentV, setAgent] = useState(agent ?? "");
	const [modelV, setModel] = useState<ModelRef | null>(model ?? null);
	const [compareOn, setCompareOn] = useState(false);
	const [compareV, setCompare] = useState<ModelRef | null>(null);
	const [tasksV, setTasks] = useState(tasks ?? QUICKRUN_DEFAULT_TASKS);
	const [trialsV, setTrials] = useState(1);
	const [budgetV, setBudget] = useState(5);
	const [missingModel, setMissingModel] = useState(false);

	const submit = () => {
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
	const fieldCls = "border-input bg-background w-full rounded-md border px-2 py-1 text-sm";

	return (
		<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" data-testid="quickrun-backdrop">
			<div
				role="dialog"
				aria-modal="true"
				aria-labelledby="quickrun-title"
				className="bg-card text-card-foreground w-full max-w-lg space-y-4 rounded-lg border p-5 shadow-lg"
				data-testid="quickrun-dialog"
			>
				<div>
					<h2 id="quickrun-title" className="text-lg font-semibold">
						{t("title")}
					</h2>
					<p className="text-muted-foreground text-sm">{t("intro")}</p>
				</div>

				{done ? (
					<div className="space-y-2 text-sm" data-testid="quickrun-done">
						<p className="font-medium">{done.started ? t("started", { id: done.exp_id }) : t("created", { id: done.exp_id })}</p>
						<p>{formatCost(done) ? t("estimate", { cost: formatCost(done) }) : t("estimateUnknown")}</p>
						<p className="text-muted-foreground font-mono text-xs">{t("savedAs", { path: done.path })}</p>
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
						className="space-y-3"
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
						<div className="space-y-1 text-sm">
							<label className="flex items-center gap-2">
								<input type="checkbox" checked={compareOn} onChange={(e) => setCompareOn(e.target.checked)} data-testid="quickrun-compare-toggle" />
								<span>{t("compare")}</span>
							</label>
							{compareOn ? (
								<ModelPicker value={compareV} onChange={setCompare} purpose="agent" showAddKeyLink={false} testId="quickrun-compare" />
							) : (
								<p className="text-muted-foreground text-xs">{t("compareHint")}</p>
							)}
						</div>
						<div className="grid grid-cols-3 gap-2">
							<label className="space-y-1 text-sm">
								<span className="font-medium">{t("tasks")}</span>
								<Input type="number" min={1} max={50} value={tasksV} onChange={(e) => setTasks(Math.max(1, Number(e.target.value) || 1))} data-testid="quickrun-tasks" />
							</label>
							<label className="space-y-1 text-sm">
								<span className="font-medium">{t("trials")}</span>
								<Input type="number" min={1} max={5} value={trialsV} onChange={(e) => setTrials(Math.max(1, Number(e.target.value) || 1))} />
							</label>
							<label className="space-y-1 text-sm">
								<span className="font-medium">{t("budget")}</span>
								<Input type="number" min={0.5} max={100} step={0.5} value={budgetV} onChange={(e) => setBudget(Math.max(0.5, Number(e.target.value) || 5))} />
							</label>
						</div>
						{missingModel && (
							<p className="text-destructive text-sm" role="alert">
								{t("modelRequired")}
							</p>
						)}
						{state.error && (
							<p className="text-destructive text-sm" role="alert" data-testid="quickrun-error">
								{t("failed", { message: quickRunError(state.error) })}
							</p>
						)}
						<div className="flex justify-end gap-2 pt-2">
							<Button type="button" variant="outline" onClick={onClose}>
								{t("cancel")}
							</Button>
							<Button type="submit" disabled={state.isLoading} data-testid="quickrun-submit">
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
