import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import "@/lib/types/evaluateI18n";
import "@/lib/types/hubsI18n";
import { QuickRunDialog } from "@/components/run/quickRunDialog";
import { cn } from "@/lib/utils";
import { ArrowLeft, ArrowRight, Check } from "lucide-react";
import { failoverProblem } from "@/components/evaluations/failoverSettings";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { useGetAgentVersionsQuery } from "@/lib/store/apis/evaluationsApi";
import { parseAsInteger, parseAsString, useQueryState, useQueryStates } from "nuqs";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	agentPickPatch,
	draftFromLink,
	draftToSpec,
	EvaluateDraftProvider,
	learnStepReady,
	specToPatch,
	useEvaluateDraft,
	type EvaluateDraft,
} from "./evaluateDraft";
import { asEvaluateStep, EVALUATE_STEPS, latestVersion, type AgentWithCaps, type EvaluateStep } from "./evaluate.utils";
import "./gw09I18n";
import { Step1Agent } from "./step1Agent";
import { Step2Learn } from "./step2Learn";
import { Step3Benchmarks } from "./step3Benchmarks";
import { armHasModel, useOwnModelAgents } from "./ownModel";
import { nothingRunsReason, specReady, Step4Setup, useStep4Estimate } from "./step4Setup";
import "./fw26I18n";
import { Step5Launch } from "./step5Launch";

/** What the step checks know beyond the draft (fix wave 2, lane G09). */
export interface StepContext {
	/** Ids of the organization's agents once GET /agents has loaded; unset while it loads. */
	known?: ReadonlySet<string> | null;
	/** An agent a link named that does not exist: step 1 stays blocked until an agent is picked (N86). */
	missingAgent?: string | null;
	/** The agent's highest saved version, what "Latest" runs (N95). */
	latest?: number;
}

/** Why step 4's failover option cannot run as set (N89); "" when it can or is off. It only runs with one setup. */
export function failoverNextProblem(d: EvaluateDraft): string {
	return d.arms.length === 1 ? failoverProblem(d.arms[0], d.failover ?? undefined) : "";
}

/**
 * Whether Next is allowed from a step. Steps 3 and 4 use simple checks their lanes may refine.
 * `nothingRuns` is step 4's estimate saying no benchmark can run (its reason): Next stays off.
 */
export function stepReady(
	step: EvaluateStep,
	d: EvaluateDraft,
	owns: ReadonlySet<string> = new Set(),
	nothingRuns?: string | null,
	ctx: StepContext = {},
): boolean {
	switch (step) {
		case 1:
			if (ctx.missingAgent) return false;
			return Boolean(d.agent) && (!ctx.known || ctx.known.has(d.agent ?? ""));
		case 2:
			return learnStepReady(d, ctx.latest);
		case 3:
			return d.benchmarks.length > 0 || Boolean(d.pack);
		case 4:
			if (nothingRuns || failoverNextProblem(d)) return false;
			return d.arms.length > 0 && d.arms.every((a) => Boolean(a.agent) && armHasModel(a, owns));
		default:
			return false;
	}
}

/** Highest step reachable from the draft: each earlier step must be ready. */
export function maxReachable(
	d: EvaluateDraft,
	owns: ReadonlySet<string> = new Set(),
	nothingRuns?: string | null,
	ctx: StepContext = {},
): EvaluateStep {
	let s: EvaluateStep = 1;
	while (s < 5 && stepReady(s, d, owns, nothingRuns, ctx)) s = (s + 1) as EvaluateStep;
	return s;
}

/** The link parameters /evaluate reads: ?agent=, ?mode=, ?suite=, ?opponent= and ?keep=1 (back from Register your agent). */
export interface LinkParams {
	agent: string | null;
	mode: string | null;
	suite: string | null;
	opponent: string | null;
	keep: string | null;
}

/** What the wizard does with a link (N154, N112, N86; decision D10). */
export type LinkPlan =
	| { kind: "none" }
	| { kind: "wait" }
	| { kind: "missing"; id: string }
	| { kind: "patch"; patch: Partial<EvaluateDraft> }
	| { kind: "replace"; draft: EvaluateDraft; putAside: EvaluateDraft | null; badMode?: string; step: EvaluateStep | null };

