import { SetupCard, Step } from "@/components/inbox/featureHero";
import { Bird, Plus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ModelPicker } from "@/components/models/modelPicker";
import {
	errorMessage,
	errorProblems,
	useCreateCanaryMutation,
	useEditCanaryMutation,
	type FieldProblem,
} from "@/lib/store/apis/schedulesApi";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { useGetSuiteQuery, useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import type { CanaryDefinition, SuiteSummary } from "@/lib/types/bench";
import type { CanaryInitRequest } from "@/lib/types/schedules";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Splits "a, b,,c" into ["a","b","c"]. */
export function splitList(v: string): string[] {
	return v
		.split(",")
		.map((x) => x.trim())
		.filter(Boolean);
}

/** How often a drift monitor checks: no schedule, three fixed schedules, or a cron line of the user's. */
export type CanaryWhen = "manual" | "daily" | "hourly" | "weekly" | "custom";

/** The choices of the "How often" select, in order. */
export const CANARY_WHEN: readonly CanaryWhen[] = ["manual", "daily", "hourly", "weekly", "custom"];

/** The cron line each fixed choice sends (always in UTC). */
export const CANARY_WHEN_CRON: Readonly<Record<"daily" | "hourly" | "weekly", string>> = {
	daily: "0 6 * * *",
	hourly: "0 * * * *",
	weekly: "0 6 * * 1",
};

/** The most models one drift monitor watches (canary armIDs, A to F). */
export const CANARY_MAX_MODELS = 6;

/** The drift monitor name rule (canary namePattern). */
const CANARY_NAME = /^[a-z0-9][a-z0-9_-]{0,47}$/;

/** One "Models to watch" row; an empty model is a row not picked yet. */
export interface CanaryModelRow {
	provider: string;
	model: string;
}

/** The form's fields. */
export interface CanaryFormFields {
	name: string;
	agent: string;
	suite: string;
	/** True: every task of the benchmark ("all"); false: only taskIds. */
	tasksAll: boolean;
	taskIds: string[];
	targets: CanaryModelRow[];
	when: CanaryWhen;
	/** The custom cron line; used only when when is "custom". */
	cron: string;
	perRun: string;
	perDay: string;
	smoke: boolean;
	force: boolean;
}

/** A new drift monitor: every day at 06:00 UTC, all tasks, one model row to pick. */
export function newCanaryFields(): CanaryFormFields {
	return {
		name: "",
		agent: "",
		suite: "",
		tasksAll: true,
		taskIds: [],
		targets: [{ provider: "", model: "" }],
		when: "daily",
		cron: "",
		perRun: "1",
		perDay: "3",
		smoke: false,
		force: false,
	};
}

/** The cron line the form sends, or null for "Only when I press Check now". Exported for tests. */
export function canaryCron(f: Pick<CanaryFormFields, "when" | "cron">): string | null {
	if (f.when === "manual") return null;
	if (f.when === "custom") return f.cron.trim();
	return CANARY_WHEN_CRON[f.when];
}

/** The choice that shows a saved schedule: a fixed one when its cron matches, else custom. Exported for tests. */
export function canaryWhenOf(schedule: { cron: string; timezone?: string } | undefined): CanaryWhen {
	if (!schedule) return "manual";
	const tz = schedule.timezone ?? "";
	if (tz === "" || tz === "UTC") {
		for (const w of ["daily", "hourly", "weekly"] as const) if (CANARY_WHEN_CRON[w] === schedule.cron.trim()) return w;
	}
	return "custom";
}

/** The text fields of the first form (comma separated tasks and provider/model targets, a cron box). */
export interface CanaryTextFields {
	name: string;
	agent: string;
	suite: string;
	tasks: string;
	targets: string;
	cron: string;
	perRun: string;
	perDay: string;
	smoke: boolean;
	force: boolean;
}

/** The form fields of the text shape: "all" or a comma list of tasks, "provider/model" targets, a blank cron for none. */
export function canaryFieldsFromText(f: CanaryTextFields): CanaryFormFields {
	const cron = f.cron.trim();
	return {
		...f,
		tasksAll: f.tasks.trim() === "all",
		taskIds: f.tasks.trim() === "all" ? [] : splitList(f.tasks),
		targets: splitList(f.targets).map((tg) => {
			const i = tg.indexOf("/");
			return i > 0 ? { provider: tg.slice(0, i), model: tg.slice(i + 1) } : { provider: "", model: tg };
		}),
		when: cron ? "custom" : "manual",
		cron,
	};
}

/** Builds the POST /canaries body from the form (or the older text shape). Exported for tests. */
export function buildCanaryRequest(input: CanaryFormFields | CanaryTextFields): CanaryInitRequest {
	const f = "tasksAll" in input ? input : canaryFieldsFromText(input);
	const req: CanaryInitRequest = {
		name: f.name.trim(),
		agent: f.agent.trim(),
		suite: f.suite.trim(),
		tasks: f.tasksAll ? "all" : [...f.taskIds],
		trials: 1,
		targets: f.targets
			.filter((r) => r.model.trim() !== "")
			.map((r) => (r.provider ? { provider: r.provider, model: r.model.trim() } : { model: r.model.trim() })),
		budget: { per_run_usd: Number(f.perRun) || 0, per_day_usd: Number(f.perDay) || 0 },
	};
	const cron = canaryCron(f);
	if (cron !== null) req.schedule = { cron, timezone: "UTC" };
	if (f.smoke) req.smoke = true;
	if (f.force) req.force = true;
	return req;
}

/** The form fields of an existing definition (the Edit button). Exported for tests. */
export function canaryFormFields(d: CanaryDefinition): CanaryFormFields {
	const when = canaryWhenOf(d.schedule);
	return {
		name: d.name,
		agent: d.agent,
		suite: d.suite,
		tasksAll: d.tasks === "all",
		taskIds: d.tasks === "all" ? [] : [...d.tasks],
		targets: d.targets.length ? d.targets.map((tg) => ({ provider: tg.provider ?? "", model: tg.model })) : [{ provider: "", model: "" }],
		when,
		cron: when === "custom" ? (d.schedule?.cron ?? "") : "",
		perRun: String(d.budget.per_run_usd ?? 0),
		perDay: String(d.budget.per_day_usd ?? 0),
		smoke: Boolean(d.smoke),
		force: false,
	};
}

/**
 * The PUT /canaries/{name} body: the form over the saved definition, so what the form does not show
 * (description, alerts, sensitivity) is kept. Exported for tests.
 */
export function buildCanaryEdit(initial: CanaryDefinition, f: CanaryFormFields): CanaryInitRequest {
	const req = buildCanaryRequest({ ...f, name: initial.name, force: false });
	const out: CanaryInitRequest = { ...(initial as CanaryInitRequest), ...req, trials: initial.trials || 1 };
	if (req.schedule) out.schedule = { ...initial.schedule, ...req.schedule };
	else delete out.schedule;
	out.smoke = f.smoke;
	delete out.force;
	return out;
}

/** The benchmark choices: "id@version" of every benchmark of the organization. Exported for tests. */
export function suiteRefs(suites: readonly Pick<SuiteSummary, "id" | "version">[] | undefined): string[] {
	return (suites ?? []).map((s) => (s.version ? `${s.id}@${s.version}` : s.id));
}

/** The task ids of the picked benchmark ("id" or "id@version") in a list of full entries. Exported for tests. */
export function suiteTaskIds(suites: readonly SuiteSummary[] | undefined, ref: string): string[] {
	const [id, version] = ref.split("@");
	const s = (suites ?? []).find((x) => x.id === id && (!version || x.version === version));
	return (s?.tasks ?? []).map((tk) => tk.id);
}

/** The schedules keys of the form's own problems. */
export type CanaryProblemKey = `canary.problem.${"name" | "agent" | "suite" | "tasks" | "targets" | "cron" | "budget" | "budgetOrder"}`;

/** Field keys of the form that a problem can sit under. */
export type CanaryField = "name" | "agent" | "suite" | "tasks" | "targets" | "schedule" | "budget";

/**
 * Everything the form still needs before Create, each under its field, as keys of the schedules
 * namespace (canary.problem.*). Every missing field is listed at once. Exported for tests.
 */
export function canaryFormProblems(
	f: CanaryFormFields,
	opts: { fixedName?: boolean } = {},
): { field: CanaryField; key: CanaryProblemKey }[] {
	const out: { field: CanaryField; key: CanaryProblemKey }[] = [];
	if (!opts.fixedName && !CANARY_NAME.test(f.name.trim())) out.push({ field: "name", key: "canary.problem.name" });
	if (!f.agent.trim()) out.push({ field: "agent", key: "canary.problem.agent" });
	if (!f.suite.trim()) out.push({ field: "suite", key: "canary.problem.suite" });
	if (!f.tasksAll && f.taskIds.length === 0) out.push({ field: "tasks", key: "canary.problem.tasks" });
	if (!f.targets.some((r) => r.model.trim() !== "")) out.push({ field: "targets", key: "canary.problem.targets" });
	if (f.when === "custom" && !f.cron.trim()) out.push({ field: "schedule", key: "canary.problem.cron" });
	if (!f.smoke) {
		const run = Number(f.perRun);
		const day = Number(f.perDay);
		if (!(run > 0) || !(day > 0)) out.push({ field: "budget", key: "canary.problem.budget" });
		else if (run > day) out.push({ field: "budget", key: "canary.problem.budgetOrder" });
	}
	return out;
}

const selectClass = "border-input bg-background h-9 rounded-md border px-2 text-sm";

/** The largest number of task checkboxes shown at once; the filter narrows a bigger benchmark. */
const TASKS_SHOWN = 200;

/**
 * New drift canary: writes canaries/<name>.yaml through POST /canaries. With initial it edits that
 * drift monitor (PUT /canaries/{name}); the name is fixed.
 */
export function NewCanaryForm({
	onCreated,
	onClose,
	initial,
}: {
	onCreated?(name: string): void;
	onClose?(): void;
	initial?: CanaryDefinition;
}) {
	const { t } = useTranslation("schedules");
	const { t: u } = useTranslation("ux13");
	const [create, createState] = useCreateCanaryMutation();
	const [edit, editState] = useEditCanaryMutation();
	const state = initial ? editState : createState;
	const suites = useGetSuitesLightQuery();
	const agents = useGetAgentsQuery();
	const [msg, setMsg] = useState<string | null>(null);
	const [serverProblems, setServerProblems] = useState<FieldProblem[]>([]);
	const [taskFilter, setTaskFilter] = useState("");
	const [f, setF] = useState<CanaryFormFields>(() => (initial ? canaryFormFields(initial) : newCanaryFields()));
	// The task list needs the picked benchmark's full entry (the light list carries no tasks): one
	// benchmark from GET /suites/{id} (N194), only fetched once the user asks to pick some tasks.
	const full = useGetSuiteQuery(f.suite.split("@")[0], { skip: f.tasksAll || !f.suite });
	const patch = (p: Partial<CanaryFormFields>) => setF((prev) => ({ ...prev, ...p }));
	const set =
		(k: "name" | "agent" | "suite" | "cron" | "perRun" | "perDay") => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
			patch({ [k]: e.target.value } as Partial<CanaryFormFields>);
	const flag = (k: "smoke" | "force") => (e: React.ChangeEvent<HTMLInputElement>) =>
		patch({ [k]: e.target.checked } as Partial<CanaryFormFields>);
	const problems = canaryFormProblems(f, { fixedName: Boolean(initial) });
	const whenWords = () => {
		const cron = canaryCron(f);
		return cron === null
			? t("canary.whenWords.manual")
			: f.when === "custom"
				? t("canary.whenWords.custom", { cron })
				: t(`canary.whenWords.${f.when}`);
	};
	const submit = async () => {
		setMsg(null);
		setServerProblems([]);
		const fail = (err: unknown, fallback: string) => {
			const ps = errorProblems(err);
			setServerProblems(ps);
			setMsg(ps.length ? null : errorMessage(err, fallback));
		};
		if (initial) {
			const res = await edit(buildCanaryEdit(initial, f));
			if ("data" in res && res.data) {
				setMsg(t("canary.saved", { name: res.data.name }));
				onCreated?.(res.data.name);
			} else if ("error" in res) fail(res.error, "not saved");
			return;
		}
		const res = await create(buildCanaryRequest(f));
		if ("data" in res && res.data) {
			setMsg(t("canary.createdWhen", { name: res.data.name, when: whenWords() }));
			onCreated?.(res.data.name);
		} else if ("error" in res) fail(res.error, "not created");
	};
	// Server problems under their field; the ones without a field of the form go under the button.
	const formFields: readonly string[] = ["name", "agent", "suite", "tasks", "targets", "schedule", "budget"];
	const under = (field: CanaryField) => {
		const lines = serverProblems.filter((p) => p.field === field).map((p) => p.message);
		if (lines.length === 0) return null;
		return (
			<span className="text-chart-error-ink text-xs" data-testid={`canary-new-problem-${field}`}>
				{lines.map((l) => (
					<span key={l} className="block">
						{l}
					</span>
				))}
			</span>
		);
	};
	const otherProblems = serverProblems.filter((p) => !formFields.includes(p.field));

	const refs = suiteRefs(suites.data?.suites);
	if (f.suite && !refs.includes(f.suite)) refs.unshift(f.suite);
	const agentList = agents.data?.agents ?? [];
	const agentPaths = agentList.map((a) => a.path);
	const allTaskIds = suiteTaskIds(full.currentData ? [full.currentData] : undefined, f.suite);
	for (const id of f.taskIds) if (!allTaskIds.includes(id)) allTaskIds.unshift(id);
	const filter = taskFilter.trim().toLowerCase();
	const matching = filter ? allTaskIds.filter((id) => id.toLowerCase().includes(filter)) : allTaskIds;
	const shown = matching.slice(0, TASKS_SHOWN);

	const nameField = (
		<label className="flex flex-col gap-1 text-xs">
			<span className="text-muted-foreground">{t("canary.name")}</span>
			<Input value={f.name} onChange={set("name")} placeholder="nightly-gpt" disabled={Boolean(initial)} data-testid="canary-new-name" />
			{under("name")}
		</label>
	);
	const agentField = (
		<label className="flex flex-col gap-1 text-xs">
			<span className="text-muted-foreground">{t("canary.agent")}</span>
			<select className={selectClass} value={f.agent} onChange={set("agent")} data-testid="canary-new-agent">
				<option value="">{agents.isLoading ? t("canary.agentsLoading") : t("canary.agentPick")}</option>
				{f.agent && !agentPaths.includes(f.agent) && <option value={f.agent}>{f.agent}</option>}
				{agentList.map((a) => (
					<option key={a.path} value={a.path}>
						{a.id}
					</option>
				))}
			</select>
			{agents.isSuccess && agentList.length === 0 && (
				<Link to="/agents" className="text-xs underline" data-testid="canary-new-no-agents">
					{t("canary.agentsEmpty")}
				</Link>
			)}
			{under("agent")}
		</label>
	);
	const suiteField = (
		<label className="flex flex-col gap-1 text-xs">
			<span className="text-muted-foreground">{t("canary.suite")}</span>
			<select
				className={selectClass}
				value={f.suite}
				onChange={(e) => patch({ suite: e.target.value, taskIds: [] })}
				data-testid="canary-new-suite"
			>
				<option value="">{suites.isLoading ? t("canary.suitesLoading") : t("canary.suitePick")}</option>
				{refs.map((r) => (
					<option key={r} value={r}>
						{r}
					</option>
				))}
			</select>
			{!suites.isLoading && refs.length === 0 && (
				<span className="text-muted-foreground" data-testid="canary-new-no-suites">
					{t("canary.suitesEmpty")}
				</span>
			)}
			{under("suite")}
		</label>
	);
	const tasksField = (
		<div className="flex flex-col gap-1 text-xs" role="group" aria-label={t("canary.tasks")}>
			<span className="text-muted-foreground">{t("canary.tasks")}</span>
			<select
				className={selectClass}
				value={f.tasksAll ? "all" : "some"}
				onChange={(e) => patch({ tasksAll: e.target.value === "all" })}
				data-testid="canary-new-tasks"
			>
				<option value="all">{t("canary.tasksAll")}</option>
				<option value="some">{t("canary.tasksSome")}</option>
			</select>
			{!f.tasksAll &&
				(!f.suite ? (
					<span className="text-muted-foreground">{t("canary.tasksPickSuite")}</span>
				) : full.isLoading || (full.isFetching && !full.currentData) ? (
					<span className="text-muted-foreground">{t("canary.tasksLoading")}</span>
				) : allTaskIds.length === 0 ? (
					<span className="text-muted-foreground">{t("canary.tasksNone")}</span>
				) : (
					<div className="flex flex-col gap-1" data-testid="canary-new-task-list">
						{allTaskIds.length > 10 && (
							<Input
								value={taskFilter}
								onChange={(e) => setTaskFilter(e.target.value)}
								placeholder={t("canary.tasksFilter")}
								aria-label={t("canary.tasksFilter")}
							/>
						)}
						<div className="border-input flex max-h-40 flex-col gap-0.5 overflow-auto rounded-md border p-2">
							{shown.map((id) => (
								<label key={id} className="flex items-center gap-1">
									<input
										type="checkbox"
										checked={f.taskIds.includes(id)}
										onChange={(e) => patch({ taskIds: e.target.checked ? [...f.taskIds, id] : f.taskIds.filter((x) => x !== id) })}
									/>
									<span className="font-mono">{id}</span>
								</label>
							))}
						</div>
						<span className="text-muted-foreground">
							{t("canary.tasksPicked", { count: f.taskIds.length })}
							{matching.length > shown.length && ` ${t("canary.tasksMore", { shown: shown.length, total: matching.length })}`}
						</span>
					</div>
				))}
			{under("tasks")}
		</div>
	);
	const setRow = (i: number, row: CanaryModelRow) => patch({ targets: f.targets.map((r, j) => (j === i ? row : r)) });
	const modelsField = (
		<div className="flex flex-col gap-2 text-xs" role="group" aria-label={t("canary.targets")} data-testid="canary-new-targets">
			<span className="text-muted-foreground">{t("canary.targets")}</span>
			{f.targets.map((row, i) => (
				<div key={i} className="flex items-end gap-2" data-testid={`canary-new-target-${i}`}>
					<div className="min-w-0 flex-1">
						<ModelPicker
							value={row.provider || row.model ? { provider: row.provider, model: row.model } : null}
							onChange={(v) => setRow(i, { provider: v.provider, model: v.model })}
							testId={`canary-new-model-${i}`}
						/>
					</div>
					{f.targets.length > 1 && (
						<Button
							size="sm"
							variant="ghost"
							aria-label={t("canary.removeModel")}
							onClick={() => patch({ targets: f.targets.filter((_, j) => j !== i) })}
							data-testid={`canary-new-remove-${i}`}
						>
							<X className="size-4" aria-hidden="true" />
						</Button>
					)}
				</div>
			))}
			{f.targets.length < CANARY_MAX_MODELS ? (
				<Button
					size="sm"
					variant="outline"
					className="self-start"
					onClick={() => patch({ targets: [...f.targets, { provider: "", model: "" }] })}
					data-testid="canary-new-add-model"
				>
					<Plus className="size-4" aria-hidden="true" />
					{t("canary.addModel")}
				</Button>
			) : (
				<span className="text-muted-foreground">{t("canary.maxModels", { n: CANARY_MAX_MODELS })}</span>
			)}
			{under("targets")}
		</div>
	);
	const whenField = (
		<label className="flex flex-col gap-1 text-xs">
			<span className="text-muted-foreground">{t("canary.when")}</span>
			<select
				className={selectClass}
				value={f.when}
				onChange={(e) => patch({ when: e.target.value as CanaryWhen })}
				data-testid="canary-new-when"
			>
				{CANARY_WHEN.map((w) => (
					<option key={w} value={w}>
						{t(`canary.whenOption.${w}`)}
					</option>
				))}
			</select>
			{f.when === "custom" && (
				<Input
					value={f.cron}
					onChange={set("cron")}
					placeholder="0 6 * * *"
					aria-label={t("canary.cronCustom")}
					data-testid="canary-new-cron"
				/>
			)}
			{f.when !== "manual" && <span className="text-muted-foreground">{t("canary.utc")}</span>}
			{under("schedule")}
		</label>
	);
	const moneyField = (k: "perRun" | "perDay", label: string) => (
		<label className="flex flex-col gap-1 text-xs">
			<span className="text-muted-foreground">{label}</span>
			<Input value={f[k]} onChange={set(k)} inputMode="decimal" data-testid={`canary-new-${k}`} />
		</label>
	);
	return (
		<SetupCard
			icon={Bird}
			title={initial ? t("canary.editTitle", { name: initial.name }) : t("canary.newTitle")}
			testId="canary-new"
			actions={
				onClose && (
					<Button size="sm" variant="ghost" onClick={onClose} data-testid="canary-new-close">
						{u("canaries.close")}
					</Button>
				)
			}
		>
			<div className="flex flex-col">
				<Step n={1} title={u("canaries.stepWhat")}>
					<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
						{nameField}
						{agentField}
						{suiteField}
						{tasksField}
					</div>
				</Step>
				<Step n={2} title={u("canaries.stepWhere")}>
					<div className="grid gap-3 sm:grid-cols-2">{modelsField}</div>
				</Step>
				<Step n={3} title={u("canaries.stepWhen")} last>
					<div className="grid gap-3 sm:grid-cols-3">
						{whenField}
						{moneyField("perRun", t("canary.perRun"))}
						{moneyField("perDay", t("canary.perDay"))}
					</div>
					{under("budget")}
					<div className="flex flex-wrap items-center gap-4 text-xs">
						<label className="flex items-center gap-1">
							<input type="checkbox" checked={f.smoke} onChange={flag("smoke")} />
							{t("canary.smoke")}
						</label>
						{!initial && (
							<label className="flex items-center gap-1">
								<input type="checkbox" checked={f.force} onChange={flag("force")} />
								{t("canary.force")}
							</label>
						)}
					</div>
					<div className="flex flex-wrap items-start gap-3 text-xs">
						<Button disabled={state.isLoading || problems.length > 0} onClick={() => void submit()} data-testid="canary-new-submit">
							{initial ? t("canary.save") : t("canary.create")}
						</Button>
						{problems.length > 0 && (
							<div className="text-muted-foreground flex flex-col gap-0.5 text-xs" data-testid="canary-new-missing">
								<span>{t("canary.missing")}</span>
								<ul className="list-disc pl-4">
									{problems.map((p) => (
										<li key={p.key} data-testid={`canary-new-missing-${p.field}`}>
											{t(p.key)}
										</li>
									))}
								</ul>
							</div>
						)}
						{msg && <span data-testid="canary-new-msg">{msg}</span>}
						{otherProblems.length > 0 && (
							<ul className="text-chart-error-ink text-xs" data-testid="canary-new-problems">
								{otherProblems.map((p) => (
									<li key={p.field + p.message}>{p.message}</li>
								))}
							</ul>
						)}
					</div>
				</Step>
			</div>
		</SetupCard>
	);
}