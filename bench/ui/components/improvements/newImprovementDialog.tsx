import { type AgentWithCaps, splitAgents } from "@/app/evaluate/views/evaluate.utils";
import { Button } from "@/components/ui/button";
import { DetailsDisclosure } from "@/components/ui/detailsDisclosure";
import { Input } from "@/components/ui/input";
import { useGetAgentConfigQuery } from "@/lib/store/apis/agentBuilderApi";
import { getBenchErrorMessage, useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import { type ImprovementSpecBody, useCreateImprovementMutation, useEstimateImprovementMutation } from "@/lib/store/apis/improveApi";
import i18n from "@/lib/i18n";
import type { ImprovementEstimate } from "@/lib/types/improve";
import { useNavigate } from "@tanstack/react-router";
import { Check, Copy } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { EstimateCard, estimateBlock } from "./estimateCard";
import { PRESETS, type PresetName, PresetPicker } from "./presetPicker";
import "./w45impI18n";

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
	/** Custom size only. */
	rounds: string;
	ideas: string;
	/** The whole run's spending limit in dollars, prefilled from the preset. */
	limit: string;
}

/** An empty form: the small size, standing instructions ticked. */
export function newForm(): ImprovementForm {
	const p = PRESETS.small;
	return {
		name: "",
		agent: "",
		suite: "",
		instructions: true,
		files: [],
		preset: "small",
		rounds: String(p.rounds),
		ideas: String(p.ideas),
		limit: String(p.limit),
	};
}

export type ImprovementFormProblem = "name" | "agent" | "suite" | "surface" | "rounds" | "ideas" | "limit";

/** Every reason the form cannot be sent yet, in field order. */
export function improvementFormProblems(f: ImprovementForm): ImprovementFormProblem[] {
	const out: ImprovementFormProblem[] = [];
	if (!IMPROVEMENT_NAME.test(f.name)) out.push("name");
	if (!f.agent) out.push("agent");
	if (!f.suite) out.push("suite");
	if (!f.instructions && f.files.length === 0) out.push("surface");
	if (f.preset === "custom") {
		const rounds = Number(f.rounds);
		if (!Number.isInteger(rounds) || rounds < 1 || rounds > MAX_IMPROVEMENT_ROUNDS) out.push("rounds");
		const ideas = Number(f.ideas);
		if (!Number.isInteger(ideas) || ideas < 1 || ideas > MAX_IDEAS) out.push("ideas");
	}
	const limit = Number(f.limit);
	if (f.limit.trim() === "" || !Number.isFinite(limit) || limit <= 0) out.push("limit");
	return out;
}

/**
 * The POST /improvements body (A1.8): the preset, the default improver task, what may change (a path
 * ending in "/" is a folder, editable.dirs) and the spending limit. Rounds and ideas per round are
 * sent only for a custom size; a named preset fills them on the server.
 */
