import { InlineError } from "@/components/runs/screenStates";
import { ModelPicker } from "@/components/models/modelPicker";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { PaidButton } from "@/components/ui/paidButton";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ShowTestsChip } from "@/components/ui/toolbar";
import {
	useCreateExperimentMutation,
	useGetAgentsQuery,
	usePlanExperimentMutation,
	useStartExperimentMutation,
} from "@/lib/store/apis/benchApi";
import { useDraftGatewayTestMutation } from "@/lib/store/apis/gatewayApi";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import { isTestObject, useShowTests, withoutTests } from "@/lib/testObjects";
import type { Experiment, ExperimentPlan } from "@/lib/types/bench";
import type { GwDraftRequest, GwDraftResponse } from "@/lib/types/gateway";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import "./uf7cI18n";
import "./w4p6I18n";

/** How a setting is entered: a model picker, a number, free text, a comma list, yes or no, or one of the budgets typed above. */
export type OptionKind = "model" | "number" | "text" | "list" | "yesno" | "budgetPick";

/** One setting of a feature's own Draft (audit/W4_DESIGN_3_BIFROST.md sections 4 to 13). Its label and hint are uf7c:dialog.opt.<key>. */
export interface FeatureOption {
	key: string;
	required: boolean;
	kind: OptionKind;
}

/** The Draft options each feature reads, required first. */
export const FEATURE_OPTIONS: Record<string, FeatureOption[]> = {
	complexity_router: [
		{ key: "strong_model", required: true, kind: "model" },
		{ key: "cheap_model", required: true, kind: "model" },
		{ key: "tolerance", required: false, kind: "number" },
		{ key: "router_when", required: false, kind: "text" },
	],
	prompt_version: [
		{ key: "prompt_id", required: true, kind: "text" },
		{ key: "variant_version", required: true, kind: "text" },
		{ key: "control_version", required: false, kind: "text" },
	],
	mcp_tools: [
		{ key: "tools_b", required: true, kind: "list" },
		{ key: "tools_c", required: false, kind: "list" },
		{ key: "tools_a", required: false, kind: "list" },
		{ key: "forbidden", required: false, kind: "list" },
		{ key: "poison_probe", required: false, kind: "text" },
	],
	semantic_cache: [
		{ key: "grid", required: false, kind: "list" },
		{ key: "target", required: false, kind: "number" },
		{ key: "ttl", required: false, kind: "text" },
	],
	// The server defaults control_path to "none" (bench/gwfeature/skill.go), so only the skill to test is required.
	skill_version: [
		{ key: "variant_path", required: true, kind: "text" },
		{ key: "control_path", required: false, kind: "text" },
	],
	providers: [{ key: "targets", required: true, kind: "list" }],
	budget_curve: [
		{ key: "budgets", required: true, kind: "list" },
		{ key: "current", required: false, kind: "budgetPick" },
		{ key: "tell_agent", required: false, kind: "yesno" },
	],
	gateway_overhead: [],
};

/** Features whose Draft refuses an empty Model (bench/gwfeature/semanticcache.go: "Pick the model both setups use."). */
export const NEEDS_MODEL: ReadonlySet<string> = new Set(["semantic_cache"]);

/** The budgets typed in the budget curve's list, as the choices of "Your budget today". Pure. */
export function budgetChoices(list: string | undefined): string[] {
	return (list ?? "")
		.split(",")
		.map((v) => v.trim())
		.filter((v, i, all) => v !== "" && Number.isFinite(Number(v)) && all.indexOf(v) === i);
}

/** The suites a feature test can pick: test benchmarks are left out unless shown. Pure. */
export function pickableSuites<T extends { id: string; description?: string | null }>(suites: readonly T[], showTests = false) {
	return withoutTests(suites, (s) => isTestObject(s.id, s), showTests);
}

/** The agents a feature test can pick: test agents (by id, file path or description) are left out unless shown. Pure. */
export function pickableAgents<T extends { id: string; path?: string; description?: string | null }>(agents: readonly T[], showTests = false) {
	return withoutTests(agents, (a) => isTestObject(a.id, a) || isTestObject(a.path?.split("/").pop()), showTests);
}

export const MAX_DRAFT_USD = 50;

/** The suite reference the draft sends: "<id>@<version>", as the New comparison form sends it. */
export function suiteOption(s: { id: string; version?: string }): string {
	return s.version ? `${s.id}@${s.version}` : s.id;
}

