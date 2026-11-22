import { SetupCard, Step } from "@/components/inbox/featureHero";
import { Bird } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { errorMessage, useCreateCanaryMutation, useEditCanaryMutation } from "@/lib/store/apis/schedulesApi";
import { useGetSuitesQuery } from "@/lib/store/apis/benchApi";
import type { CanaryDefinition, SuiteSummary } from "@/lib/types/bench";
import type { CanaryInitRequest } from "@/lib/types/schedules";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Splits "a, b,,c" into ["a","b","c"]. */
export function splitList(v: string): string[] {
	return v
		.split(",")
		.map((x) => x.trim())
		.filter(Boolean);
}

/** Builds the POST /canaries body from the form. Exported for tests. */
export function buildCanaryRequest(f: {
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
}): CanaryInitRequest {
	const tasks = f.tasks.trim() === "all" ? "all" : splitList(f.tasks);
	const req: CanaryInitRequest = {
		name: f.name.trim(),
		agent: f.agent.trim(),
		suite: f.suite.trim(),
		tasks,
		trials: 1,
		targets: splitList(f.targets).map((t) => {
			const i = t.indexOf("/");
			return i > 0 ? { provider: t.slice(0, i), model: t.slice(i + 1) } : { model: t };
		}),
		budget: { per_run_usd: Number(f.perRun) || 0, per_day_usd: Number(f.perDay) || 0 },
	};
	if (f.cron.trim()) req.schedule = { cron: f.cron.trim() };
	if (f.smoke) req.smoke = true;
	if (f.force) req.force = true;
	return req;
}

/** The form fields of an existing definition (the Edit button). Exported for tests. */
export function canaryFormFields(d: CanaryDefinition) {
	return {
		name: d.name,
		agent: d.agent,
		suite: d.suite,
		tasks: d.tasks === "all" ? "all" : d.tasks.join(", "),
		targets: d.targets.map((tg) => (tg.provider ? `${tg.provider}/${tg.model}` : tg.model)).join(", "),
		cron: d.schedule?.cron ?? "",
		perRun: String(d.budget.per_run_usd ?? 0),
		perDay: String(d.budget.per_day_usd ?? 0),
		smoke: Boolean(d.smoke),
		force: false,
	};
}

/**
 * The PUT /canaries/{name} body: the form over the saved definition, so what the form does not show
 * (description, alerts, sensitivity, timezone) is kept. Exported for tests.
 */
export function buildCanaryEdit(initial: CanaryDefinition, f: Parameters<typeof buildCanaryRequest>[0]): CanaryInitRequest {
	const req = buildCanaryRequest({ ...f, name: initial.name, force: false });
	const out: CanaryInitRequest = { ...(initial as CanaryInitRequest), ...req, trials: initial.trials || 1 };
	if (req.schedule) out.schedule = { ...initial.schedule, ...req.schedule };
	else delete out.schedule;
	out.smoke = f.smoke;
	delete out.force;
	return out;
}

/** The benchmark choices: "id@version" of every benchmark of the organization. Exported for tests. */
export function suiteRefs(suites: readonly SuiteSummary[] | undefined): string[] {
	return (suites ?? []).map((s) => (s.version ? `${s.id}@${s.version}` : s.id));
}

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
	const suites = useGetSuitesQuery();
	const [msg, setMsg] = useState<string | null>(null);
	const [f, setF] = useState(
		initial
			? canaryFormFields(initial)
			: {
					name: "",
					agent: "",
					suite: "",
					tasks: "",
					targets: "",
					cron: "0 6 * * *",
					perRun: "1",
					perDay: "3",
					smoke: false,
					force: false,
				},
	);
	const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
		setF({ ...f, [k]: e.target instanceof HTMLInputElement && e.target.type === "checkbox" ? e.target.checked : e.target.value });
	const submit = async () => {
		setMsg(null);
		if (initial) {
			const res = await edit(buildCanaryEdit(initial, f));
			if ("data" in res && res.data) {
				setMsg(t("canary.saved", { name: res.data.name }));
				onCreated?.(res.data.name);
			} else if ("error" in res) setMsg(errorMessage(res.error, "not saved"));
			return;
		}
		const res = await create(buildCanaryRequest(f));
		if ("data" in res && res.data) {
			setMsg(t("canary.created", { name: res.data.name }));
			onCreated?.(res.data.name);
		} else if ("error" in res) setMsg(errorMessage(res.error, "not created"));
	};
	const refs = suiteRefs(suites.data?.suites);
	if (f.suite && !refs.includes(f.suite)) refs.unshift(f.suite);
	const suiteField = (
		<label className="flex flex-col gap-1 text-xs">
			<span className="text-muted-foreground">{t("canary.suite")}</span>
			<select
				className="border-input bg-background h-9 rounded-md border px-2 text-sm"
				value={f.suite}
				onChange={set("suite")}
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
		</label>
	);
	const text = (k: keyof typeof f, label: string, placeholder?: string) => (
		<label className="flex flex-col gap-1 text-xs">
			<span className="text-muted-foreground">{label}</span>
			<Input
				value={String(f[k])}
				onChange={set(k)}
				placeholder={placeholder}
				disabled={k === "name" && Boolean(initial)}
				data-testid={`canary-new-${k}`}
			/>
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
						{text("name", t("canary.name"), "nightly-gpt")}
						{text("agent", t("canary.agent"), t("canary.agentPlaceholder"))}
						{suiteField}
						{text("tasks", t("canary.tasks"), "t1,t2")}
					</div>
				</Step>
				<Step n={2} title={u("canaries.stepWhere")}>
					<div className="grid gap-3 sm:grid-cols-2">{text("targets", t("canary.targets"), "openai/gpt-4o-mini")}</div>
				</Step>
				<Step n={3} title={u("canaries.stepWhen")} last>
					<div className="grid gap-3 sm:grid-cols-3">
						{text("cron", t("canary.cron"))}
						{text("perRun", t("canary.perRun"))}
						{text("perDay", t("canary.perDay"))}
					</div>
					<div className="flex flex-wrap items-center gap-4 text-xs">
						<label className="flex items-center gap-1">
							<input type="checkbox" checked={f.smoke} onChange={set("smoke")} />
							{t("canary.smoke")}
						</label>
						{!initial && (
							<label className="flex items-center gap-1">
								<input type="checkbox" checked={f.force} onChange={set("force")} />
								{t("canary.force")}
							</label>
						)}
					</div>
					<div className="flex flex-wrap items-center gap-3 text-xs">
						<Button
							disabled={state.isLoading || !f.name || !f.suite || !f.targets}
							onClick={() => void submit()}
							data-testid="canary-new-submit"
						>
							{initial ? t("canary.save") : t("canary.create")}
						</Button>
						{!msg && (!f.name || !f.suite || !f.targets) && (
							<span className="text-muted-foreground text-xs" data-testid="canary-new-missing">
								{t("canary.missing")}
							</span>
						)}
						{msg && <span data-testid="canary-new-msg">{msg}</span>}
					</div>
				</Step>
			</div>
		</SetupCard>
	);
}