import { SetupCard, Step } from "@/components/inbox/featureHero";
import { Bird } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { errorMessage, useCreateCanaryMutation } from "@/lib/store/apis/schedulesApi";
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

/** New drift canary: writes canaries/<name>.yaml through POST /canaries. */
export function NewCanaryForm({ onCreated, onClose }: { onCreated?(name: string): void; onClose?(): void }) {
	const { t } = useTranslation("schedules");
	const { t: u } = useTranslation("ux13");
	const [create, state] = useCreateCanaryMutation();
	const [msg, setMsg] = useState<string | null>(null);
	const [f, setF] = useState({
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
	});
	const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) =>
		setF({ ...f, [k]: e.target.type === "checkbox" ? e.target.checked : e.target.value });
	const submit = async () => {
		setMsg(null);
		const res = await create(buildCanaryRequest(f));
		if ("data" in res && res.data) {
			setMsg(t("canary.created", { name: res.data.name }));
			onCreated?.(res.data.name);
		} else if ("error" in res) setMsg(errorMessage(res.error, "not created"));
	};
	const text = (k: keyof typeof f, label: string, placeholder?: string) => (
		<label className="flex flex-col gap-1 text-xs">
			<span className="text-muted-foreground">{label}</span>
			<Input value={String(f[k])} onChange={set(k)} placeholder={placeholder} data-testid={`canary-new-${k}`} />
		</label>
	);
	return (
		<SetupCard
			icon={Bird}
			title={t("canary.newTitle")}
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
						{text("suite", t("canary.suite"), "coding-core@1")}
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
						<label className="flex items-center gap-1">
							<input type="checkbox" checked={f.force} onChange={set("force")} />
							{t("canary.force")}
						</label>
					</div>
					<div className="flex flex-wrap items-center gap-3 text-xs">
						<Button disabled={state.isLoading || !f.name || !f.suite || !f.targets} onClick={() => void submit()} data-testid="canary-new-submit">
							{t("canary.create")}
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
