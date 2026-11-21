import { TERMINAL_STATES } from "@/components/experiments/experiments.utils";
import { ModelPicker, type ModelRef } from "@/components/models/modelPicker";
import { RunButton } from "@/components/run/runButton";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { VerdictCard } from "@/components/verdict/verdictCard";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useGetAgentsQuery, useGetExperimentQuery, useGetHealthQuery, useGetSuitesQuery, useGetVerdictQuery } from "@/lib/store/apis/benchApi";
import { githubStartUrl } from "@/lib/store/apis/platformApi";
import { useWelcomeQuickRunMutation } from "@/lib/store/apis/welcomeApi";
import { launchAvailable, type AgentSummary, type Decision, type SuiteSummary } from "@/lib/types/bench";
import "@/lib/types/welcomeI18n";
import { cn } from "@/lib/utils";
import { Link, useRouterState } from "@tanstack/react-router";
import { ArrowLeft, ArrowRight, BarChart3, Bot, Boxes, Check, ChevronDown, CircleAlert, Clock, Cpu, Github, Loader2, PartyPopper, Play, Sparkles, Trophy, Wallet } from "lucide-react";
import { parseAsInteger, parseAsString, useQueryState } from "nuqs";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AgentTile, ArmBars, CATEGORY_ICON, CatalogTile, CategoryChips, IconTile, LiveProgress, ModelQuickPick, Monogram, ProviderMark, SuiteTile } from "./welcomeParts";
import {
	asStep,
	categoryOf,
	roughCostUsd,
	SUITE_CATEGORIES,
	type SuiteCategory,
	clampTasks,
	DEFAULT_TASKS,
	defaultAgent,
	defaultSuite,
	errorMessageOf,
	errorStatusOf,
	markWelcomeSeen,
	RUN_SIZES,
	sortAgents,
	sortSuites,
	WIZARD_STEPS,
	type WizardStep,
} from "./welcome.utils";

const PROGRESS_POLL_MS = 3_000;

/**
 * /welcome: the first-run wizard. Suite, agent, model, Run, then the live progress and the verdict
 * on the same page, so a new user reaches a verdict without leaving it. Every choice lives in the
 * URL (?step, ?suite, ?agent, ?exp), so a reload or a shared link resumes where it was.
 */