export function improvementSpec(f: ImprovementForm): ImprovementSpecBody {
	const files = f.files.filter((p) => !p.endsWith("/"));
	const dirs = f.files.filter((p) => p.endsWith("/"));
	const custom = f.preset === "custom";
	const body: ImprovementSpecBody = {
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
	return body;
}

/**
 * Why Create is disabled, or undefined when it may be pressed: the form's first problem, then a
 * pending estimate, then the estimate's first refusal (A2.9 item 8).
 */
export function createBlock(f: ImprovementForm, estimate: ImprovementEstimate | undefined, estimating: boolean): string | undefined {
	const first = improvementFormProblems(f)[0];
	if (first) return i18n.t(`reason.${first}`, { ns: "w45imp" });
	if (estimating) return i18n.t("reason.estimating", { ns: "w45imp" });
	return estimateBlock(estimate);
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
	/** Benchmarks as <id>@<version>. */
	suites: readonly string[];
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
	onSubmit(): void;
	onCancel(): void;
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
	const ids = useId();
	const problems = improvementFormProblems(form);
	const block = createBlock(form, estimate, estimating);
	const set = (patch: Partial<ImprovementForm>) => onChange({ ...form, ...patch });
	const toggle = (path: string, on: boolean) => set({ files: on ? [...form.files, path].sort() : form.files.filter((p) => p !== path) });
	const pickPreset = (preset: PresetName) => set({ preset, limit: String(PRESETS[preset].limit) });
	const selectClass = "border-input bg-card h-9 rounded-md border px-2 text-sm";
	return (
		<form
			role="dialog"
			aria-modal="true"
			aria-labelledby={`${ids}-title`}
			className="bg-card text-card-foreground flex max-h-[90vh] w-full max-w-xl flex-col gap-4 overflow-y-auto rounded-lg border p-5 text-left shadow-lg"
			data-testid="new-improvement-dialog"
			onSubmit={(e) => {
				e.preventDefault();
				if (!block && !busy) onSubmit();
			}}
		>
			<div>
				<h2 id={`${ids}-title`} className="text-lg font-semibold">
					{t("create.title")}
				</h2>
				<p className="text-muted-foreground text-sm">{t("create.description")}</p>
			</div>
			<label className="flex flex-col gap-1 text-xs">
				<span className="text-foreground font-medium">{t("create.name")}</span>
				<Input
					value={form.name}
					onChange={(e) => set({ name: e.target.value })}
					maxLength={63}
					autoFocus
					data-testid="new-improvement-name"
				/>
				{form.name.length > 0 && problems.includes("name") && (
					<span className="text-chart-error-ink" data-testid="new-improvement-name-error">
						{t("create.nameError")}
					</span>
				)}
			</label>
			<label className="flex flex-col gap-1 text-xs">
				<span className="text-foreground font-medium">{t("create.agent")}</span>
				{agents.length === 0 ? (
					<span className="text-muted-foreground" data-testid="new-improvement-no-agents">
						{t("create.noAgents")}
					</span>
				) : (
					<select
						className={selectClass}
						value={form.agent}
						onChange={(e) => set({ agent: e.target.value, files: [] })}
						data-testid="new-improvement-agent"
					>
						<option value="">{t("create.agentPick")}</option>
						{agents.map((a) => (
							<option key={a} value={a}>
								{a}
							</option>
						))}
					</select>
				)}
			</label>
			<label className="flex flex-col gap-1 text-xs">
				<span className="text-foreground font-medium">{t("create.benchmark")}</span>
				<select
					className={selectClass}
					value={form.suite}
					onChange={(e) => set({ suite: e.target.value })}
					data-testid="new-improvement-suite"
				>
					<option value="">{t("create.benchmarkPick")}</option>
					{suites.map((s) => (
						<option key={s} value={s}>
							{s}
						</option>
					))}
				</select>
			</label>
			<fieldset className="flex flex-col gap-1 text-xs" data-testid="new-improvement-files">
				<legend className="text-foreground mb-1 font-medium">{tw("form.surfaces")}</legend>
				<label className="flex items-center gap-2">
					<input
						type="checkbox"
						checked={form.instructions}
						onChange={(e) => set({ instructions: e.target.checked })}
						data-testid="new-improvement-instructions"
					/>
					<span>{tw("form.instructions")}</span>
				</label>
				{form.agent && agentFiles === undefined && <span className="text-muted-foreground">{t("create.filesLoading")}</span>}
				{(agentFiles ?? []).map((p) => (
					<label key={p} className="flex items-center gap-2">
						<input
							type="checkbox"
							checked={form.files.includes(p)}
							onChange={(e) => toggle(p, e.target.checked)}
							data-testid="new-improvement-file"
						/>
						<code className="font-mono">{p}</code>
						{p.endsWith("/") && <span className="text-muted-foreground">{tw("form.folder")}</span>}
					</label>
				))}
				<span className="text-muted-foreground">{tw("form.fixed")}</span>
				{problems.includes("surface") && <span className="text-chart-error-ink">{tw("form.surfaceNone")}</span>}
			</fieldset>
			<PresetPicker value={form.preset} onChange={pickPreset} />
			{form.preset === "custom" && (
				<div className="flex flex-wrap gap-3">
					<label className="flex w-28 flex-col gap-1 text-xs">
						<span className="text-foreground font-medium">{t("create.rounds")}</span>
						<Input
							type="number"
							min={1}
							max={MAX_IMPROVEMENT_ROUNDS}
							step={1}
							value={form.rounds}
							onChange={(e) => set({ rounds: e.target.value })}
							data-testid="new-improvement-rounds"
						/>
					</label>
					<label className="flex w-36 flex-col gap-1 text-xs">
						<span className="text-foreground font-medium">{tw("form.ideas")}</span>
						<Input
							type="number"
							min={1}
							max={MAX_IDEAS}
							step={1}
							value={form.ideas}
							onChange={(e) => set({ ideas: e.target.value })}
							data-testid="new-improvement-ideas"
						/>
					</label>
				</div>
			)}
			{problems.includes("rounds") && <span className="text-chart-error-ink text-xs">{t("create.roundsError")}</span>}
			{problems.includes("ideas") && <span className="text-chart-error-ink text-xs">{tw("form.ideasError")}</span>}
			<label className="flex w-64 flex-col gap-1 text-xs">
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
			<EstimateCard estimate={estimate} loading={estimating} error={estimateError} model={model} waiting={problems.length > 0} />
			{error && (
				<p className="text-chart-error-ink text-sm" role="alert" data-testid="new-improvement-error">
					{error}
				</p>
			)}
			<div className="flex flex-wrap items-center justify-end gap-2">
				{block && (
					<span className="text-muted-foreground mr-auto text-xs" data-testid="new-improvement-reason">
						{block}
					</span>
				)}
				<Button type="button" variant="ghost" onClick={onCancel} data-testid="new-improvement-cancel">
					{t("create.cancel")}
				</Button>
				<Button type="submit" disabled={block !== undefined || busy} title={block} data-testid="new-improvement-submit">
					{t("create.submit")}
				</Button>
			</div>
			<ImprovementCli testId="new-improvement-cli" />
		</form>
	);
}

/**
 * New improvement run (admins): name, one of the org's own agents, a benchmark, what may change
 * (standing instructions first, then the agent's files), a size and the spending limit. Each change
 * asks for a new estimate after a short pause; Create posts the spec and opens the new run, and a
 * refusal shows under the form.
 */
export function NewImprovementDialog({ onClose }: { onClose(): void }) {
	const navigate = useNavigate();
	const [form, setForm] = useState<ImprovementForm>(newForm);
	const agentsQ = useGetAgentsQuery();
	// The picker needs only each benchmark id and version: the light list never waits for a pack load.
	const suitesQ = useGetSuitesLightQuery();
	const config = useGetAgentConfigQuery(form.agent, { skip: !form.agent });
	const [create, state] = useCreateImprovementMutation();
	const [estimate, est] = useEstimateImprovementMutation();
	useEffect(() => {
		const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [onClose]);
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
	const suites = (suitesQ.data?.suites ?? []).map((s) => `${s.id}@${s.version}`).sort();
	const agentFiles = form.agent && config.data && !config.isFetching ? (config.data.fields.files ?? []) : undefined;
	const submit = async () => {
		try {
			const run = await create(improvementSpec(form)).unwrap();
			onClose();
			void navigate({ to: "/improvements/$improvementId", params: { improvementId: run.id } });
		} catch {
			// state.error renders the refusal.
		}
	};
	return (
		<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" data-testid="new-improvement-backdrop">
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
				onSubmit={() => void submit()}
				onCancel={onClose}
			/>
		</div>
	);
}