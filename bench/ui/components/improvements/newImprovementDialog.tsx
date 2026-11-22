import { type AgentWithCaps, splitAgents } from "@/app/evaluate/views/evaluate.utils";
import { Button } from "@/components/ui/button";
import { DetailsDisclosure } from "@/components/ui/detailsDisclosure";
import { Input } from "@/components/ui/input";
import { useGetAgentConfigQuery } from "@/lib/store/apis/agentBuilderApi";
import { getBenchErrorMessage, useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import { type ImprovementSpecBody, useCreateImprovementMutation } from "@/lib/store/apis/improveApi";
import { useNavigate } from "@tanstack/react-router";
import { Check, Copy } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";

/** The improvement name rule the server enforces (improve/spec.go nameRe). */
export const IMPROVEMENT_NAME = /^[a-z0-9][a-z0-9-]{0,62}$/;
/** Rounds the server accepts (improve.MaxRounds) and its default. */
export const MAX_IMPROVEMENT_ROUNDS = 20;
export const DEFAULT_IMPROVEMENT_ROUNDS = 3;
/** The improver's default spend cap per round, in dollars (improve.DefaultBudgetUSD). */
export const DEFAULT_ROUND_BUDGET = "2.00";

/** What the form holds. Rounds and budget stay text while typed. */
export interface ImprovementForm {
	name: string;
	agent: string;
	/** <suite id>@<version> */
	suite: string;
	files: string[];
	rounds: string;
	budget: string;
}

export type ImprovementFormProblem = "name" | "agent" | "suite" | "noFiles" | "files" | "rounds" | "budget";

/** Every reason the form cannot be sent yet, in field order. */
export function improvementFormProblems(f: ImprovementForm, agentFiles: readonly string[] | undefined): ImprovementFormProblem[] {
	const out: ImprovementFormProblem[] = [];
	if (!IMPROVEMENT_NAME.test(f.name)) out.push("name");
	if (!f.agent) out.push("agent");
	if (!f.suite) out.push("suite");
	if (f.agent && agentFiles && agentFiles.length === 0) out.push("noFiles");
	else if (f.files.length === 0) out.push("files");
	const rounds = Number(f.rounds);
	if (!Number.isInteger(rounds) || rounds < 1 || rounds > MAX_IMPROVEMENT_ROUNDS) out.push("rounds");
	const budget = Number(f.budget);
	if (f.budget.trim() === "" || !Number.isFinite(budget) || budget <= 0) out.push("budget");
	return out;
}

/** The POST /improvements body: a path ending in "/" is a folder the improver may change (editable.dirs). */
export function improvementSpec(f: ImprovementForm): ImprovementSpecBody {
	const files = f.files.filter((p) => !p.endsWith("/"));
	const dirs = f.files.filter((p) => p.endsWith("/"));
	return {
		kind: "improvement",
		name: f.name,
		agent: `agents/${f.agent}.yaml`,
		suite: f.suite,
		rounds: Number(f.rounds),
		editable: dirs.length > 0 ? { files, dirs } : { files },
		improver: { budget_usd: Number(f.budget) },
	};
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
	busy?: boolean;
	/** The server's refusal, shown under the form. */
	error?: string;
	onSubmit(): void;
	onCancel(): void;
}

/** The dialog's form, without data fetching. Exported for tests. */
export function NewImprovementForm({
	form,
	onChange,
	agents,
	suites,
	agentFiles,
	busy,
	error,
	onSubmit,
	onCancel,
}: NewImprovementFormProps) {
	const { t } = useTranslation("improve");
	const ids = useId();
	const problems = improvementFormProblems(form, agentFiles);
	const touched = (v: string) => v.length > 0;
	const set = (patch: Partial<ImprovementForm>) => onChange({ ...form, ...patch });
	const toggle = (path: string, on: boolean) => set({ files: on ? [...form.files, path].sort() : form.files.filter((p) => p !== path) });
	const selectClass = "border-input bg-card h-9 rounded-md border px-2 text-sm";
	return (
		<form
			role="dialog"
			aria-modal="true"
			aria-labelledby={`${ids}-title`}
			className="bg-card text-card-foreground flex max-h-[90vh] w-full max-w-lg flex-col gap-4 overflow-y-auto rounded-lg border p-5 text-left shadow-lg"
			data-testid="new-improvement-dialog"
			onSubmit={(e) => {
				e.preventDefault();
				if (problems.length === 0 && !busy) onSubmit();
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
				{touched(form.name) && problems.includes("name") && (
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
			{form.agent && (
				<fieldset className="flex flex-col gap-1 text-xs" data-testid="new-improvement-files">
					<legend className="text-foreground mb-1 font-medium">{t("create.files")}</legend>
					{agentFiles === undefined ? (
						<span className="text-muted-foreground">{t("create.filesLoading")}</span>
					) : agentFiles.length === 0 ? (
						<span className="text-muted-foreground" data-testid="new-improvement-no-files">
							{t("create.noFiles")}
						</span>
					) : (
						agentFiles.map((p) => (
							<label key={p} className="flex items-center gap-2">
								<input
									type="checkbox"
									checked={form.files.includes(p)}
									onChange={(e) => toggle(p, e.target.checked)}
									data-testid="new-improvement-file"
								/>
								<code className="font-mono">{p}</code>
								{p.endsWith("/") && <span className="text-muted-foreground">{t("create.folder")}</span>}
							</label>
						))
					)}
				</fieldset>
			)}
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
				<label className="flex w-48 flex-col gap-1 text-xs">
					<span className="text-foreground font-medium">{t("create.budget")}</span>
					<Input
						type="number"
						min={0.01}
						step={0.01}
						value={form.budget}
						onChange={(e) => set({ budget: e.target.value })}
						data-testid="new-improvement-budget"
					/>
				</label>
			</div>
			{problems.includes("rounds") && <span className="text-chart-error-ink text-xs">{t("create.roundsError")}</span>}
			{problems.includes("budget") && <span className="text-chart-error-ink text-xs">{t("create.budgetError")}</span>}
			<p className="text-muted-foreground text-xs">{t("create.roundNote")}</p>
			{error && (
				<p className="text-chart-error-ink text-sm" role="alert" data-testid="new-improvement-error">
					{error}
				</p>
			)}
			<div className="flex justify-end gap-2">
				<Button type="button" variant="ghost" onClick={onCancel} data-testid="new-improvement-cancel">
					{t("create.cancel")}
				</Button>
				<Button type="submit" disabled={problems.length > 0 || busy} data-testid="new-improvement-submit">
					{t("create.submit")}
				</Button>
			</div>
			<ImprovementCli testId="new-improvement-cli" />
		</form>
	);
}

const EMPTY_FORM: ImprovementForm = {
	name: "",
	agent: "",
	suite: "",
	files: [],
	rounds: String(DEFAULT_IMPROVEMENT_ROUNDS),
	budget: DEFAULT_ROUND_BUDGET,
};

/**
 * New improvement run (admins): name, one of the org's own agents, a benchmark, the agent files the
 * improver may change, rounds and the improver's budget per round. Create posts the spec; on 202 the
 * page opens the new run, on 422 the server's text shows under the form.
 */
export function NewImprovementDialog({ onClose }: { onClose(): void }) {
	const navigate = useNavigate();
	const [form, setForm] = useState<ImprovementForm>(EMPTY_FORM);
	const agentsQ = useGetAgentsQuery();
	// The picker needs only each benchmark id and version: the light list never waits for a pack load.
	const suitesQ = useGetSuitesLightQuery();
	const config = useGetAgentConfigQuery(form.agent, { skip: !form.agent });
	const [create, state] = useCreateImprovementMutation();
	useEffect(() => {
		const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [onClose]);
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
				busy={state.isLoading}
				error={state.error ? getBenchErrorMessage(state.error) : undefined}
				onSubmit={() => void submit()}
				onCancel={onClose}
			/>
		</div>
	);
}