export default function WelcomeView() {
	const { t } = useTranslation("welcome");
	const [stepRaw, setStepRaw] = useQueryState("step", parseAsString);
	const [suite, setSuite] = useQueryState("suite", parseAsString);
	const [agent, setAgent] = useQueryState("agent", parseAsString);
	const [exp, setExp] = useQueryState("exp", parseAsString);
	const [tasks, setTasks] = useQueryState("tasks", parseAsInteger.withDefault(DEFAULT_TASKS));
	const [model, setModel] = useState<ModelRef | null>(null);

	const suites = useGetSuitesQuery();
	const agents = useGetAgentsQuery();

	useEffect(() => markWelcomeSeen(), []);

	// Pre-select the starter suite and agent once the lists arrive.
	useEffect(() => {
		if (!suite && suites.data) {
			const id = defaultSuite(suites.data.suites ?? []);
			if (id) void setSuite(id);
		}
	}, [suite, suites.data, setSuite]);
	useEffect(() => {
		if (!agent && agents.data) {
			const id = defaultAgent(agents.data.agents ?? []);
			if (id) void setAgent(id);
		}
	}, [agent, agents.data, setAgent]);

	// A run in the URL always shows the verdict step; a lost model (reload) goes back to the model step.
	let step = asStep(stepRaw);
	if (exp) step = "verdict";
	else if (step === "verdict") step = "run";
	if (step === "run" && !model?.model) step = "model";
	if ((step === "model" || step === "run") && !agent) step = "agent";
	if (step !== "suite" && !suite) step = "suite";

	const go = (s: WizardStep) => void setStepRaw(s === "suite" ? null : s);
	const restart = () => {
		void setExp(null);
		void setStepRaw(null);
	};

	const suiteObj = suites.data?.suites.find((s) => s.id === suite);
	const agentObj = agents.data?.agents.find((a) => a.id === agent);
	// Start the model step on the agent's own model, so Continue works without typing.
	const [prefilledFor, setPrefilledFor] = useState<string | null>(null);
	useEffect(() => {
		if (agentObj?.model && prefilledFor !== agentObj.id) {
			setPrefilledFor(agentObj.id);
			setModel({ provider: providerForModel(agentObj.model), model: agentObj.model });
		}
	}, [agentObj?.id, agentObj?.model, prefilledFor]);

	return (
		<section className="mx-auto flex w-full max-w-4xl min-w-0 flex-col gap-6 pb-10" data-testid="welcome-view">
			<header className="flex flex-col gap-3">
				<div className="flex items-start gap-3">
					<span className="bg-primary text-primary-foreground flex size-10 shrink-0 items-center justify-center rounded-xl shadow-sm">
						<Sparkles className="size-5" aria-hidden="true" />
					</span>
					<div className="flex min-w-0 flex-1 flex-col gap-1">
						<h1 className="text-2xl font-semibold tracking-tight" data-testid="welcome-title">
							{t("title")}
						</h1>
						<p className="text-muted-foreground text-sm">{t("subtitle")}</p>
					</div>
					<Button asChild variant="ghost" size="sm" className="shrink-0">
						<Link to="/" data-testid="welcome-skip">
							{t("skip")}
						</Link>
					</Button>
				</div>
				<Stepper current={step} onPick={go} done={{ suite: Boolean(suite), agent: Boolean(agent), model: Boolean(model?.model), run: Boolean(exp) }} />
			</header>

			<div key={step} className="animate-in fade-in slide-in-from-bottom-1 duration-200">
				{step === "suite" && (
					<StepCard icon={Boxes} title={t("suite.heading")} help={t("suite.help")} testId="welcome-step-suite">
						{suites.isLoading ? (
							<BlockSkeleton rows={3} testId="welcome-suites-loading" />
						) : suites.error ? (
							<LoadError title={t("suite.loadError")} error={suites.error} onRetry={suites.refetch} testId="welcome-suites-error" />
						) : (suites.data?.suites.length ?? 0) === 0 ? (
							<EmptyState
								icon={Boxes}
								message={t("suite.empty")}
								action={{ label: t("suite.browseCatalog"), to: "/catalog" }}
								testId="welcome-suites-empty"
							/>
						) : (
							<SuitePicker suites={suites.data?.suites ?? []} value={suite} onPick={(id) => void setSuite(id)} />
						)}
						<NavRow onNext={suite ? () => go("agent") : undefined} />
					</StepCard>
				)}

				{step === "agent" && (
					<StepCard icon={Bot} title={t("agent.heading")} help={t("agent.help")} testId="welcome-step-agent">
						{agents.isLoading ? (
							<BlockSkeleton rows={3} testId="welcome-agents-loading" />
						) : agents.error ? (
							<LoadError title={t("agent.loadError")} error={agents.error} onRetry={agents.refetch} testId="welcome-agents-error" />
						) : (agents.data?.agents.length ?? 0) === 0 ? (
							<EmptyState icon={Bot} message={t("agent.empty")} action={{ label: t("agent.create"), to: "/agents/new" }} testId="welcome-agents-empty" />
						) : (
							<>
								<ChoiceGrid>
									{sortAgents(agents.data?.agents ?? []).map((a) => (
										<AgentTile key={a.id} agent={a} selected={a.id === agent} onPick={() => void setAgent(a.id)} />
									))}
								</ChoiceGrid>
								<p className="text-muted-foreground text-xs">
									<Link to="/agents/new" className="text-primary underline underline-offset-2" data-testid="welcome-agent-template">
										{t("agent.createAnother")}
									</Link>
								</p>
							</>
						)}
						<NavRow onBack={() => go("suite")} onNext={agent ? () => go("model") : undefined} />
					</StepCard>
				)}

				{step === "model" && (
					<StepCard icon={Cpu} title={t("model.heading")} help={t("model.help")} testId="welcome-step-model">
						<ModelQuickPick value={model} onChange={setModel} />
						<div className="bg-muted/40 flex flex-col gap-3 rounded-xl border p-4">
							<ModelPicker value={model} onChange={setModel} purpose="agent" testId="welcome-model-picker" />
							{agentObj?.model && <p className="text-muted-foreground text-xs">{t("agent.defaultModel", { model: agentObj.model })}</p>}
						</div>
						<p className="text-muted-foreground text-xs">
							<Link to="/models" className="text-primary underline underline-offset-2" data-testid="welcome-add-key">
								{t("model.addKey")}
							</Link>
						</p>
						<NavRow
							onBack={() => go("agent")}
							onNext={model?.model ? () => go("run") : undefined}
							hint={model?.model ? undefined : t("model.required")}
						/>
					</StepCard>
				)}

				{step === "run" && suite && agent && model && (
					<RunStep
						suite={suiteObj}
						suiteId={suite}
						agent={agentObj}
						agentId={agent}
						model={model}
						tasks={clampTasks(tasks, suiteObj)}
						onTasks={(n) => void setTasks(n === DEFAULT_TASKS ? null : n)}
						onBack={() => go("model")}
						onStarted={(id) => {
							void setExp(id);
							void setStepRaw("verdict");
						}}
					/>
				)}

				{step === "verdict" && exp && <VerdictStep expId={exp} suite={suite ?? undefined} agent={agent ?? undefined} onRestart={restart} />}
			</div>
		</section>
	);
}

