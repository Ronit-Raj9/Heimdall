import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import "@/lib/types/evaluateI18n";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { ArrowLeft, ArrowRight, Check } from "lucide-react";
import { parseAsInteger, parseAsString, useQueryState } from "nuqs";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { EvaluateDraftProvider, learnStepReady, useEvaluateDraft, type EvaluateDraft } from "./evaluateDraft";
import { asEvaluateStep, EVALUATE_STEPS, type EvaluateStep } from "./evaluate.utils";
import { Step1Agent } from "./step1Agent";
import { Step2Learn } from "./step2Learn";
import { Step3Benchmarks } from "./step3Benchmarks";
import { Step4Setup } from "./step4Setup";
import { Step5Launch } from "./step5Launch";

/** Whether Next is allowed from a step. Steps 3 and 4 use simple checks their lanes may refine. */
export function stepReady(step: EvaluateStep, d: EvaluateDraft): boolean {
	switch (step) {
		case 1:
			return Boolean(d.agent);
		case 2:
			return learnStepReady(d);
		case 3:
			return d.benchmarks.length > 0 || Boolean(d.pack);
		case 4:
			return d.arms.length > 0 && d.arms.every((a) => Boolean(a.agent) && Boolean(a.model));
		default:
			return false;
	}
}

/** Highest step reachable from the draft: each earlier step must be ready. */
export function maxReachable(d: EvaluateDraft): EvaluateStep {
	let s: EvaluateStep = 1;
	while (s < 5 && stepReady(s, d)) s = (s + 1) as EvaluateStep;
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
	const { draft, update, reset } = useEvaluateDraft();
	const [stepRaw, setStepRaw] = useQueryState("step", parseAsInteger);
	const [agentParam, setAgentParam] = useQueryState("agent", parseAsString);

	// Back from /agents/new?return=/evaluate: the new agent arrives as ?agent=<id>.
	useEffect(() => {
		if (agentParam) {
			if (agentParam !== draft.agent) update({ agent: agentParam, version: null, compareVersion: null });
			void setAgentParam(null);
		}
	}, [agentParam, draft.agent, update, setAgentParam]);

	const reach = maxReachable(draft);
	const wanted = asEvaluateStep(stepRaw);
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
						<Button asChild variant="link" size="sm">
							<Link to="/welcome">{t("oldWizard")}</Link>
						</Button>
					</div>
				}
			/>
			<Stepper step={step} reach={reach} onGo={go} />
			<p className="text-muted-foreground text-xs">{t("stepOf", { n: step })}</p>

			<div className="min-w-0">
				{step === 1 && <Step1Agent />}
				{step === 2 && <Step2Learn />}
				{step === 3 && <Step3Benchmarks />}
				{step === 4 && <Step4Setup />}
				{step === 5 && <Step5Launch />}
			</div>

			<div className="flex items-center justify-between border-t pt-4">
				<Button variant="outline" disabled={step === 1} onClick={() => go((step - 1) as EvaluateStep)} data-testid="evaluate-back">
					<ArrowLeft />
					{t("back")}
				</Button>
				{step < 5 && (
					<Button disabled={!stepReady(step, draft)} onClick={() => go((step + 1) as EvaluateStep)} data-testid="evaluate-next">
						{t("next")}
						<ArrowRight />
					</Button>
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
