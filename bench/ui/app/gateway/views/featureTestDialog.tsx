import { GovDialog } from "@/components/govern/govDialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
	useCreateExperimentMutation,
	useGetAgentsQuery,
	useGetSuitesQuery,
	usePlanExperimentMutation,
	useStartExperimentMutation,
} from "@/lib/store/apis/benchApi";
import { useDraftGatewayTestMutation } from "@/lib/store/apis/gatewayApi";
import { errorMessage } from "@/lib/store/apis/schedulesApi";
import type { Experiment, ExperimentPlan } from "@/lib/types/bench";
import type { GwDraftRequest, GwDraftResponse } from "@/lib/types/gateway";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import "./w4p6I18n";

/** One setting of a feature's own Draft (audit/W4_DESIGN_3_BIFROST.md sections 4 to 13). */
export interface FeatureOption {
	key: string;
	required: boolean;
}

/** The Draft options each feature reads, required first. */
export const FEATURE_OPTIONS: Record<string, FeatureOption[]> = {
	complexity_router: [
		{ key: "strong_model", required: true },
		{ key: "cheap_model", required: true },
		{ key: "tolerance", required: false },
		{ key: "router_when", required: false },
	],
	prompt_version: [
		{ key: "prompt_id", required: true },
		{ key: "variant_version", required: true },
		{ key: "control_version", required: false },
	],
	mcp_tools: [
		{ key: "tools_b", required: true },
		{ key: "tools_c", required: false },
		{ key: "tools_a", required: false },
		{ key: "forbidden", required: false },
		{ key: "poison_probe", required: false },
	],
	semantic_cache: [
		{ key: "grid", required: false },
		{ key: "target", required: false },
		{ key: "ttl", required: false },
	],
	skill_version: [
		{ key: "control_path", required: true },
		{ key: "variant_path", required: true },
	],
	providers: [{ key: "targets", required: true }],
	budget_curve: [
		{ key: "budgets", required: true },
		{ key: "current", required: false },
		{ key: "tell_agent", required: false },
	],
	gateway_overhead: [],
};

export const MAX_DRAFT_USD = 50;

export interface FeatureTestForm {
	suite: string;
	base: string;
	model: string;
	provider: string;
	budget: string;
	trials: string;
	options: Record<string, string>;
}