const STEP_ICON = { suite: Boxes, agent: Bot, model: Cpu, run: Play, verdict: Trophy } as const;

function Stepper({ current, onPick, done }: { current: WizardStep; onPick(s: WizardStep): void; done: Record<Exclude<WizardStep, "verdict">, boolean> }) {
	const { t } = useTranslation("welcome");
	const at = WIZARD_STEPS.indexOf(current);
	return (
		<ol className="flex items-center gap-1 overflow-x-auto pb-1" data-testid="welcome-stepper" aria-label={t("title")}>
			{WIZARD_STEPS.map((s, i) => {
				const Icon = STEP_ICON[s];
				const isDone = i < at || (s !== "verdict" && done[s] && i < at);
				const reachable = i < at && current !== "verdict";
				return (
					<li key={s} className="flex min-w-0 flex-1 items-center gap-1">
						<button
							type="button"
							disabled={!reachable}
							onClick={() => onPick(s)}
							aria-current={i === at ? "step" : undefined}
							className={cn(
								"flex min-w-0 items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium whitespace-nowrap transition-colors",
								i === at && "border-primary bg-primary text-primary-foreground shadow-sm",
								isDone && i !== at && "border-chart-success/50 bg-chart-success/10 text-chart-success-ink hover:bg-chart-success/20",
								i > at && "text-muted-foreground border-dashed",
							)}
							data-testid={`welcome-stepper-${s}`}
						>
							{isDone && i !== at ? <Check className="size-3.5" aria-hidden="true" /> : <Icon className="size-3.5" aria-hidden="true" />}
							<span>
								{i + 1}. {t(`steps.${s}`)}
							</span>
						</button>
						{i < WIZARD_STEPS.length - 1 && <span className={cn("h-px min-w-3 flex-1", i < at ? "bg-chart-success/50" : "bg-border")} aria-hidden="true" />}
					</li>
				);
			})}
		</ol>
	);
}

function StepCard({ icon: Icon, title, help, testId, children }: { icon: typeof Boxes; title: string; help: string; testId: string; children: ReactNode }) {
	return (
		<div className="bg-card flex flex-col gap-4 rounded-xl border p-5 shadow-(--shadow-card) md:p-6" data-testid={testId}>
			<div className="flex items-start gap-3">
				<Icon className="text-primary mt-0.5 size-5 shrink-0" aria-hidden="true" />
				<div className="flex flex-col gap-1">
					<h2 className="text-lg font-semibold tracking-tight">{title}</h2>
					{help && <p className="text-muted-foreground text-sm">{help}</p>}
				</div>
			</div>
			{children}
		</div>
	);
}