/**
 * The plan for the link params over the stored draft. A named agent waits for the agents list; an unknown one leaves
 * the draft alone. keep=1 patches only the agent (as a click would). Any other link starts a new draft and puts a
 * stored draft that has an agent or benchmarks aside. `step` is where to go when the link has no ?step= of its own:
 * 2 for an agent and a mode, 3 for an agent and a benchmark.
 */
export function planLink(
	p: LinkParams,
	draft: EvaluateDraft,
	agents: readonly AgentWithCaps[] | undefined,
	stepRaw: number | null,
): LinkPlan {
	if (!p.agent && !p.mode && !p.suite && !p.opponent && !p.keep) return { kind: "none" };
	let row: AgentWithCaps | undefined;
	if (p.agent) {
		if (!agents) return { kind: "wait" };
		row = agents.find((a) => a.id === p.agent);
		if (!row) return { kind: "missing", id: p.agent };
	}
	if (p.keep === "1") {
		const patch: Partial<EvaluateDraft> = row && row.id !== draft.agent ? agentPickPatch(draft, row) : {};
		if (p.suite && !draft.benchmarks.includes(p.suite)) Object.assign(patch, { benchmarks: [...draft.benchmarks, p.suite], pack: null });
		return { kind: "patch", patch };
	}
	const { draft: next, badMode } = draftFromLink(p, row);
	const putAside = draft.agent || draft.benchmarks.length > 0 || draft.pack ? draft : null;
	const step: EvaluateStep | null = stepRaw != null ? null : p.agent && p.mode ? 2 : p.agent && p.suite ? 3 : null;
	return { kind: "replace", draft: next, putAside, ...(badMode ? { badMode } : {}), step };
}

/**
 * The value to write to ?step= so the URL names the step shown (N85): undefined when nothing is to be written (not
 * settled yet, no step in the URL, or it already matches), null for step 1.
 */
export function stepWriteBack(stepRaw: number | null, step: EvaluateStep, settled: boolean): number | null | undefined {
	if (!settled || stepRaw == null) return undefined;
	const want = step === 1 ? null : step;
	return stepRaw === want ? undefined : want;
}

/** The note after a link started a new draft: the earlier draft was put aside, and an unknown ?mode= if any. */
export function LinkNotice({ putAside, badMode, onBack }: { putAside: boolean; badMode?: string | null; onBack(): void }) {
	const { t } = useTranslation("gw09");
	return (
		<div
			role="status"
			className="border-chart-info/40 bg-chart-info/10 flex flex-wrap items-center gap-3 rounded-lg border px-3 py-2 text-sm"
			data-testid="evaluate-link-notice"
		>
			<span className="flex flex-col gap-0.5">
				{putAside && <span>{t("link.putAside")}</span>}
				{badMode && <span data-testid="evaluate-link-bad-mode">{t("link.badMode", { mode: badMode })}</span>}
			</span>
			{putAside && (
				<Button variant="outline" size="sm" onClick={onBack} data-testid="evaluate-link-back">
					{t("link.goBack")}
				</Button>
			)}
		</div>
	);
}

const LINK_PARSERS = {
	agent: parseAsString,
	mode: parseAsString,
	suite: parseAsString,
	opponent: parseAsString,
	keep: parseAsString,
};

