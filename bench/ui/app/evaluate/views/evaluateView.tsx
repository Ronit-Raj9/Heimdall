import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import "@/lib/types/evaluateI18n";
import "@/lib/types/hubsI18n";
import { QuickRunDialog } from "@/components/run/quickRunDialog";
import { cn } from "@/lib/utils";
import { ArrowLeft, ArrowRight, Check } from "lucide-react";
import { parseAsInteger, parseAsString, useQueryState } from "nuqs";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { draftToSpec, EvaluateDraftProvider, learnStepReady, specToPatch, useEvaluateDraft, type EvaluateDraft } from "./evaluateDraft";
import { asEvaluateStep, EVALUATE_STEPS, type EvaluateStep } from "./evaluate.utils";
import { Step1Agent } from "./step1Agent";
import { Step2Learn } from "./step2Learn";
import { Step3Benchmarks } from "./step3Benchmarks";
import { armHasModel, useOwnModelAgents } from "./ownModel";
import { nothingRunsReason, specReady, Step4Setup, useStep4Estimate } from "./step4Setup";
import "./fw26I18n";
import { Step5Launch } from "./step5Launch";

/**
 * Whether Next is allowed from a step. Steps 3 and 4 use simple checks their lanes may refine.
 * `nothingRuns` is step 4's estimate saying no benchmark can run (its reason): Next stays off.
 */
export function stepReady(
	step: EvaluateStep,
	d: EvaluateDraft,
	owns: ReadonlySet<string> = new Set(),
	nothingRuns?: string | null,
): boolean {
	switch (step) {
		case 1:
			return Boolean(d.agent);
		case 2:
			return learnStepReady(d);
		case 3:
			return d.benchmarks.length > 0 || Boolean(d.pack);
		case 4:
			if (nothingRuns) return false;
			return d.arms.length > 0 && d.arms.every((a) => Boolean(a.agent) && armHasModel(a, owns));
		default:
			return false;
	}
}

/** Highest step reachable from the draft: each earlier step must be ready. */
export function maxReachable(d: EvaluateDraft, owns: ReadonlySet<string> = new Set(), nothingRuns?: string | null): EvaluateStep {
	let s: EvaluateStep = 1;
	while (s < 5 && stepReady(s, d, owns, nothingRuns)) s = (s + 1) as EvaluateStep;
	return s;
}

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
	const { draft, update, reset } = useEvaluateDraft();
	const [stepRaw, setStepRaw] = useQueryState("step", parseAsInteger);
	const [agentParam, setAgentParam] = useQueryState("agent", parseAsString);
	const [suiteParam, setSuiteParam] = useQueryState("suite", parseAsString);
	const [quickRun, setQuickRun] = useState(false);

	// Back from /agents/new?return=/evaluate: the new agent arrives as ?agent=<id>.
	useEffect(() => {
		if (agentParam) {
			if (agentParam !== draft.agent) update({ agent: agentParam, version: null, compareVersion: null });
			void setAgentParam(null);
		}
	}, [agentParam, draft.agent, update, setAgentParam]);

	// From a benchmark's Run button: /evaluate?suite=<id> adds that benchmark to the picks.
	useEffect(() => {
		if (!suiteParam) return;
		if (!draft.benchmarks.includes(suiteParam)) update({ benchmarks: [...draft.benchmarks, suiteParam], pack: null });
		void setSuiteParam(null);
	}, [suiteParam, draft.benchmarks, update, setSuiteParam]);

	const owns = useOwnModelAgents();
	const wanted = asEvaluateStep(stepRaw);
	// Step 4's estimate (the same cached request the step makes): when it plans no runs, Next stays off.
	const spec = draftToSpec(draft);
	const step4Estimate = useStep4Estimate(spec, wanted < 4 || !specReady(spec, owns));
	const nothingRuns = wanted >= 4 && !step4Estimate.pending ? nothingRunsReason(step4Estimate.main.data) : null;
	const reach = maxReachable(draft, owns, nothingRuns);
	const step = (wanted > reach ? reach : wanted) as EvaluateStep;
	const go = (s: EvaluateStep) => void setStepRaw(s === 1 ? null : s);

	return (
		<section className="mx-auto flex w-full max-w-[1100px] min-w-0 flex-col gap-6" data-testid="evaluate-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				actions={
					<div className="flex items-center gap-2">
						<Button variant="ghost" size="sm" onClick={() => (reset(), go(1))} data-testid="evaluate-reset">
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
			<Stepper step={step} reach={reach} onGo={go} />
			<p className="text-muted-foreground text-xs">{t("stepOf", { n: step })}</p>

			<div className="min-w-0">
				{step === 1 && <Step1Agent />}
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
						<Button
							disabled={!stepReady(step, draft, owns, nothingRuns)}
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