function ChoiceGrid({ children }: { children: ReactNode }) {
	return (
		<div role="radiogroup" className="grid max-h-[32rem] gap-3 overflow-y-auto p-1 sm:grid-cols-2 lg:grid-cols-3">
			{children}
		</div>
	);
}

/** Suite tiles with category filter chips and a catalog tile at the end. */
function SuitePicker({ suites, value, onPick }: { suites: SuiteSummary[]; value: string | null; onPick(id: string): void }) {
	const [cat, setCat] = useState<SuiteCategory | null>(null);
	const sorted = useMemo(() => sortSuites(suites), [suites]);
	const present = useMemo(() => {
		const have = new Set(sorted.map((s) => categoryOf(s)));
		return SUITE_CATEGORIES.filter((c) => have.has(c));
	}, [sorted]);
	const shown = cat ? sorted.filter((s) => categoryOf(s) === cat) : sorted;
	return (
		<>
			<CategoryChips present={present} value={cat} onChange={setCat} />
			<ChoiceGrid>
				{shown.map((s) => (
					<SuiteTile key={s.id} suite={s} selected={s.id === value} onPick={() => onPick(s.id)} />
				))}
				<CatalogTile />
			</ChoiceGrid>
		</>
	);
}

function NavRow({ onBack, onNext, hint }: { onBack?: () => void; onNext?: () => void; hint?: string }) {
	const { t } = useTranslation("welcome");
	return (
		<div className="flex items-center gap-3 border-t pt-4">
			{onBack && (
				<Button variant="ghost" onClick={onBack} data-testid="welcome-back">
					<ArrowLeft className="size-4" aria-hidden="true" />
					{t("back")}
				</Button>
			)}
			{hint && <span className="text-muted-foreground ml-auto text-xs">{hint}</span>}
			<Button className={cn(!hint && "ml-auto")} disabled={!onNext} onClick={onNext} data-testid="welcome-next">
				{t("next")}
				<ArrowRight className="size-4" aria-hidden="true" />
			</Button>
		</div>
	);
}

