import { type AgentWithCaps, splitAgents } from "@/app/evaluate/views/evaluate.utils";
import { Button } from "@/components/ui/button";
import { DetailsDisclosure } from "@/components/ui/detailsDisclosure";
import { Input } from "@/components/ui/input";
import { useGetAgentConfigQuery } from "@/lib/store/apis/agentBuilderApi";
import { getBenchErrorMessage, useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import { type ImprovementSpecBody, useCreateImprovementMutation, useEstimateImprovementMutation } from "@/lib/store/apis/improveApi";
import type { ImprovementEstimate } from "@/lib/types/improve";
import { useNavigate } from "@tanstack/react-router";
import { Check, Copy } from "lucide-react";
import { Dialog } from "@/components/ui/dialog";
import { Checkbox } from "@/components/ui/checkbox";
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select";
import { PaidButton } from "@/components/ui/paidButton";
import { useFormDraft } from "@/lib/hooks/useFormDraft";
import { useLeaveGuard } from "@/lib/hooks/useLeaveGuard";
import type { SuiteSummary } from "@/lib/types/bench";
import { useEffect, useRef, useState } from "react";
import { flushSync } from "react-dom";
import i18n from "@/lib/i18n";
import { useTranslation } from "react-i18next";
import { EstimateCard, estimateBlock } from "./estimateCard";
import { money } from "./improve.utils";
import { PRESETS, type PresetName, PresetPicker, TRIAL_LIMITS } from "./presetPicker";
import "./w45impI18n";
import "./uf2bI18n";
import "./uff6I18n";
import "./ufl7I18n";
import { useCreateImprovementStudyMutation } from "@/lib/store/apis/improveStudyApi";

/** The create body plus the task count of a trial or a custom size (improve.Spec max_tasks). */
export type ImprovementBody = ImprovementSpecBody & { max_tasks?: number; replicate?: { loops: number } };

/** Loops a study runs (improve.MaxLoops); 1 is a plain improvement run. */
export const MAX_LOOPS = 5;

/** The improvement name rule the server enforces (improve/spec.go nameRe). */
export const IMPROVEMENT_NAME = /^[a-z0-9][a-z0-9-]{0,62}$/;
/** Rounds the server accepts (improve.MaxRounds), and ideas per round the form offers for a custom size. */
export const MAX_IMPROVEMENT_ROUNDS = 20;
export const MAX_IDEAS = 8;
/** The improver task every run of the form uses (improve.DefaultImproverTask), shown read only. */
export const DEFAULT_IMPROVER_TASK = "heimdall-improver@1.1.0:reflect-edit";
/** Wait this long after the last change before asking for a new estimate (A2.9). */
export const ESTIMATE_DELAY_MS = 400;

/** What the form holds. Numbers stay text while typed. */
export interface ImprovementForm {
	name: string;
	agent: string;
	/** <suite id>@<version> */
	suite: string;
	/** The standing instructions may change; ticked by default so every agent can be improved. */
	instructions: boolean;
	/** Ticked paths of the agent's files map; folders end in "/". */
	files: string[];
	preset: PresetName;
	/** Trial and custom sizes only. */
	rounds: string;
	ideas: string;
	/** Tasks taken from the benchmark: a trial's count, or empty (every task) for a custom size. */
	tasks: string;
	/** The whole run's spending limit in dollars, prefilled from the preset. */
	limit: string;
	/** Whole loops from version 0: 1 is a run, 2 to 5 a study (POST /improvement-studies). */
	loops: string;
}

/** An empty form: the smallest size (a trial), standing instructions ticked. */
export function newForm(): ImprovementForm {
	const p = PRESETS.trial;
	return {
		name: "",
		agent: "",
		suite: "",
		instructions: true,
		files: [],
		preset: "trial",
		rounds: String(p.rounds),
		ideas: String(p.ideas),
		tasks: String(p.pool),
		limit: String(p.limit),
		loops: "1",
	};
}

/** The loop count of the form, 1 when not a whole number yet. */
export function formLoops(f: ImprovementForm): number {
	const n = Number(f.loops ?? "1");
	return Number.isInteger(n) && n >= 1 ? n : 1;
}

/** True when the form starts a study (2 or more loops). */
export function isStudy(f: ImprovementForm): boolean {
	return f.preset !== "trial" && formLoops(f) >= 2;
}

export type ImprovementFormProblem = "name" | "agent" | "suite" | "surface" | "rounds" | "ideas" | "tasks" | "limit" | "loops";

/** True when the size sends its own rounds, ideas and task count. */
function ownSize(preset: PresetName): boolean {
	return preset === "custom" || preset === "trial";
}

/** Every reason the form cannot be sent yet, in field order. */
export function improvementFormProblems(f: ImprovementForm): ImprovementFormProblem[] {
	const out: ImprovementFormProblem[] = [];
	if (!IMPROVEMENT_NAME.test(f.name)) out.push("name");
	if (!f.agent) out.push("agent");
	if (!f.suite) out.push("suite");
	if (!f.instructions && f.files.length === 0) out.push("surface");
	if (ownSize(f.preset)) {
		const trial = f.preset === "trial";
		const rounds = Number(f.rounds);
		const maxRounds = trial ? TRIAL_LIMITS.maxRounds : MAX_IMPROVEMENT_ROUNDS;
		if (!Number.isInteger(rounds) || rounds < 1 || rounds > maxRounds) out.push("rounds");
		const ideas = Number(f.ideas);
		const maxIdeas = trial ? TRIAL_LIMITS.maxIdeas : MAX_IDEAS;
		if (!Number.isInteger(ideas) || ideas < 1 || ideas > maxIdeas) out.push("ideas");
		const tasks = Number(f.tasks);
		if (trial) {
			if (!Number.isInteger(tasks) || tasks < TRIAL_LIMITS.minTasks || tasks > TRIAL_LIMITS.maxTasks) out.push("tasks");
		} else if (f.tasks.trim() !== "" && (!Number.isInteger(tasks) || tasks < 3)) out.push("tasks");
	}
	const limit = Number(f.limit);
	if (f.limit.trim() === "" || !Number.isFinite(limit) || limit <= 0) out.push("limit");
	const loops = Number(f.loops ?? "1");
	if (!Number.isInteger(loops) || loops < 1 || loops > MAX_LOOPS || (f.preset === "trial" && loops !== 1)) out.push("loops");
	return out;
}

/**
 * The POST /improvements body (A1.8): the preset, the default improver task, what may change (a path
 * ending in "/" is a folder, editable.dirs) and the spending limit. Rounds, ideas per round and the
 * task count are sent only for a trial or a custom size; a named preset fills them on the server.
 */
export function improvementSpec(f: ImprovementForm): ImprovementBody {
	const files = f.files.filter((p) => !p.endsWith("/"));
	const dirs = f.files.filter((p) => p.endsWith("/"));
	const custom = ownSize(f.preset);
	const body: ImprovementBody = {
		kind: "improvement",
		name: f.name,
		agent: `agents/${f.agent}.yaml`,
		suite: f.suite,
		preset: f.preset,
		editable: dirs.length > 0 ? { instructions: f.instructions, files, dirs } : { instructions: f.instructions, files },
		improver: custom
			? { task: DEFAULT_IMPROVER_TASK, candidates: Number(f.ideas), keep: Number(f.ideas) }
			: { task: DEFAULT_IMPROVER_TASK },
		budget: { total_usd: Number(f.limit) },
	};
	if (custom) body.rounds = Number(f.rounds);
	if (custom && f.tasks.trim() !== "") body.max_tasks = Number(f.tasks);
	if (isStudy(f)) body.replicate = { loops: formLoops(f) };
	return body;
}

/**
 * Why Create is disabled, or undefined when it may be pressed: the form's first problem, then a
 * pending estimate, then the estimate's first refusal (A2.9 item 8).
 */
export function createBlock(f: ImprovementForm, estimate: ImprovementEstimate | undefined, estimating: boolean): string | undefined {
	const first = improvementFormProblems(f)[0];
	if (first === "tasks") return tasksError(f.preset);
	if (first === "loops") return i18n.t("form.loopsError", { ns: "ufl7" });
	if (first) return i18n.t(`reason.${first}`, { ns: "w45imp" });
	if (estimating) return i18n.t("reason.estimating", { ns: "w45imp" });
	return estimateBlock(estimate);
}

/**
 * What picking a size changes. PAID-23: Custom keeps the limit, rounds and ideas already there (a
 * trial's task count is cleared, so Custom starts from every task); a named size fills its own.
 */
export function presetPatch(form: ImprovementForm, preset: PresetName): Partial<ImprovementForm> {
	if (preset === "custom") return { preset, tasks: form.preset === "trial" ? "" : form.tasks };
	if (preset === "trial") {
		const t = PRESETS.trial;
		return { preset, limit: String(t.limit), rounds: String(t.rounds), ideas: String(t.ideas), tasks: String(t.pool), loops: "1" };
	}
	const p = PRESETS[preset];
	return { preset, limit: String(p.limit), rounds: String(p.rounds), ideas: String(p.ideas), tasks: "" };
}

/** Why the task count is refused, for the size picked. */
function tasksError(preset: PresetName): string {
	return preset === "trial"
		? i18n.t("form.tasksError", { ns: "uff6", min: TRIAL_LIMITS.minTasks, max: TRIAL_LIMITS.maxTasks })
		: i18n.t("form.tasksErrorCustom", { ns: "uff6" });
}

/**
 * The confirm text of Start: the estimate range, the runs and the hard stop, then the trial notice
 * when the size is a trial, then what the run spends.
 */
export function improvementConfirmText(f: ImprovementForm, e: ImprovementEstimate | undefined, spends: string): string {
	const limit = limitUsd(f.limit);
	const parts: string[] = [];
	if (e && limit !== null) {
		parts.push(
			i18n.t("estimate.confirm", { ns: "uff6", low: money(e.usd.low), high: money(e.usd.high), runs: e.runs.total, limit: money(limit) }),
		);
	} else if (limit !== null) {
		parts.push(i18n.t("estimate.confirmUnknown", { ns: "uff6", limit: money(limit) }));
	}
	if (f.preset === "trial") parts.push(i18n.t("form.trialNote", { ns: "uff6" }));
	if (isStudy(f) && limit !== null) parts.push(studyLimitText(limit, formLoops(f)));
	parts.push(spends);
	return parts.join(" ");
}

/** A study's two limits in words, the server's rule (improve.LoopShareUSD: the total split, cents floored). */
export function studyLimitText(total: number, loops: number): string {
	const share = Math.floor((total * 100) / loops + 1e-9) / 100;
	return i18n.t("form.studyLimit", { ns: "ufl7", loops, total: money(total), share: money(share) });
}

/**
 * The name the form suggests when an agent is picked: <agent>-improve-<yyyymmdd>, cut to the
 * 63 characters the server accepts and without a trailing dash.
 */
export function defaultImprovementName(agent: string, now: Date = new Date()): string {
	const y = now.getFullYear();
	const m = String(now.getMonth() + 1).padStart(2, "0");
	const d = String(now.getDate()).padStart(2, "0");
	const slug = agent
		.toLowerCase()
		.replace(/[^a-z0-9-]+/g, "-")
		.replace(/^-+/, "");
	const suffix = `-improve-${y}${m}${d}`;
	return `${slug.slice(0, 63 - suffix.length).replace(/-+$/, "")}${suffix}`.replace(/^-+/, "");
}

/** One benchmark in the picker: the value sent (<id>@<version>), the text shown, and whether it can be picked. */
export interface SuiteChoice {
	value: string;
	label: string;
	/** True for a benchmark with no ready task: listed under "Not ready yet", not pickable. */
	notReady?: boolean;
}

/**
 * The benchmark picker's options: the benchmark name and its task count, the version only when two
 * versions of one benchmark are listed, sorted by value. A benchmark the server says has no ready
 * task is marked not ready.
 */
export function suiteChoices(
	suites: readonly (Pick<SuiteSummary, "id" | "version"> & Partial<Pick<SuiteSummary, "task_count" | "ready_known" | "ready_task_ids">>)[],
): SuiteChoice[] {
	const versions = new Map<string, number>();
	for (const s of suites) versions.set(s.id, (versions.get(s.id) ?? 0) + 1);
	return suites
		.map((s) => {
			const name = (versions.get(s.id) ?? 0) > 1 ? `${s.id} ${s.version}` : s.id;
			const label = typeof s.task_count === "number" ? i18n.t("improve.suiteOption", { ns: "uf2b", name, count: s.task_count }) : name;
			const notReady = s.ready_known === true && (s.ready_task_ids ?? []).length === 0;
			return { value: `${s.id}@${s.version}`, label, notReady };
		})
		.sort((a, b) => a.value.localeCompare(b.value));
}

/** The command line equivalent, with a Copy button. */
export function ImprovementCli({ testId }: { testId: string }) {
	const { t } = useTranslation("improve");
	const [copied, setCopied] = useState(false);
	const cli = t("empty.cli");
	useEffect(() => {
		if (!copied) return;
		const timer = window.setTimeout(() => setCopied(false), 1400);
		return () => window.clearTimeout(timer);
	}, [copied]);
	const copy = () => {
		try {
			void navigator.clipboard?.writeText(cli).then(
				() => setCopied(true),
				() => undefined,
			);
		} catch {
			// No clipboard (insecure context): the command is still selectable text.
		}
	};
	return (
		<DetailsDisclosure label={t("create.cli")} testId={testId}>
			<div className="flex min-w-0 items-center gap-2">
				<code className="bg-muted min-w-0 flex-1 truncate rounded px-2 py-1 text-xs" title={cli}>
					{cli}
				</code>
				<Button type="button" size="sm" variant="outline" onClick={copy} data-testid={`${testId}-copy`}>
					{copied ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
					{copied ? t("create.copied") : t("create.copy")}
				</Button>
			</div>
		</DetailsDisclosure>
	);
}

export interface NewImprovementFormProps {
	form: ImprovementForm;
	onChange(next: ImprovementForm): void;
	/** The org's own agents, by id. */
	agents: readonly string[];
	/** Benchmarks as <id>@<version>, or as picker choices with their shown text. */
	suites: readonly (string | SuiteChoice)[];
	/** The picked agent's files map paths; undefined while they load or with no agent picked. */
	agentFiles?: readonly string[];
	/** The picked agent's model, for the improver line. */
	model?: string;
	estimate?: ImprovementEstimate;
	estimating?: boolean;
	/** The estimate route's error text. */
	estimateError?: string;
	busy?: boolean;
	/** The server's refusal, shown under the form. */
	error?: string;
	onSubmit(): void | Promise<unknown>;
	onCancel(): void;
}

/** The spending limit as dollars for the paid button; null when not a positive number yet. */
export function limitUsd(limit: string): number | null {
	const n = Number(limit);
	return limit.trim() !== "" && Number.isFinite(n) && n > 0 ? n : null;
}

/** True once the person changed anything from the empty form (the leave guard asks then). */
export function improvementFormDirty(f: ImprovementForm): boolean {
	return JSON.stringify(f) !== JSON.stringify(newForm());
}

/** The dialog's form (A2.9), without data fetching. Exported for tests. */
export function NewImprovementForm({
	form,
	onChange,
	agents,
	suites,
	agentFiles,
	model = "",
	estimate,
	estimating = false,
	estimateError,
	busy,
	error,
	onSubmit,
	onCancel,
}: NewImprovementFormProps) {
	const { t } = useTranslation("improve");
	const { t: tw } = useTranslation("w45imp");
	const { t: tu } = useTranslation("uf2b");
	const { t: t6 } = useTranslation("uff6");
	const { t: t7 } = useTranslation("ufl7");
	const study = isStudy(form);
	const trial = form.preset === "trial";
	const choices: SuiteChoice[] = suites.map((s) => (typeof s === "string" ? { value: s, label: s } : s));
	const readyChoices = choices.filter((c) => !c.notReady);
	const notReadyChoices = choices.filter((c) => c.notReady);
	const problems = improvementFormProblems(form);
	const block = createBlock(form, estimate, estimating);
	const set = (patch: Partial<ImprovementForm>) => onChange({ ...form, ...patch });
	const toggle = (path: string, on: boolean) => set({ files: on ? [...form.files, path].sort() : form.files.filter((p) => p !== path) });
	const pickPreset = (preset: PresetName) => set(presetPatch(form, preset));
	// The name suggested for the last picked agent: replaced on the next pick only while not edited.
	const suggested = useRef("");
	const pickAgent = (agent: string) => {
		const patch: Partial<ImprovementForm> = { agent, files: [] };
		if (agent && (form.name === "" || form.name === suggested.current)) {
			suggested.current = defaultImprovementName(agent);
			patch.name = suggested.current;
		}
		set(patch);
	};
	return (
		<form
			aria-label={t("create.title")}
			className="flex flex-col gap-4 text-left"
			data-testid="new-improvement-form"
			onSubmit={(e) => {
				// Enter never starts paid work: only the Start button (which asks first) does.
				e.preventDefault();
			}}
		>
			<ImprovementCli testId="new-improvement-cli" />
			<label className="flex flex-col gap-1 text-sm">
				<span className="text-foreground font-medium">{t("create.name")}</span>
				<Input
					value={form.name}
					onChange={(e) => set({ name: e.target.value })}
					maxLength={63}
					autoFocus
					data-testid="new-improvement-name"
				/>
				<span className="text-muted-foreground text-xs">{tu("improve.nameHelp")}</span>
				{form.name.length > 0 && problems.includes("name") && (
					<span className="text-chart-error-ink" data-testid="new-improvement-name-error">
						{t("create.nameError")}
					</span>
				)}
			</label>
			<div className="flex flex-col gap-1 text-sm">
				<span className="text-foreground font-medium" id="new-improvement-agent-label">
					{t("create.agent")}
				</span>
				{agents.length === 0 ? (
					<span className="text-muted-foreground" data-testid="new-improvement-no-agents">
						{t("create.noAgents")}
					</span>
				) : (
					// Q-109: the shared Select, like every other form; no value shows the placeholder.
					<Select value={form.agent || undefined} onValueChange={pickAgent}>
						<SelectTrigger className="w-full" aria-labelledby="new-improvement-agent-label" data-testid="new-improvement-agent">
							<SelectValue placeholder={t("create.agentPick")} />
						</SelectTrigger>
						<SelectContent>
							{agents.map((a) => (
								<SelectItem key={a} value={a}>
									{a}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				)}
			</div>
			<div className="flex flex-col gap-1 text-sm">
				<span className="text-foreground font-medium" id="new-improvement-suite-label">
					{t("create.benchmark")}
				</span>
				<Select value={form.suite || undefined} onValueChange={(v) => set({ suite: v })}>
					<SelectTrigger className="w-full" aria-labelledby="new-improvement-suite-label" data-testid="new-improvement-suite">
						<SelectValue placeholder={t("create.benchmarkPick")} />
					</SelectTrigger>
					<SelectContent>
						{readyChoices.map((c) => (
							<SelectItem key={c.value} value={c.value}>
								{c.label}
							</SelectItem>
						))}
						{notReadyChoices.length > 0 && (
							<SelectGroup data-testid="new-improvement-suite-not-ready">
								<SelectLabel>{tu("improve.notReadyGroup")}</SelectLabel>
								{notReadyChoices.map((c) => (
									<SelectItem key={c.value} value={c.value} disabled title={tu("improve.notReadyTitle")}>
										{c.label}
									</SelectItem>
								))}
							</SelectGroup>
						)}
					</SelectContent>
				</Select>
			</div>
			<fieldset className="flex flex-col gap-1 text-sm" data-testid="new-improvement-files">
				<legend className="text-foreground mb-1 font-medium">{tw("form.surfaces")}</legend>
				<Checkbox
					checked={form.instructions}
					onChange={(on) => set({ instructions: on })}
					label={tw("form.instructions")}
					testId="new-improvement-instructions"
				/>
				{form.agent && agentFiles === undefined && <span className="text-muted-foreground">{t("create.filesLoading")}</span>}
				{(agentFiles ?? []).map((p) => (
					<Checkbox
						key={p}
						checked={form.files.includes(p)}
						onChange={(on) => toggle(p, on)}
						label={
							<>
								<code className="font-mono">{p}</code>
								{p.endsWith("/") && <span className="text-muted-foreground"> {tw("form.folder")}</span>}
							</>
						}
						testId="new-improvement-file"
					/>
				))}
				<span className="text-muted-foreground">{tw("form.fixed")}</span>
				{problems.includes("surface") && <span className="text-chart-error-ink">{tw("form.surfaceNone")}</span>}
			</fieldset>
			<fieldset className="flex flex-col gap-1 text-sm" data-testid="new-improvement-source">
				<legend className="text-foreground mb-1 font-medium">{t7("form.source")}</legend>
				<label className="flex items-center gap-2">
					<input type="radio" name="new-improvement-source" checked readOnly />
					{t7("form.sourceAgent")}
				</label>
				<label className="text-muted-foreground flex items-center gap-2" title={t7("form.sourcePromptOff")}>
					<input type="radio" name="new-improvement-source" disabled data-testid="new-improvement-source-prompt" />
					{t7("form.sourcePrompt")}
				</label>
				<span className="text-muted-foreground text-xs" data-testid="new-improvement-source-off">
					{t7("form.sourcePromptOff")}
				</span>
			</fieldset>
			<PresetPicker value={form.preset} onChange={pickPreset} />
			{trial && (
				<p className="text-chart-warning-ink text-xs" data-testid="new-improvement-trial-note">
					{t6("form.trialNote")}
				</p>
			)}
			{ownSize(form.preset) && (
				<div className="flex flex-wrap gap-3">
					<label className="flex w-28 flex-col gap-1 text-sm">
						<span className="text-foreground font-medium">{t6("form.tasks")}</span>
						<Input
							type="number"
							min={trial ? TRIAL_LIMITS.minTasks : 3}
							max={trial ? TRIAL_LIMITS.maxTasks : undefined}
							step={1}
							value={form.tasks}
							onChange={(e) => set({ tasks: e.target.value })}
							data-testid="new-improvement-tasks"
						/>
					</label>
					<label className="flex w-28 flex-col gap-1 text-sm">
						<span className="text-foreground font-medium">{t("create.rounds")}</span>
						<Input
							type="number"
							min={1}
							max={trial ? TRIAL_LIMITS.maxRounds : MAX_IMPROVEMENT_ROUNDS}
							step={1}
							value={form.rounds}
							onChange={(e) => set({ rounds: e.target.value })}
							data-testid="new-improvement-rounds"
						/>
					</label>
					<label className="flex w-36 flex-col gap-1 text-sm">
						<span className="text-foreground font-medium">{tw("form.ideas")}</span>
						<Input
							type="number"
							min={1}
							max={trial ? TRIAL_LIMITS.maxIdeas : MAX_IDEAS}
							step={1}
							value={form.ideas}
							onChange={(e) => set({ ideas: e.target.value })}
							data-testid="new-improvement-ideas"
						/>
					</label>
				</div>
			)}
			{ownSize(form.preset) && (
				<span className="text-muted-foreground text-xs">
					{trial ? t6("form.tasksHelp", { min: TRIAL_LIMITS.minTasks, max: TRIAL_LIMITS.maxTasks }) : t6("form.tasksHelpCustom")}
				</span>
			)}
			{problems.includes("tasks") && <span className="text-chart-error-ink text-xs">{tasksError(form.preset)}</span>}
			{problems.includes("rounds") && (
				<span className="text-chart-error-ink text-xs">
					{trial ? t6("form.roundsError", { max: TRIAL_LIMITS.maxRounds }) : t("create.roundsError")}
				</span>
			)}
			{problems.includes("ideas") && (
				<span className="text-chart-error-ink text-xs">
					{trial ? t6("form.ideasError", { max: TRIAL_LIMITS.maxIdeas }) : tw("form.ideasError")}
				</span>
			)}
			{!trial && (
				<label className="flex w-28 flex-col gap-1 text-sm">
					<span className="text-foreground font-medium">{t7("form.loops")}</span>
					<Input
						type="number"
						min={1}
						max={MAX_LOOPS}
						step={1}
						value={form.loops ?? "1"}
						onChange={(e) => set({ loops: e.target.value })}
						data-testid="new-improvement-loops"
					/>
				</label>
			)}
			{!trial && <span className="text-muted-foreground text-xs">{t7("form.loopsHelp")}</span>}
			{problems.includes("loops") && <span className="text-chart-error-ink text-xs">{t7("form.loopsError")}</span>}
			<label className="flex w-full flex-col gap-1 text-sm sm:w-64">
				<span className="text-foreground font-medium">{tw("form.limit")}</span>
				<Input
					type="number"
					min={0.01}
					step={0.01}
					value={form.limit}
					onChange={(e) => set({ limit: e.target.value })}
					data-testid="new-improvement-limit"
				/>
			</label>
			{problems.includes("limit") && <span className="text-chart-error-ink text-xs">{tw("form.limitError")}</span>}
			{study && limitUsd(form.limit) !== null && (
				<span className="text-muted-foreground text-xs" data-testid="new-improvement-study-limit">
					{studyLimitText(limitUsd(form.limit) ?? 0, formLoops(form))}
				</span>
			)}
			{error && (
				<p className="text-chart-error-ink text-sm" role="alert" data-testid="new-improvement-error">
					{error}
				</p>
			)}
			{/* Sticky footer: the estimate stays right above the paid Start button however long the form is. */}
			<div className="bg-card sticky bottom-0 -mx-5 -mb-5 flex flex-col gap-3 border-t px-5 pt-3 pb-5" data-testid="new-improvement-footer">
				<EstimateCard estimate={estimate} loading={estimating} error={estimateError} model={model} waiting={problems.length > 0} />
				<div className="flex flex-wrap items-center justify-end gap-2">
					{block && (
						<span className="text-muted-foreground mr-auto text-xs" data-testid="new-improvement-reason">
							{block}
						</span>
					)}
					<Button type="button" variant="ghost" onClick={onCancel} data-testid="new-improvement-cancel">
						{t("create.cancel")}
					</Button>
					<PaidButton
						verb={study ? t7("form.studyVerb") : tu("improve.startVerb")}
						capUsd={limitUsd(form.limit)}
						confirm="always"
						kind="form"
						estimateRuns={estimate?.runs.total}
						confirmText={improvementConfirmText(form, estimate, tu("improve.confirmText"))}
						disabled={block !== undefined || busy}
						isLoading={busy}
						title={block}
						onConfirm={onSubmit}
						testId="new-improvement-submit"
					/>
				</div>
			</div>
		</form>
	);
}

/**
 * New improvement run (admins): name, one of the org's own agents, a benchmark, what may change
 * (standing instructions first, then the agent's files), a size and the spending limit. Each change
 * asks for a new estimate after a short pause; Create posts the spec and opens the new run, and a
 * refusal shows under the form.
 */
export function NewImprovementDialog({ onClose, onStudy }: { onClose(): void; onStudy?(id: string): void }) {
	const navigate = useNavigate();
	const { t } = useTranslation("improve");
	const { t: tu } = useTranslation("uf2b");
	// Kept in the session while open, so a reload or a slip of the Back button does not lose it.
	const [form, setForm, clearDraft] = useFormDraft<ImprovementForm>("newImprovement", newForm());
	const [open, setOpen] = useState(true);
	// Off once the run is created, so opening the new run does not ask "Leave this page?".
	const [sent, setSent] = useState(false);
	useLeaveGuard(!sent && improvementFormDirty(form));
	const agentsQ = useGetAgentsQuery();
	// The picker needs only each benchmark id and version: the light list never waits for a pack load.
	const suitesQ = useGetSuitesLightQuery();
	const config = useGetAgentConfigQuery(form.agent, { skip: !form.agent });
	const [createRun, runState] = useCreateImprovementMutation();
	const [createStudy, studyState] = useCreateImprovementStudyMutation();
	const state = isStudy(form) ? studyState : runState;
	const [estimate, est] = useEstimateImprovementMutation();
	const complete = improvementFormProblems(form).length === 0;
	const body = complete ? JSON.stringify(improvementSpec(form)) : "";
	// The estimate shown must be for the current form: a newer change makes it stale until it answers.
	const [asked, setAsked] = useState("");
	useEffect(() => {
		if (!body) return;
		const timer = window.setTimeout(() => {
			setAsked(body);
			void estimate(JSON.parse(body) as ImprovementSpecBody);
		}, ESTIMATE_DELAY_MS);
		return () => window.clearTimeout(timer);
	}, [body, estimate]);
	const current = asked === body && est.originalArgs !== undefined && JSON.stringify(est.originalArgs) === body;
	const estimating = complete && (!current || est.isLoading);
	const agents = splitAgents((agentsQ.data?.agents ?? []) as AgentWithCaps[]).own.map((a) => a.id);
	const suites = suiteChoices(suitesQ.data?.suites ?? []);
	const agentFiles = form.agent && config.data && !config.isFetching ? (config.data.fields.files ?? []) : undefined;
	const submit = async () => {
		// The run spends the organization's model keys: PaidButton asked first (confirm="always").
		try {
			const spec = improvementSpec(form);
			if (spec.replicate) {
				// A study (2 to 5 loops) goes to POST /improvement-studies and opens on the Studies tab.
				const st = await createStudy({ ...spec, replicate: spec.replicate }).unwrap();
				flushSync(() => {
					setSent(true);
					clearDraft();
				});
				onClose();
				onStudy?.(st.id);
				return;
			}
			const run = await createRun(spec).unwrap();
			flushSync(() => {
				setSent(true);
				clearDraft();
			});
			onClose();
			void navigate({ to: "/improvements/$improvementId", params: { improvementId: run.id } });
		} catch {
			// state.error renders the refusal.
		}
	};
	return (
		<Dialog
			open={open}
			onOpenChange={(next) => {
				setOpen(next);
				if (!next) onClose();
			}}
			title={t("create.title")}
			description={t("create.description")}
			closeLabel={tu("improve.close")}
			size="lg"
			testId="new-improvement-dialog"
		>
			<NewImprovementForm
				form={form}
				onChange={setForm}
				agents={agents}
				suites={suites}
				agentFiles={agentFiles}
				model={config.data?.fields.model}
				estimate={current && !est.isError ? est.data : undefined}
				estimating={estimating}
				estimateError={current && est.error ? getBenchErrorMessage(est.error) : undefined}
				busy={state.isLoading}
				error={state.error ? getBenchErrorMessage(state.error) : undefined}
				onSubmit={submit}
				onCancel={onClose}
			/>
		</Dialog>
	);
}