function Stepper({ step, reach, onGo }: { step: EvaluateStep; reach: EvaluateStep; onGo(s: EvaluateStep): void }) {
	const { t } = useTranslation("evaluate");
	return (
		<ol className="flex flex-wrap items-center gap-2" data-testid="evaluate-stepper">
			{EVALUATE_STEPS.map((s) => {
				const done = s < step;
				const current = s === step;
				const allowed = s <= reach;
				return (
					<li key={s} className="flex items-center gap-2">
						<button
							type="button"
							disabled={!allowed}
							onClick={() => onGo(s)}
							aria-current={current ? "step" : undefined}
							data-testid={`evaluate-stepper-${s}`}
							className={cn(
								"flex items-center gap-2 rounded-full border px-3 py-1 text-sm transition-colors disabled:cursor-not-allowed disabled:opacity-50",
								current ? "border-primary bg-primary/10 text-primary font-medium" : "border-border/70 hover:bg-accent",
							)}
						>
							<span
								className={cn(
									"flex size-5 items-center justify-center rounded-full text-[11px] font-semibold",
									current ? "bg-primary text-primary-foreground" : done ? "bg-chart-success/20 text-chart-success-ink" : "bg-muted",
								)}
							>
								{done ? <Check className="size-3" /> : s}
							</span>
							{t(`steps.${s}`)}
						</button>
						{s < 5 && <span className="bg-border hidden h-px w-4 sm:block" aria-hidden />}
					</li>
				);
			})}
		</ol>
	);
}