function RunStep(props: {
	suite?: SuiteSummary;
	suiteId: string;
	agent?: AgentSummary;
	agentId: string;
	model: ModelRef;
	tasks: number;
	onTasks(n: number): void;
	onBack(): void;
	onStarted(expId: string): void;
}) {
	const { t } = useTranslation("welcome");
	const { t: tz } = useTranslation("wizard");
	const session = useSession();
	const launch = launchAvailable(useGetHealthQuery().data);
	const [quickRun, { isLoading, error, reset }] = useWelcomeQuickRunMutation();
	const mayRun = canUse(session, "member");

	const run = async () => {
		reset();
		const res = await quickRun({ suite: props.suiteId, agent: props.agentId, model: props.model, tasks: props.tasks, trials: 1 });
		if ("data" in res && res.data?.exp_id) props.onStarted(res.data.exp_id);
	};

	const status = errorStatusOf(error);
	return (
		<StepCard icon={Play} title={t("run.heading")} help={t("run.help")} testId="welcome-step-run">
			<div className="overflow-hidden rounded-xl border" data-testid="welcome-summary">
				<div className="grid divide-y sm:grid-cols-3 sm:divide-x sm:divide-y-0">
					<SummaryCell label={t("run.suite")} testId="welcome-summary-suite" value={props.suiteId}>
						<IconTile icon={CATEGORY_ICON[props.suite ? categoryOf(props.suite) : "coding"]} className="bg-primary/10 text-primary" size="sm" />
					</SummaryCell>
					<SummaryCell label={t("run.agent")} testId="welcome-summary-agent" value={props.agentId}>
						<Monogram name={props.agentId} className="size-7 rounded-lg text-[10px]" />
					</SummaryCell>
					<SummaryCell label={t("run.model")} testId="welcome-summary-model" value={`${props.model.provider}/${props.model.model}`}>
						<ProviderMark provider={props.model.provider} />
					</SummaryCell>
				</div>
				<div className="bg-muted/30 flex flex-col gap-3 border-t p-4">
					<div className="flex flex-wrap items-center gap-2">
						<span className="text-muted-foreground mr-1 text-sm">{t("run.size")}</span>
						{RUN_SIZES.map((n) => {
							const max = props.suite?.task_count ?? 0;
							const disabled = max > 0 && n > max && n !== RUN_SIZES[0];
							return (
								<Button
									key={n}
									size="sm"
									variant={props.tasks === n ? "default" : "outline"}
									className="rounded-full"
									disabled={disabled}
									onClick={() => props.onTasks(n)}
									data-testid={`welcome-size-${n}`}
								>
									{t("run.tasks", { count: n })}
								</Button>
							);
						})}
						<span className="text-muted-foreground text-xs">{t("run.trialNote")}</span>
					</div>
					<div className="grid gap-3 sm:grid-cols-2">
						<div className="flex items-center gap-3" data-testid="welcome-cost">
							<IconTile icon={Wallet} className="bg-chart-success/10 text-chart-success-ink" />
							<div className="flex flex-col">
								<span className="text-muted-foreground text-xs">{tz("cost")}</span>
								<span className="text-base font-semibold tabular-nums">{tz("costValue", { cost: roughCostUsd(props.model.model, props.tasks).toFixed(2) })}</span>
								<span className="text-muted-foreground text-[11px]">
									{tz("runs", { count: props.tasks * 2 })}. {tz("costNote")}
								</span>
							</div>
						</div>
						<div className="flex items-center gap-3">
							<IconTile icon={Clock} className="bg-chart-info/10 text-chart-info-ink" />
							<div className="flex flex-col">
								<span className="text-muted-foreground text-xs">{tz("time")}</span>
								<span className="text-base font-semibold">{tz("timeValue")}</span>
							</div>
						</div>
					</div>
				</div>
			</div>

			{!mayRun ? (
				<GuestNote />
			) : !launch ? (
				<Notice tone="warn" testId="welcome-no-launcher">
					{t("run.noLauncher")}
				</Notice>
			) : (
				error && (
					<Notice tone="error" testId="welcome-run-error">
						{status === 404 ? t("run.notFound") : t("run.failed", { message: errorMessageOf(error, String(status ?? "network error")) })}{" "}
						<Link to="/experiments/new" className="underline underline-offset-2">
							{t("run.advanced")}
						</Link>
					</Notice>
				)
			)}

			<div className="flex items-center gap-3 border-t pt-4">
				<Button variant="ghost" onClick={props.onBack} data-testid="welcome-back">
					<ArrowLeft className="size-4" aria-hidden="true" />
					{t("back")}
				</Button>
				<Button size="lg" className="ml-auto h-12 min-w-52 rounded-xl text-base shadow-md transition-transform hover:scale-[1.02] active:scale-[0.98]" disabled={!mayRun || !launch || isLoading} onClick={() => void run()} data-testid="welcome-run">
					{isLoading ? <Loader2 className="size-4 animate-spin" aria-hidden="true" /> : <Play className="size-4" aria-hidden="true" />}
					{isLoading ? t("run.starting") : t("run.button")}
				</Button>
			</div>
		</StepCard>
	);
}

function SummaryCell({ label, value, testId, children }: { label: string; value: string; testId: string; children: ReactNode }) {
	return (
		<div className="flex min-w-0 items-center gap-3 p-4">
			{children}
			<div className="flex min-w-0 flex-col">
				<span className="text-muted-foreground text-xs">{label}</span>
				<span className="truncate font-mono text-sm font-medium" title={value} data-testid={testId}>
					{value}
				</span>
			</div>
		</div>
	);
}