export interface FeatureTestForm {
	/** "<id>@<version>" (suiteOption). */
	suite: string;
	/** The agent file path, for example agents/codex.yaml: the planner and the budget and overhead drafts read it as a path. */
	base: string;
	model: string;
	provider: string;
	budget: string;
	trials: string;
	options: Record<string, string>;
}

export type DraftProblem = "needBudget" | "needFields" | "needModel" | "needTrials";

/** The fields still missing or wrong, by field name ("suite", "base", "model", "budget", "trials" or an option key). Pure. */
export function missingFields(feature: string, f: FeatureTestForm): string[] {
	const out: string[] = [];
	if (!f.suite) out.push("suite");
	if (!f.base) out.push("base");
	if (NEEDS_MODEL.has(feature) && !f.model.trim()) out.push("model");
	for (const o of FEATURE_OPTIONS[feature] ?? []) if (o.required && !f.options[o.key]?.trim()) out.push(o.key);
	const budget = Number(f.budget);
	if (!f.budget.trim() || !Number.isFinite(budget) || budget <= 0 || budget > MAX_DRAFT_USD) out.push("budget");
	if (f.trials.trim() !== "") {
		const trials = Number(f.trials);
		if (!Number.isInteger(trials) || trials < 1) out.push("trials");
	}
	return out;
}

/** The draft request, or the problem that stops it. Pure. */
export function draftRequest(feature: string, f: FeatureTestForm): { req?: GwDraftRequest; problem?: DraftProblem } {
	const missing = missingFields(feature, f);
	if (missing.includes("budget")) return { problem: "needBudget" };
	if (missing.includes("trials")) return { problem: "needTrials" };
	if (missing.length === 1 && missing[0] === "model") return { problem: "needModel" };
	if (missing.length > 0) return { problem: "needFields" };
	const budget = Number(f.budget);
	const options: Record<string, string> = {};
	for (const o of FEATURE_OPTIONS[feature] ?? []) {
		const v = f.options[o.key]?.trim();
		if (v) options[o.key] = v;
	}
	const trials = Number(f.trials);
	return {
		req: {
			feature,
			suite: f.suite,
			base: f.base,
			...(f.model.trim() ? { model: f.model.trim() } : {}),
			...(f.provider.trim() ? { provider: f.provider.trim() } : {}),
			...(Number.isInteger(trials) && trials > 0 ? { trials } : {}),
			budget_usd: budget,
			...(Object.keys(options).length ? { options } : {}),
		},
	};
}

/** The three calls the Start button makes, in order. */
export interface FeatureTestCalls {
	draft(req: GwDraftRequest): Promise<GwDraftResponse>;
	plan(exp: Experiment): Promise<ExperimentPlan>;
	create(exp: Experiment): Promise<{ exp_id: string }>;
	start(expId: string): Promise<unknown>;
}

/** Step one: draft, then plan the drafted comparison (the usual estimate and refusals). */
export async function prepareFeatureTest(
	calls: FeatureTestCalls,
	req: GwDraftRequest,
): Promise<{ draft: GwDraftResponse; plan: ExperimentPlan }> {
	const draft = await calls.draft(req);
	const plan = await calls.plan(draft.experiment);
	return { draft, plan };
}

/** Step two, after the confirmation: create, then start. Answers the new comparison's id. */
export async function startFeatureTest(calls: FeatureTestCalls, exp: Experiment): Promise<string> {
	const created = await calls.create(exp);
	await calls.start(created.exp_id);
	return created.exp_id;
}

type Prepared = { draft: GwDraftResponse; plan: ExperimentPlan };

/** The plan's estimate in dollars and minutes, when both are observed. Pure. */
export function planEstimate(plan: ExperimentPlan): { usd: number; minutes: number } | null {
	if (plan.est_usd?.state !== "observed" || plan.est_usd.v === null || plan.est_wall_s?.state !== "observed" || plan.est_wall_s.v === null)
		return null;
	return { usd: plan.est_usd.v as number, minutes: Math.max(1, Math.round((plan.est_wall_s.v as number) / 60)) };
}

/** The confirmation sentence: the estimate and the cap, or the cap alone when the estimate is not known. Pure. */
export function confirmText(t: (key: "dialog.confirm" | "dialog.confirmUnknown", o: { est?: string; budget: string }) => string, plan: ExperimentPlan, budget: number) {
	const est = planEstimate(plan);
	return est
		? t("dialog.confirm", { est: est.usd.toFixed(2), budget: budget.toFixed(2) })
		: t("dialog.confirmUnknown", { budget: budget.toFixed(2) });
}