function EvaluateWizard() {
	const { t } = useTranslation("evaluate");
	const { t: tHubs } = useTranslation("hubs");
	const { t: tFw } = useTranslation("fw26");
	const { draft, update, replace, reset } = useEvaluateDraft();
	const [stepRaw, setStepRaw] = useQueryState("step", parseAsInteger);
	const [link, setLink] = useQueryStates(LINK_PARSERS);
	const [quickRun, setQuickRun] = useState(false);
	// The draft a link put aside (page state only: leaving the page drops it), an unknown ?mode= and an unknown ?agent=.
	const [replaced, setReplaced] = useState<EvaluateDraft | null>(null);
	const [badMode, setBadMode] = useState<string | null>(null);
	const [missingAgent, setMissingAgent] = useState<string | null>(null);
	const agentsQ = useGetAgentsQuery();
	const agentList = (agentsQ.data as { agents?: AgentWithCaps[] } | undefined)?.agents;
	const known = useMemo(() => (agentList ? new Set(agentList.map((a) => a.id)) : null), [agentList]);
	const linkPending = Boolean(link.agent || link.mode || link.suite || link.opponent || link.keep);

	// One effect for every link into the wizard (agent menu, a benchmark's Run button, Test my MCP server, and the
	// return from Register your agent with keep=1). It runs once per set of link params.
	const linkDone = useRef<string | null>(null);
	useEffect(() => {
		const plan = planLink(link, draft, agentList, stepRaw);
		if (plan.kind === "none") {
			linkDone.current = null;
			return;
		}
		if (plan.kind === "wait") return;
		const sig = JSON.stringify(link);
		if (linkDone.current === sig) return;
		linkDone.current = sig;
		if (plan.kind === "missing") {
			setMissingAgent(plan.id);
		} else if (plan.kind === "patch") {
			setMissingAgent(null);
			if (Object.keys(plan.patch).length) update(plan.patch);
		} else {
			setMissingAgent(null);
			setReplaced(plan.putAside);
			setBadMode(plan.badMode ?? null);
			replace(plan.draft);
			if (plan.step) void setStepRaw(plan.step, { history: "replace" });
		}
		void setLink({ agent: null, mode: null, suite: null, opponent: null, keep: null }, { history: "replace" });
	}, [link, draft, agentList, stepRaw, update, replace, setLink, setStepRaw]);

	const owns = useOwnModelAgents();
	const versionsQ = useGetAgentVersionsQuery(draft.agent ?? "", { skip: !draft.agent });
	const latest = latestVersion(versionsQ.data?.versions);
	const ctx: StepContext = { known, missingAgent, latest };
	const wanted = asEvaluateStep(stepRaw);
	// Step 4's estimate (the same cached request the step makes): when it plans no runs, Next stays off.
	const spec = draftToSpec(draft);
	const estimateSkipped = wanted < 4 || !specReady(spec, owns);
	const step4Estimate = useStep4Estimate(spec, estimateSkipped);
	const nothingRuns = wanted >= 4 && !step4Estimate.pending ? nothingRunsReason(step4Estimate.main.data) : null;
	const reach = maxReachable(draft, owns, nothingRuns, ctx);
	const step = (wanted > reach ? reach : wanted) as EvaluateStep;
	const go = (s: EvaluateStep) => void setStepRaw(s === 1 ? null : s);
	const failoverHint = step === 4 && !nothingRuns ? failoverNextProblem(draft) : "";

	// N85: once the link params are applied and the agents list (and, from step 4 on, the estimate) has settled, the
	// URL names the step shown, so the wizard never moves ahead by itself towards a stale ?step=.
	const estimateSettled = wanted < 4 || estimateSkipped || (!step4Estimate.pending && !step4Estimate.main.isFetching);
	const settled = !linkPending && !agentsQ.isLoading && estimateSettled;
	const writeBack = stepWriteBack(stepRaw, step, settled);
	useEffect(() => {
		if (writeBack !== undefined) void setStepRaw(writeBack, { history: "replace" });
	}, [writeBack, setStepRaw]);

	return (
		<section className="mx-auto flex w-full max-w-[1100px] min-w-0 flex-col gap-6" data-testid="evaluate-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				actions={
					<div className="flex items-center gap-2">
						<Button
							variant="ghost"
							size="sm"
							onClick={() => (reset(), setReplaced(null), setBadMode(null), setMissingAgent(null), go(1))}
							data-testid="evaluate-reset"
						>
							{t("startOver")}
						</Button>
						<Button
							variant="link"
							size="sm"
							onClick={() => setQuickRun(true)}
							title={tHubs("quickRun.hint")}
							data-testid="evaluate-quick-run"
						>
							{tHubs("quickRun.link")}
						</Button>
					</div>
				}
			/>
			{quickRun && <QuickRunDialog onClose={() => setQuickRun(false)} />}
			{(replaced || badMode) && (
				<LinkNotice
					putAside={Boolean(replaced)}
					badMode={badMode}
					onBack={() => {
						if (replaced) replace(replaced);
						setReplaced(null);
						setBadMode(null);
						go(1);
					}}
				/>
			)}
			<Stepper step={step} reach={reach} onGo={go} />
			<p className="text-muted-foreground text-xs">{t("stepOf", { n: step })}</p>

			<div className="min-w-0">
				{step === 1 && <Step1Agent missingAgent={missingAgent} onPicked={() => setMissingAgent(null)} />}
				{step === 2 && <Step2Learn />}
				{step === 3 && <Step3Benchmarks />}
				{step === 4 && <Step4Setup spec={spec} onChange={(s) => update(specToPatch(s, draft))} />}
				{step === 5 && <Step5Launch spec={draftToSpec(draft)} onChange={(s) => update(specToPatch(s))} />}
			</div>

			<div className="flex items-center justify-between border-t pt-4">
				<Button variant="outline" disabled={step === 1} onClick={() => go((step - 1) as EvaluateStep)} data-testid="evaluate-back">
					<ArrowLeft />
					{t("back")}
				</Button>
				{step < 5 && (
					<div className="flex items-center gap-3">
						{step === 4 && nothingRuns && (
							<span className="text-muted-foreground max-w-xl text-right text-xs" data-testid="evaluate-next-hint">
								{tFw("step4.nothingRunsHint", { reason: nothingRuns })}
							</span>
						)}
						{failoverHint && (
							<span className="text-muted-foreground max-w-xl text-right text-xs" data-testid="evaluate-next-hint">
								{failoverHint}
							</span>
						)}
						<Button
							disabled={!stepReady(step, draft, owns, nothingRuns, ctx)}
							onClick={() => go((step + 1) as EvaluateStep)}
							data-testid="evaluate-next"
						>
							{t("next")}
							<ArrowRight />
						</Button>
					</div>
				)}
			</div>
		</section>
	);
}

/** /evaluate: the agent-first wizard. Agent, what to learn, benchmarks, setup, launch. */
export default function EvaluateView({ initial }: { initial?: Partial<EvaluateDraft> }) {
	return (
		<EvaluateDraftProvider initial={initial}>
			<EvaluateWizard />
		</EvaluateDraftProvider>
	);
}