/** A visitor or viewer cannot run: say why and offer the way in. */
function GuestNote() {
	const { t } = useTranslation("welcome");
	const session = useSession();
	const href = useRouterState({ select: (s) => s.location.href });
	if (session.signedIn) {
		return (
			<Notice tone="warn" testId="welcome-read-only">
				{t("run.readOnly")}
			</Notice>
		);
	}
	return (
		<div className="flex flex-wrap items-center gap-3 rounded-lg border border-dashed p-4" data-testid="welcome-guest">
			<span className="text-sm font-medium">{session.github ? t("guest.cta") : t("guest.ctaPlain")}</span>
			<Button asChild size="sm" className="ml-auto">
				{session.github ? (
					<a href={githubStartUrl(href)} data-testid="welcome-guest-github">
						<Github className="size-4" aria-hidden="true" />
						GitHub
					</a>
				) : (
					<Link to="/login" search={{ return_to: href }} data-testid="welcome-guest-login">
						{t("guest.ctaPlain")}
					</Link>
				)}
			</Button>
		</div>
	);
}

function Notice({ tone, testId, children }: { tone: "warn" | "error" | "info"; testId: string; children: ReactNode }) {
	return (
		<div
			role={tone === "error" ? "alert" : "status"}
			className={cn(
				"flex items-start gap-2 rounded-lg border px-3 py-2.5 text-sm",
				tone === "error" && "border-chart-error/50 bg-chart-error/10",
				tone === "warn" && "border-chart-warning/50 bg-chart-warning/10",
				tone === "info" && "border-chart-info/40 bg-chart-info/10",
			)}
			data-testid={testId}
		>
			<CircleAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<span className="min-w-0">{children}</span>
		</div>
	);
}

const DECISION_STYLE: Record<Decision, string> = {
	improved: "border-chart-success/50 bg-chart-success/10 text-chart-success-ink",
	regressed: "border-chart-error/50 bg-chart-error/10 text-chart-error-ink",
	inconclusive: "border-chart-info/40 bg-chart-info/10 text-chart-info-ink",
	invalid: "border-chart-warning/50 bg-chart-warning/10 text-chart-warning-ink",
};

function VerdictStep({ expId, suite, agent, onRestart }: { expId: string; suite?: string; agent?: string; onRestart(): void }) {
	const { t } = useTranslation("welcome");
	const [poll, setPoll] = useState(PROGRESS_POLL_MS);
	const status = useGetExperimentQuery(expId, { pollingInterval: poll });
	const state = status.data?.state;
	const terminal = state ? TERMINAL_STATES.has(state) : false;
	useEffect(() => setPoll(terminal ? 0 : PROGRESS_POLL_MS), [terminal]);
	const verdict = useGetVerdictQuery(expId, { skip: state !== "done" });

	return (
		<StepCard icon={Trophy} title={t("verdict.heading")} help={terminal ? "" : t("verdict.waiting")} testId="welcome-step-verdict">
			{status.isLoading ? (
				<BlockSkeleton rows={2} testId="welcome-progress-loading" />
			) : status.error ? (
				<LoadError title={t("verdict.loadError")} error={status.error} onRetry={status.refetch} testId="welcome-progress-error" />
			) : status.data && state !== "done" ? (
				<LiveProgress status={status.data} />
			) : null}

			{state === "analyzing" && (
				<Notice tone="info" testId="welcome-analyzing">
					{t("verdict.analyzing")}
				</Notice>
			)}
			{(state === "failed" || state === "cancelled") && (
				<Notice tone="error" testId="welcome-failed">
					{t("verdict.failed")}
				</Notice>
			)}

			{state === "done" &&
				(verdict.isLoading ? (
					<BlockSkeleton rows={3} testId="welcome-verdict-loading" />
				) : verdict.error ? (
					<LoadError title={t("verdict.loadError")} error={verdict.error} onRetry={verdict.refetch} testId="welcome-verdict-error" />
				) : verdict.data ? (
					<VerdictHero decision={verdict.data.verdict.decision} naive={verdict.data.naive ?? {}}>
						<VerdictCard verdict={verdict.data.verdict} />
					</VerdictHero>
				) : null)}

			<div className="flex flex-wrap items-center gap-2 border-t pt-4">
				<Button variant="ghost" onClick={onRestart} data-testid="welcome-restart">
					{t("startOver")}
				</Button>
				<div className="ml-auto flex flex-wrap items-center gap-2">
					{terminal && (
						<>
							<RunButton suite={suite} agent={agent} label={t("verdict.compare")} />
							<Button asChild variant="outline">
								<Link to="/releases" data-testid="welcome-release">
									{t("verdict.release")}
								</Link>
							</Button>
						</>
					)}
					<Button asChild variant={terminal ? "default" : "outline"}>
						<Link to="/experiments/$expId" params={{ expId }} data-testid="welcome-open">
							{t("verdict.open")}
							<ArrowRight className="size-4" aria-hidden="true" />
						</Link>
					</Button>
				</div>
			</div>
		</StepCard>
	);
}

