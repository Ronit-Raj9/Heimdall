import { StickyFooter } from "@/components/ui/stickyFooter";
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
import { asEvaluateStep, EVALUATE_STEPS, isRoughEstimate, latestVersion, type AgentWithCaps, type EvaluateStep } from "./evaluate.utils";
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

/** The evaluate locale key that says why Next is off on a step (M3-26); null when the step is ready. */
export type NextReason = "needAgent" | "needVersion" | "needOpponent" | "needLearn" | "needBenchmark" | "needModel";

export function nextReason(
	step: EvaluateStep,
	d: EvaluateDraft,
	owns: ReadonlySet<string> = new Set(),
	nothingRuns?: string | null,
	ctx: StepContext = {},
): NextReason | null {
	if (step >= 5 || stepReady(step, d, owns, nothingRuns, ctx)) return null;
	switch (step) {
		case 1:
			return "needAgent";
		case 2:
			return d.mode === "versions" ? "needVersion" : d.mode === "opponent" ? "needOpponent" : "needLearn";
		case 3:
			return "needBenchmark";
		default:
			// Step 4 has its own hints (nothing runs, failover); otherwise a setup lacks a model.
			return nothingRuns || failoverNextProblem(d) ? null : "needModel";
	}
}

/** "2 benchmarks, 20 runs, about $0.10 to $0.40, about 12 min" from the step 4 estimate, for the footer (M3-26). */
export function footerSummary(
	t: (key: string, opts?: Record<string, unknown>) => string,
	benchmarks: number,
	est:
		| {
				runs: number;
				est_cost_usd?: { low: number; high: number; known?: boolean };
				est_minutes?: { low: number; high: number };
				per_benchmark?: readonly object[] | null;
		  }
		| undefined,
): string {
	if (!est || !(est.runs > 0)) return "";
	const usd = (n: number) => `$${n < 1 ? n.toFixed(2) : n.toFixed(0)}`;
	if (!est.est_cost_usd?.known) return t("footerSummaryNoCost", { benchmarks, runs: est.runs });
	// A list price guess says so: "roughly ... (list prices)" (G1 estimate_basis, M3-28).
	return t(isRoughEstimate(est) ? "footerSummaryRough" : "footerSummary", {
		benchmarks,
		runs: est.runs,
		low: usd(est.est_cost_usd.low),
		high: usd(est.est_cost_usd.high),
		min: Math.max(1, Math.round(est.est_minutes?.high ?? 0)),
	});
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

/**
 * The link parameters /evaluate reads: ?agent=, ?mode=, ?suite=, ?opponent=, ?keep=1 (back from Register your agent),
 * and ?model= and ?task= (the run page's "Run this task again", which also sends ?step=4, M5-5). ?step= is read on its own.
 */
export interface LinkParams {
	agent: string | null;
	mode: string | null;
	suite: string | null;
	opponent: string | null;
	keep: string | null;
	model?: string | null;
	task?: string | null;
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
	if (!p.agent && !p.mode && !p.suite && !p.opponent && !p.keep && !p.model && !p.task) return { kind: "none" };
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
	const step: EvaluateStep | null =
		stepRaw != null ? null : p.agent && p.suite && p.task ? 4 : p.agent && p.mode ? 2 : p.agent && p.suite ? 3 : null;
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
	model: parseAsString,
	task: parseAsString,
};

function Stepper({ step, reach, onGo }: { step: EvaluateStep; reach: EvaluateStep; onGo(s: EvaluateStep): void }) {
	const { t } = useTranslation("evaluate");
	return (
		<ol className="hidden flex-wrap items-center gap-2 sm:flex" data-testid="evaluate-stepper">
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
								// Unreachable steps stay readable: muted text at full opacity, not grey on grey (M3-45).
								"disabled:text-muted-foreground flex items-center gap-2 rounded-full border px-3 py-1 text-sm transition-colors disabled:cursor-not-allowed",
								current ? "border-primary bg-primary/10 text-primary font-medium" : "border-border/70 hover:bg-accent",
							)}
						>
							{/* The circle is decoration; the step number is read as text (M4-95). */}
							<span className="sr-only">{t("stepOf", { n: s })}: </span>
							<span
								aria-hidden="true"
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
	const { t: tUf } = useTranslation("uf3");
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
	const linkPending = Boolean(link.agent || link.mode || link.suite || link.opponent || link.keep || link.model || link.task);
	// The task a "Run this task again" link named (M5-5): shown as a note while the draft still runs one task of its benchmark.
	const [linkTask, setLinkTask] = useState<{ task: string; suite: string } | null>(null);

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
			setLinkTask(link.task && link.suite ? { task: link.task, suite: link.suite } : null);
			replace(plan.draft);
			if (plan.step) void setStepRaw(plan.step, { history: stepHistoryMode("fix") });
		}
		void setLink({ agent: null, mode: null, suite: null, opponent: null, keep: null, model: null, task: null }, { history: "replace" });
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
	const go = (s: EvaluateStep) => void setStepRaw(s === 1 ? null : s, { history: stepHistoryMode("user") });
	// Resume (M3-44): opened without ?step= on a draft that has an agent, the wizard goes to the furthest step it can.
	const resumed = useRef(false);
	useEffect(() => {
		if (resumed.current || linkPending || agentsQ.isLoading) return;
		resumed.current = true;
		if (stepRaw == null && draft.agent && reach > 1) void setStepRaw(reach, { history: stepHistoryMode("fix") });
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [linkPending, agentsQ.isLoading]);
	const [launchSlot, setLaunchSlot] = useState<HTMLElement | null>(null);
	const reason = nextReason(step, draft, owns, nothingRuns, ctx);
	const summaryText = step >= 4 ? footerSummary(t as never, spec.benchmarks.length, step4Estimate.main.data) : "";
	const failoverHint = step === 4 && !nothingRuns ? failoverNextProblem(draft) : "";

	// N85: once the link params are applied and the agents list (and, from step 4 on, the estimate) has settled, the
	// URL names the step shown, so the wizard never moves ahead by itself towards a stale ?step=.
	const estimateSettled = wanted < 4 || estimateSkipped || (!step4Estimate.pending && !step4Estimate.main.isFetching);
	const settled = !linkPending && !agentsQ.isLoading && estimateSettled;
	const writeBack = stepWriteBack(stepRaw, step, settled);
	useEffect(() => {
		if (writeBack !== undefined) void setStepRaw(writeBack, { history: stepHistoryMode("fix") });
	}, [writeBack, setStepRaw]);

	return (
		<section className="mx-auto flex w-full max-w-[1100px] min-w-0 flex-col gap-6" data-testid="evaluate-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				breadcrumbs={[{ label: t("crumbList"), to: "/evaluations" }, { label: t("crumbNew") }]}
				actions={
					// PageHeader wraps its actions itself (M3-50): no local wrapper.
					<Button
						variant="ghost"
						size="sm"
						onClick={() => (reset(), setReplaced(null), setBadMode(null), setMissingAgent(null), setLinkTask(null), go(1))}
						data-testid="evaluate-reset"
					>
						{t("startOver")}
					</Button>
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
			{linkTask && draft.benchmarks.includes(linkTask.suite) && draft.tasksPerBenchmark === 1 && (
				<p role="status" className="text-muted-foreground text-sm" data-testid="evaluate-link-task">
					{tUf("link.task", { task: linkTask.task, suite: linkTask.suite })}
				</p>
			)}
			<div className="flex flex-col gap-2">
				<Stepper step={step} reach={reach} onGo={go} />
				{/* One progress indicator: the stepper on wider screens, "Step n of 5: name" with a bar on phones (M3-45). */}
				<div className="flex flex-col gap-1 sm:sr-only" data-testid="evaluate-step-of">
					<p className="text-sm font-medium">{t("stepOfName", { n: step, name: t(`steps.${step}`) })}</p>
					<div className="bg-muted h-1 w-full rounded-full" aria-hidden="true">
						<div className="bg-primary h-1 rounded-full" style={{ width: `${(step / 5) * 100}%` }} />
					</div>
				</div>
				<button
					type="button"
					onClick={() => setQuickRun(true)}
					title={tHubs("quickRun.hint")}
					className="text-muted-foreground hover:text-foreground w-fit text-xs underline-offset-2 hover:underline"
					data-testid="evaluate-quick-run"
				>
					{tHubs("quickRun.link")}
				</button>
			</div>

			<div className="min-w-0">
				{step === 1 && <Step1Agent missingAgent={missingAgent} onPicked={() => setMissingAgent(null)} />}
				{step === 2 && <Step2Learn />}
				{step === 3 && <Step3Benchmarks />}
				{step === 4 && <Step4Setup spec={spec} onChange={(s) => update(specToPatch(s, draft))} />}
				{step === 5 && <Step5Launch spec={draftToSpec(draft)} onChange={(s) => update(specToPatch(s))} launchSlot={launchSlot} />}
			</div>

			{/* One sticky footer on every step: Back, the reason or summary, and the primary button (M3-26, M1-50). */}
			<StickyFooter className="-mx-4 justify-between gap-3 sm:mx-0" testId="evaluate-footer">
				<Button variant="outline" disabled={step === 1} onClick={() => go((step - 1) as EvaluateStep)} data-testid="evaluate-back">
					<ArrowLeft />
					{t("back")}
				</Button>
				{(reason || summaryText) && (
					<span
						className="text-muted-foreground min-w-0 flex-1 text-xs sm:text-center"
						data-testid={reason ? "evaluate-next-reason" : "evaluate-footer-summary"}
					>
						{reason ? t(`nextReason.${reason}`) : summaryText}
					</span>
				)}
				{step === 5 && <span ref={setLaunchSlot} className="ml-auto flex items-center" data-testid="evaluate-launch-slot" />}
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
			</StickyFooter>
		</section>
	);
}

/** /evaluate: the agent-first wizard. Agent, what to learn, benchmarks, setup, launch. */
/**
 * How a step change is written to the URL: a step the person chose is pushed, so browser Back returns to the
 * previous step; a correction of the URL (link params, the step clamp) replaces, so Back never lands on a step
 * the page refuses.
 */
export function stepHistoryMode(reason: "user" | "fix"): "push" | "replace" {
	return reason === "user" ? "push" : "replace";
}

export default function EvaluateView({ initial }: { initial?: Partial<EvaluateDraft> }) {
	return (
		<EvaluateDraftProvider initial={initial}>
			<EvaluateWizard />
		</EvaluateDraftProvider>
	);
}