/** The estimate, notes, plan refusals and the confirmation, shown as soon as the form is complete. Pure. */
export function PreparedView({ prepared, budget, confirming }: { prepared: Prepared; budget: number; confirming: boolean }) {
	const { t } = useTranslation("w4p6");
	const { t: u } = useTranslation("uf7c");
	const { notes } = prepared.draft;
	const { plan } = prepared;
	const est = planEstimate(plan);
	return (
		<div className="flex flex-col gap-3 text-sm" data-testid="feature-test-prepared">
			<p data-testid="feature-test-estimate">
				{est ? u("dialog.estimateLine", { usd: est.usd.toFixed(2), minutes: est.minutes }) : u("dialog.estimateLineUnknown")}
			</p>
			{notes.length > 0 && (
				<div className="flex flex-col gap-1" data-testid="feature-test-notes">
					<p className="font-medium">{t("dialog.notes")}</p>
					<ul className="list-disc pl-5">
						{notes.map((n) => (
							<li key={n}>{n}</li>
						))}
					</ul>
				</div>
			)}
			{plan.errors.length > 0 && (
				<div className="text-destructive" role="alert" data-testid="feature-test-plan-errors">
					<p>{t("dialog.planErrors")}</p>
					<ul className="list-disc pl-5">
						{plan.errors.map((e) => (
							<li key={e}>{e}</li>
						))}
					</ul>
				</div>
			)}
			{confirming && (
				<p className="font-medium" data-testid="feature-test-confirm">
					{confirmText((k, o) => String(u(k, { ...o })), plan, budget)}
				</p>
			)}
		</div>
	);
}

/** The text of a benchmark option: its name and task count. Pure. */
export function suiteLabel(u: (key: "dialog.suiteOption", o: { name: string; count: number }) => string, s: { id: string; task_count?: number }) {
	return u("dialog.suiteOption", { name: s.id, count: s.task_count ?? 0 });
}

/** The text of an agent option: its name and its model when it has one. Pure. */
export function agentLabel(u: (key: "dialog.agentOption", o: { name: string; model: string }) => string, a: { id: string; model?: string }) {
	return a.model ? u("dialog.agentOption", { name: a.id, model: a.model }) : a.id;
}

/** The raw error, for the Details fold under the server's message. Pure. */
export function errorDetail(e: unknown): string {
	if (e instanceof Error) return e.message;
	try {
		return JSON.stringify(e);
	} catch {
		return String(e);
	}
}

// A suggested budget, so the estimate shows before anything is typed (the server caps it at MAX_DRAFT_USD).
const EMPTY: FeatureTestForm = { suite: "", base: "", model: "", provider: "", budget: "5", trials: "2", options: {} };

/** The wait after the last change before the estimate is worked out. */
export const ESTIMATE_DELAY_MS = 500;