/** The draft request, or the problem that stops it. Pure. */
export function draftRequest(feature: string, f: FeatureTestForm): { req?: GwDraftRequest; problem?: "needBudget" | "needFields" } {
	const budget = Number(f.budget);
	if (!Number.isFinite(budget) || budget <= 0 || budget > MAX_DRAFT_USD) return { problem: "needBudget" };
	const opts = FEATURE_OPTIONS[feature] ?? [];
	if (!f.suite || !f.base || opts.some((o) => o.required && !f.options[o.key]?.trim())) return { problem: "needFields" };
	const options: Record<string, string> = {};
	for (const o of opts) {
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

/** The dialog's content after Prepare: notes, estimate, plan refusals and the confirmation. Pure. */
export function PreparedView({ prepared, budget, confirming }: { prepared: Prepared; budget: number; confirming: boolean }) {
	const { t } = useTranslation("w4p6");
	const { notes } = prepared.draft;
	const { plan } = prepared;
	const known =
		plan.est_usd?.state === "observed" && plan.est_usd.v !== null && plan.est_wall_s?.state === "observed" && plan.est_wall_s.v !== null;
	return (
		<div className="flex flex-col gap-3 text-sm" data-testid="feature-test-prepared">
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
			<p data-testid="feature-test-estimate">
				{known
					? t("dialog.estimate", {
							usd: (plan.est_usd.v as number).toFixed(2),
							minutes: Math.max(1, Math.round((plan.est_wall_s.v as number) / 60)),
						})
					: t("dialog.estimateUnknown")}
			</p>
			{plan.errors.length > 0 && (
				<div className="text-red-700 dark:text-red-400" data-testid="feature-test-plan-errors">
					<p>{t("dialog.planErrors")}</p>
					<ul className="list-disc pl-5">
						{plan.errors.map((e) => (
							<li key={e}>{e}</li>
						))}
					</ul>
				</div>
			)}
			{confirming && <p data-testid="feature-test-confirm">{t("dialog.confirm", { usd: budget.toFixed(2) })}</p>}
		</div>
	);
}

const EMPTY: FeatureTestForm = { suite: "", base: "", model: "", provider: "", budget: "", trials: "2", options: {} };

/** "Start a test": pick what to run, Prepare (draft and plan), then Start (create and start) with a confirmation. */
export function FeatureTestDialog({ feature, name, open, onClose }: { feature: string; name: string; open: boolean; onClose(): void }) {
	const { t } = useTranslation("w4p6");
	const navigate = useNavigate();
	const suites = useGetSuitesQuery(undefined, { skip: !open });
	const agents = useGetAgentsQuery(undefined, { skip: !open });
	const [draft] = useDraftGatewayTestMutation();
	const [plan] = usePlanExperimentMutation();
	const [create] = useCreateExperimentMutation();
	const [start] = useStartExperimentMutation();
	const [form, setForm] = useState<FeatureTestForm>(EMPTY);
	const [prepared, setPrepared] = useState<Prepared | null>(null);
	const [confirming, setConfirming] = useState(false);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	const calls: FeatureTestCalls = {
		draft: (req) => draft(req).unwrap(),
		plan: (exp) => plan(exp).unwrap(),
		create: (exp) => create(exp).unwrap(),
		start: (id) => start(id).unwrap(),
	};
	const set = (patch: Partial<FeatureTestForm>) => {
		setForm((f) => ({ ...f, ...patch }));
		setPrepared(null);
		setConfirming(false);
	};
	const built = draftRequest(feature, form);
	const onPrepare = async () => {
		if (!built.req) return setError(t(`dialog.${built.problem ?? "needFields"}`));
		setBusy(true);
		setError("");
		try {
			setPrepared(await prepareFeatureTest(calls, built.req));
		} catch (e) {
			setError(t("dialog.error", { error: errorMessage(e, String(e)) }));
		} finally {
			setBusy(false);
		}
	};
	const onStart = async () => {
		if (!prepared) return;
		if (!confirming) return setConfirming(true);
		setBusy(true);
		try {
			const id = await startFeatureTest(calls, prepared.draft.experiment);
			onClose();
			void navigate({ to: "/experiments/$expId", params: { expId: id } });
		} catch (e) {
			setError(t("dialog.error", { error: errorMessage(e, String(e)) }));
		} finally {
			setBusy(false);
		}
	};
	const field = "border-input bg-background h-9 w-full rounded-md border px-2 text-sm";
	return (
		<GovDialog
			open={open}
			onClose={onClose}
			title={t("dialog.title", { name })}
			closeLabel={t("dialog.cancel")}
			testId="feature-test-dialog"
			className="max-w-2xl"
		>
			<div className="flex flex-col gap-3 text-sm">
				<label className="flex flex-col gap-1">
					{t("dialog.benchmark")}
					<select className={field} value={form.suite} onChange={(e) => set({ suite: e.target.value })} data-testid="feature-test-suite">
						<option value="">{t("dialog.pick")}</option>
						{(suites.data?.suites ?? []).map((s) => (
							<option key={s.id} value={s.id}>
								{s.id}
							</option>
						))}
					</select>
				</label>
				<label className="flex flex-col gap-1">
					{t("dialog.agent")}
					<select className={field} value={form.base} onChange={(e) => set({ base: e.target.value })} data-testid="feature-test-agent">
						<option value="">{t("dialog.pick")}</option>
						{(agents.data?.agents ?? []).map((a) => (
							<option key={a.id} value={a.id}>
								{a.id}
							</option>
						))}
					</select>
				</label>
				<div className="grid grid-cols-2 gap-2">
					<label className="flex flex-col gap-1">
						{t("dialog.model")}
						<Input value={form.model} onChange={(e) => set({ model: e.target.value })} data-testid="feature-test-model" />
					</label>
					<label className="flex flex-col gap-1">
						{t("dialog.provider")}
						<Input value={form.provider} onChange={(e) => set({ provider: e.target.value })} data-testid="feature-test-provider" />
					</label>
				</div>
				{(FEATURE_OPTIONS[feature] ?? []).length > 0 && (
					<fieldset className="flex flex-col gap-2" data-testid="feature-test-options">
						<legend className="font-medium">{t("dialog.options")}</legend>
						{(FEATURE_OPTIONS[feature] ?? []).map((o) => (
							<label key={o.key} className="flex flex-col gap-1">
								<span>
									<code>{o.key}</code>
									{o.required && <span className="text-muted-foreground ml-2 text-xs">{t("dialog.required")}</span>}
								</span>
								<Input value={form.options[o.key] ?? ""} onChange={(e) => set({ options: { ...form.options, [o.key]: e.target.value } })} />
							</label>
						))}
					</fieldset>
				)}
				<div className="grid grid-cols-2 gap-2">
					<label className="flex flex-col gap-1">
						{t("dialog.budget")}
						<Input
							inputMode="decimal"
							value={form.budget}
							onChange={(e) => set({ budget: e.target.value })}
							data-testid="feature-test-budget"
						/>
						<span className="text-muted-foreground text-xs">{t("dialog.budgetHint")}</span>
					</label>
					<label className="flex flex-col gap-1">
						{t("dialog.trials")}
						<Input
							inputMode="numeric"
							value={form.trials}
							onChange={(e) => set({ trials: e.target.value })}
							data-testid="feature-test-trials"
						/>
					</label>
				</div>
				{prepared && <PreparedView prepared={prepared} budget={Number(form.budget)} confirming={confirming} />}
				{error && (
					<p className="text-red-700 dark:text-red-400" data-testid="feature-test-error">
						{error}
					</p>
				)}
				<div className="flex justify-end gap-2">
					<Button type="button" variant="outline" onClick={onClose}>
						{t("dialog.cancel")}
					</Button>
					{!prepared ? (
						<Button type="button" disabled={busy} onClick={() => void onPrepare()} data-testid="feature-test-prepare">
							{busy ? t("dialog.preparing") : t("dialog.prepare")}
						</Button>
					) : (
						<Button
							type="button"
							disabled={busy || prepared.plan.errors.length > 0}
							onClick={() => void onStart()}
							data-testid="feature-test-start"
						>
							{busy ? t("dialog.starting") : confirming ? t("dialog.confirmYes") : t("dialog.start")}
						</Button>
					)}
				</div>
			</div>
		</GovDialog>
	);
}