const DECISION_ICON: Record<Decision, typeof Trophy> = { improved: PartyPopper, regressed: CircleAlert, inconclusive: BarChart3, invalid: CircleAlert };

/** The clear verdict: headline, one-line meaning, A vs B bars; the full statistics on demand. */
function VerdictHero({ decision, naive, children }: { decision: Decision; naive: Record<string, { num: number; den: number }>; children: ReactNode }) {
	const { t } = useTranslation("welcome");
	const { t: tz } = useTranslation("wizard");
	const Icon = DECISION_ICON[decision] ?? Trophy;
	const winner = decision === "improved" ? "B" : decision === "regressed" ? "A" : undefined;
	return (
		<div className="animate-in fade-in zoom-in-95 flex flex-col gap-4 duration-500" data-testid="welcome-verdict">
			<div className={cn("relative flex flex-col gap-4 overflow-hidden rounded-2xl border p-5 md:p-6", DECISION_STYLE[decision])}>
				{decision === "improved" && <Confetti />}
				<div className="relative flex items-start gap-4">
					<span className="bg-background/70 flex size-12 shrink-0 items-center justify-center rounded-2xl shadow-sm">
						<Icon className={cn("size-6", decision === "improved" && "animate-bounce")} aria-hidden="true" />
					</span>
					<div className="flex min-w-0 flex-col gap-1">
						<p className="text-xl font-semibold tracking-tight md:text-2xl" data-testid="welcome-verdict-headline">
							{t(`verdict.headline.${decision}`)}
						</p>
						<p className="text-foreground/80 text-sm">{t(`verdict.explain.${decision}`)}</p>
					</div>
				</div>
				<div className="relative">
					<ArmBars naive={naive} winner={winner} />
				</div>
			</div>
			<details className="group rounded-xl border">
				<summary className="hover:bg-accent/40 flex cursor-pointer list-none items-center gap-2 rounded-xl px-4 py-3 text-sm font-medium">
					<BarChart3 className="text-muted-foreground size-4" aria-hidden="true" />
					{tz("verdictDetails")}
					<ChevronDown className="text-muted-foreground ml-auto size-4 transition-transform group-open:rotate-180" aria-hidden="true" />
				</summary>
				<div className="border-t p-4">{children}</div>
			</details>
		</div>
	);
}

const CONFETTI = ["bg-chart-success", "bg-chart-info", "bg-chart-warning", "bg-primary", "bg-pink-500"];

/** A few drifting dots behind an improved verdict; decorative and motion-safe. */
function Confetti() {
	return (
		<div className="pointer-events-none absolute inset-0 motion-reduce:hidden" aria-hidden="true">
			{Array.from({ length: 14 }, (_, i) => (
				<span
					key={i}
					className={cn("animate-in fade-in slide-in-from-top-8 absolute size-1.5 rounded-full opacity-70 duration-1000", CONFETTI[i % CONFETTI.length])}
					style={{ left: `${(i * 37) % 100}%`, top: `${(i * 23) % 70}%`, animationDelay: `${i * 60}ms` }}
				/>
			))}
		</div>
	);
}

/** Best guess of the gateway provider of an agent's default model name. */
function providerForModel(model: string): ModelRef["provider"] {
	const m = model.toLowerCase();
	if (m.startsWith("claude")) return "anthropic";
	if (m.startsWith("gemini")) return "gemini";
	return "openai";
}