/** "Start a test": pick what to run; the estimate shows as soon as the form is complete; Start test, then the confirmation. */
export function FeatureTestDialog({
	feature,
	name,
	open,
	onClose,
	initial,
}: {
	feature: string;
	name: string;
	open: boolean;
	onClose(): void;
	/** Fields to start from, for example the benchmark of a test being run again. */
	initial?: Partial<FeatureTestForm>;
}) {
	const { t } = useTranslation("w4p6");
	const { t: u } = useTranslation("uf7c");
	const navigate = useNavigate();
	const suites = useGetSuitesLightQuery(undefined, { skip: !open });
	const agents = useGetAgentsQuery(undefined, { skip: !open });
	const [draft] = useDraftGatewayTestMutation();
	const [plan] = usePlanExperimentMutation();
	const [create] = useCreateExperimentMutation();
	const [start] = useStartExperimentMutation();
	const [form, setForm] = useState<FeatureTestForm>(() => ({ ...EMPTY, ...initial }));
	const [touched, setTouched] = useState<ReadonlySet<string>>(new Set());
	const [prepared, setPrepared] = useState<Prepared | null>(null);
	const [estimating, setEstimating] = useState(false);
	const [error, setError] = useState<unknown>(null);
	const [showTests, setShowTests] = useShowTests();
	const suiteList = pickableSuites(suites.data?.suites ?? [], showTests);
	const agentList = pickableAgents(agents.data?.agents ?? [], showTests);
	const calls: FeatureTestCalls = {
		draft: (req) => draft(req).unwrap(),
		plan: (exp) => plan(exp).unwrap(),
		create: (exp) => create(exp).unwrap(),
		start: (id) => start(id).unwrap(),
	};
	const set = (patch: Partial<FeatureTestForm>, field: string) => {
		setForm((f) => ({ ...f, ...patch }));
		setTouched((s) => new Set(s).add(field));
	};
	const setOption = (key: string, value: string) => set({ options: { ...form.options, [key]: value } }, key);
	const built = draftRequest(feature, form);
	const missing = new Set(missingFields(feature, form));
	const reqKey = built.req ? JSON.stringify(built.req) : "";
	// Draft and plan save nothing and spend nothing: they run on their own once the form is complete.
	useEffect(() => {
		setPrepared(null);
		setError(null);
		if (!open || !reqKey) return;
		let live = true;
		setEstimating(true);
		const timer = setTimeout(() => {
			prepareFeatureTest(calls, JSON.parse(reqKey) as GwDraftRequest)
				.then((p) => live && setPrepared(p))
				.catch((e) => live && setError(e))
				.finally(() => live && setEstimating(false));
		}, ESTIMATE_DELAY_MS);
		return () => {
			live = false;
			clearTimeout(timer);
		};
		// calls is rebuilt each render; the request text is what matters.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [reqKey, open]);
	// PaidButton asks first (with the estimate and the cap); only then is the comparison created and started.
	const onStart = async () => {
		if (!prepared) return;
		setError(null);
		try {
			const id = await startFeatureTest(calls, prepared.draft.experiment);
			onClose();
			void navigate({ to: "/experiments/$expId", params: { expId: id } });
		} catch (e) {
			setError(e);
		}
	};
	const est = prepared ? planEstimate(prepared.plan) : null;
	const budgetUsd = Number(form.budget);
	const needed = (key: string, text?: string) =>
		touched.has(key) && missing.has(key) ? (
			<span className="text-destructive text-xs" data-testid={`feature-test-needed-${key}`}>
				{text ?? u("dialog.fieldNeeded")}
			</span>
		) : null;
	const opt = (key: string, part: "label" | "hint" | "placeholder") => String(u(`dialog.opt.${key}.${part}` as "dialog.opt.grid.label"));
	const modelRequired = NEEDS_MODEL.has(feature);
	const budgets = budgetChoices(form.options.budgets);
	return (
		<Dialog
			open={open}
			onOpenChange={(next) => !next && onClose()}
			title={t("dialog.title", { name })}
			closeLabel={u("dialog.close")}
			testId="feature-test-dialog"
			size="lg"
			footer={
				<div className="flex flex-wrap items-start justify-end gap-2">
					<Button type="button" variant="outline" onClick={onClose}>
						{t("dialog.cancel")}
					</Button>
					<PaidButton
						verb={u("dialog.startTest")}
						capUsd={Number.isFinite(budgetUsd) && budgetUsd > 0 ? budgetUsd : null}
						estimateUsd={est?.usd ?? null}
						confirm="auto"
						kind="form"
						disabled={estimating || !built.req || !prepared || prepared.plan.errors.length > 0}
						onConfirm={onStart}
						testId="feature-test-start"
					/>
				</div>
			}
		>
			<div className="flex flex-col gap-3 text-sm">
				<ShowTestsChip count={suiteList.hidden + agentList.hidden} on={showTests} onChange={setShowTests} testId="feature-test-show-tests" />
				<label className="flex flex-col gap-1">
					<span>
						{t("dialog.benchmark")}
						<span className="text-muted-foreground ml-2 text-xs">{t("dialog.required")}</span>
					</span>
					<Select value={form.suite || undefined} onValueChange={(v) => set({ suite: v }, "suite")}>
						<SelectTrigger className="w-full" aria-required data-testid="feature-test-suite">
							<SelectValue placeholder={t("dialog.pick")} />
						</SelectTrigger>
						<SelectContent>
							{suiteList.shown.map((s) => (
								<SelectItem key={s.id} value={suiteOption(s)}>
									{suiteLabel((k, o) => String(u(k, o)), s)}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
					{needed("suite")}
				</label>
				<label className="flex flex-col gap-1">
					<span>
						{t("dialog.agent")}
						<span className="text-muted-foreground ml-2 text-xs">{t("dialog.required")}</span>
					</span>
					<Select value={form.base || undefined} onValueChange={(v) => set({ base: v }, "base")}>
						<SelectTrigger className="w-full" aria-required data-testid="feature-test-agent">
							<SelectValue placeholder={t("dialog.pick")} />
						</SelectTrigger>
						<SelectContent>
							{agentList.shown.map((a) => (
								<SelectItem key={a.id} value={a.path}>
									{agentLabel((k, o) => String(u(k, o)), a)}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
					{needed("base")}
				</label>
				<div className="flex flex-col gap-1" data-testid="feature-test-model">
					<span>
						{modelRequired ? u("dialog.modelRequired") : t("dialog.model")}
						{modelRequired && <span className="text-muted-foreground ml-2 text-xs">{t("dialog.required")}</span>}
					</span>
					<ModelPicker
						value={form.model ? { provider: form.provider || "openai", model: form.model } : null}
						onChange={(v) => set({ model: v.model, provider: v.provider }, "model")}
						purpose="agent"
						testId="feature-test-model-picker"
					/>
					<span className="text-muted-foreground text-xs">{modelRequired ? u("dialog.modelNeeded") : u("dialog.modelHint")}</span>
					{needed("model", u("dialog.needModel"))}
				</div>
				{(FEATURE_OPTIONS[feature] ?? []).length > 0 && (
					<fieldset className="flex flex-col gap-3" data-testid="feature-test-options">
						<legend className="mb-1 font-medium">{t("dialog.options")}</legend>
						{(FEATURE_OPTIONS[feature] ?? []).map((o) => (
							<div key={o.key} className="flex flex-col gap-1" data-testid={`feature-test-opt-${o.key}`}>
								<label htmlFor={`feature-test-opt-${o.key}-input`}>
									{opt(o.key, "label")}
									{o.required && <span className="text-muted-foreground ml-2 text-xs">{t("dialog.required")}</span>}
								</label>
								{o.kind === "model" ? (
									<ModelPicker
										value={form.options[o.key] ? { provider: "openai", model: form.options[o.key] } : null}
										onChange={(v) => setOption(o.key, v.model)}
										purpose="agent"
										testId={`feature-test-opt-${o.key}-input`}
									/>
								) : o.kind === "yesno" ? (
									<Select value={form.options[o.key] || "yes"} onValueChange={(v) => setOption(o.key, v)}>
										<SelectTrigger id={`feature-test-opt-${o.key}-input`} className="w-full">
											<SelectValue />
										</SelectTrigger>
										<SelectContent>
											<SelectItem value="yes">{u("dialog.yes")}</SelectItem>
											<SelectItem value="no">{u("dialog.no")}</SelectItem>
										</SelectContent>
									</Select>
								) : o.kind === "budgetPick" ? (
									<Select value={form.options[o.key] || undefined} onValueChange={(v) => setOption(o.key, v)}>
										<SelectTrigger id={`feature-test-opt-${o.key}-input`} className="w-full">
											<SelectValue placeholder={t("dialog.pick")} />
										</SelectTrigger>
										<SelectContent>
											{budgets.map((b) => (
												<SelectItem key={b} value={b}>
													{`$${b}`}
												</SelectItem>
											))}
										</SelectContent>
									</Select>
								) : (
									<Input
										id={`feature-test-opt-${o.key}-input`}
										inputMode={o.kind === "number" ? "decimal" : undefined}
										placeholder={opt(o.key, "placeholder")}
										aria-required={o.required}
										value={form.options[o.key] ?? ""}
										onChange={(e) => setOption(o.key, e.target.value)}
									/>
								)}
								<span className="text-muted-foreground text-xs">{opt(o.key, "hint")}</span>
								{needed(o.key)}
							</div>
						))}
					</fieldset>
				)}
				<div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
					<label className="flex flex-col gap-1">
						{t("dialog.budget")}
						<Input
							inputMode="decimal"
							value={form.budget}
							aria-required
							onChange={(e) => set({ budget: e.target.value }, "budget")}
							data-testid="feature-test-budget"
						/>
						<span className="text-muted-foreground text-xs">{t("dialog.budgetHint")}</span>
						{needed("budget", t("dialog.needBudget"))}
					</label>
					<label className="flex flex-col gap-1">
						{t("dialog.trials")}
						<Input
							inputMode="numeric"
							value={form.trials}
							onChange={(e) => set({ trials: e.target.value }, "trials")}
							data-testid="feature-test-trials"
						/>
						<span className="text-muted-foreground text-xs">{u("dialog.trialsHint")}</span>
						{needed("trials", u("dialog.trialsMin"))}
					</label>
				</div>
				{estimating && (
					<p className="text-muted-foreground" role="status" data-testid="feature-test-estimating">
						{u("dialog.estimating")}
					</p>
				)}
				{prepared && <PreparedView prepared={prepared} budget={budgetUsd} confirming={false} />}
				<InlineError error={error} testId="feature-test-error" />
			</div>
		</Dialog>
	);
}
