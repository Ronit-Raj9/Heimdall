import { JobRunButton } from "@/components/jobs/jobRunButton";
import "@/components/platform/ufintI18n";
import i18n from "@/lib/i18n";
import { Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { livescoreApi } from "@/lib/store/apis/livescoreApi";
import type { LiveInitParams, LiveSplit, PullCheck } from "@/lib/types/liveinbox";
import { liveinboxApi } from "@/lib/store/apis/liveinboxApi";
import { PullSteps } from "@/components/inbox/pullSteps";
import { JobLogPanel } from "@/components/jobs/jobLogPanel";
import { useId, useState } from "react";
import { useAppDispatch } from "@/lib/store/hooks";
import { useTranslation } from "react-i18next";
import { SetupCard, Step } from "@/components/inbox/featureHero";
import { Activity } from "lucide-react";

/** Refreshes the Live status once a job has been queued (the job keeps running; the user can refresh again). */
function useRefreshLive() {
	const dispatch = useAppDispatch();
	return () => {
		dispatch(livescoreApi.util.invalidateTags([{ type: "BenchLive", id: "STATUS" }]));
		dispatch(liveinboxApi.util.invalidateTags([{ type: "BenchPullReady", id: "INFO" }]));
	};
}

export interface LiveActionsProps {
	/** True on the hosted server: the pull runs there with org secrets and the org's own judge key. */
	hosted?: boolean;
	/** What a pull still needs (GET /liveinbox/pull); undefined while loading. */
	check?: PullCheck;
	/** True when live scoring is configured; the pull and compare buttons are offered only then. */
	configured: boolean;
}

/** Pull and compare as background jobs (livescore.pull, livescore.compare). */
export function LiveActions({ hosted, configured, check }: LiveActionsProps) {
	const { t } = useTranslation("liveinbox");
	const refresh = useRefreshLive();
	const [judge, setJudge] = useState(false);
	const [restart, setRestart] = useState(false);
	if (!configured) return null;
	return (
		<Section title={t("live.actions.title")} description={t("live.actions.description")} testId="live-actions">
			<div className="flex flex-wrap items-center gap-4">
				{check && !check.ready ? (
					<PullSteps check={check} testId="live-pull-steps" />
				) : (
					<span className="flex flex-wrap items-center gap-2" data-testid="live-pull">
						<JobRunButton
							kind="livescore.pull"
							params={{ judge }}
							label={t("live.actions.pull")}
							// Grading with a model is paid: never on one click.
							confirm={judge ? i18n.t("live.judgeConfirm", { ns: "ufint" }) : undefined}
							onDone={refresh}
						/>
						{(!check || check.judge) && (
							<label className="text-muted-foreground flex items-center gap-1 text-xs">
								<input type="checkbox" checked={judge} onChange={(e) => setJudge(e.target.checked)} data-testid="live-judge" />
								{hosted ? t("live.actions.judgeHosted") : t("live.actions.judge")}
							</label>
						)}
						{judge && check?.judge && !check.judge.key_ready && (
							<span className="text-xs" data-testid="live-judge-next">
								{check.judge.next}{" "}
								{check.judge.link && (
									<a href={check.judge.link} className="text-primary font-medium underline underline-offset-2">
										{t("pull.steps.openModels")}
									</a>
								)}
							</span>
						)}
					</span>
				)}
				<span className="flex items-center gap-2">
					<JobRunButton
						kind="livescore.compare"
						params={{ restart }}
						label={t("live.actions.compare")}
						variant="outline"
						confirm={restart ? t("live.actions.restartConfirm") : undefined}
						onDone={refresh}
					/>
					<label className="text-muted-foreground flex items-center gap-1 text-xs">
						<input type="checkbox" checked={restart} onChange={(e) => setRestart(e.target.checked)} data-testid="live-restart" />
						{t("live.actions.restart")}
					</label>
				</span>
			</div>
		</Section>
	);
}

/** Builds livescore.init params from the form; null when a chosen split is missing a value. Exported for tests. */
export function liveInitParams(split: LiveSplit, v: Required<Omit<LiveInitParams, "force">>, force: boolean): LiveInitParams | null {
	const p: LiveInitParams = { force };
	if (v.url.trim()) p.url = v.url.trim();
	if (v.token_env.trim()) p.token_env = v.token_env.trim();
	if (split === "keys") {
		if (!v.baseline_key.trim() || !v.candidate_key.trim()) return null;
		p.baseline_key = v.baseline_key.trim();
		p.candidate_key = v.candidate_key.trim();
	} else if (split === "tag") {
		if (!v.tag_header.trim() || !v.baseline_tag.trim() || !v.candidate_tag.trim()) return null;
		p.tag_header = v.tag_header.trim();
		p.baseline_tag = v.baseline_tag.trim();
		p.candidate_tag = v.candidate_tag.trim();
	}
	return p;
}

/** Save is possible with complete params, and over existing settings only once Replace is ticked (N131). */
export function liveSetupCanSave(params: LiveInitParams | null, existing: boolean | undefined, force: boolean): boolean {
	return params !== null && (!existing || force);
}

/** "Decide later" writes no settings file: an unedited template is a trap, and "not set up yet" is the true state. */
export function liveSetupStartsJob(split: LiveSplit): boolean {
	return split !== "later";
}

const EMPTY = {
	url: "",
	token_env: "",
	baseline_key: "",
	candidate_key: "",
	tag_header: "x-bf-lh-agent-version",
	baseline_tag: "",
	candidate_tag: "",
};

/** The setup form: queues livescore.init, which writes livescore/config.yaml. */
export function LiveSetup({ existing, hosted, onClose }: { existing?: boolean; hosted?: boolean; onClose?(): void }) {
	const { t } = useTranslation("liveinbox");
	const { t: u } = useTranslation("ux13");
	const { t: g } = useTranslation("gw16");
	const refresh = useRefreshLive();
	const [split, setSplit] = useState<LiveSplit>("keys");
	const [v, setV] = useState(EMPTY);
	// Replace starts unticked even when settings exist: one Save must never overwrite them by default (N131).
	const [force, setForce] = useState(false);
	const needsTick = Boolean(existing) && !force;
	const [createJob, state] = useCreateJobMutation();
	const [jobId, setJobId] = useState<string | null>(null);
	const ids = useId();
	const params = liveInitParams(split, v, force);
	const field = (key: keyof typeof EMPTY, label: string, hint?: string, placeholder?: string) => (
		<label className="flex min-w-56 flex-1 flex-col gap-1 text-xs" htmlFor={`${ids}-${key}`}>
			<span className="text-foreground font-medium">{label}</span>
			<Input
				id={`${ids}-${key}`}
				value={v[key]}
				placeholder={placeholder}
				onChange={(e) => setV({ ...v, [key]: e.target.value })}
				data-testid={`live-setup-${key}`}
			/>
			{hint && <span className="text-muted-foreground">{hint}</span>}
		</label>
	);
	const submit = async () => {
		if (!liveSetupStartsJob(split)) {
			onClose?.();
			return;
		}
		if (!params) return;
		const res = await createJob({ kind: "livescore.init", params });
		if ("data" in res && res.data) {
			setJobId(res.data.id);
			refresh();
		}
	};
	const errorText = state.error ? getBenchErrorMessage(state.error) : "";
	return (
		<SetupCard
			icon={Activity}
			title={u("live.setupTitle")}
			description={u("live.setupDescription")}
			testId="live-setup"
			actions={
				onClose && (
					<Button size="sm" variant="ghost" onClick={onClose} data-testid="live-setup-close">
						{u("live.close")}
					</Button>
				)
			}
		>
			<div className="flex flex-col">
				<Step n={1} title={u("live.stepSplit")}>
					<fieldset className="flex flex-wrap items-center gap-2 text-sm">
						<legend className="sr-only">{t("live.setup.split")}</legend>
						{(["keys", "tag", "later"] as const).map((s) => (
							<label
								key={s}
								className={`flex cursor-pointer items-center gap-2 rounded-full border px-3 py-1.5 text-xs transition-colors ${split === s ? "border-primary bg-primary/10 text-foreground" : "text-muted-foreground hover:bg-muted"}`}
							>
								<input
									type="radio"
									className="sr-only"
									name={`${ids}-split`}
									checked={split === s}
									onChange={() => setSplit(s)}
									data-testid={`live-setup-split-${s}`}
								/>
								{t(`live.setup.${s}`)}
							</label>
						))}
					</fieldset>
					{split === "keys" && (
						<div className="flex flex-wrap gap-3">
							{field("baseline_key", t("live.setup.baselineKey"), undefined, "vk-prod")}
							{field("candidate_key", t("live.setup.candidateKey"), undefined, "vk-canary")}
						</div>
					)}
					{split === "tag" && (
						<div className="flex flex-wrap gap-3">
							{field("tag_header", t("live.setup.tagHeader"))}
							{field("baseline_tag", t("live.setup.baselineTag"), undefined, "v1")}
							{field("candidate_tag", t("live.setup.candidateTag"), undefined, "v2")}
						</div>
					)}
				</Step>
				<Step n={2} title={u("live.stepGateway")}>
					<div className="flex flex-wrap gap-3">
						{field(
							"url",
							t("live.setup.url"),
							hosted ? t("live.setup.urlHintHosted") : t("live.setup.urlHint"),
							hosted ? "https://gateway.example.com" : "http://localhost:8080",
						)}
						{field(
							"token_env",
							hosted ? t("live.setup.tokenEnvHosted") : t("live.setup.tokenEnv"),
							hosted ? t("live.setup.tokenEnvHintHosted") : t("live.setup.tokenEnvHint"),
							"BIFROST_ADMIN_TOKEN",
						)}
					</div>
				</Step>
				<Step n={3} title={u("live.stepSave")} last>
					{existing && (
						<p className="text-foreground text-xs" data-testid="live-setup-existing">
							{g("live.existing")}
						</p>
					)}
					<label className="text-muted-foreground flex items-center gap-2 text-xs">
						<input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} data-testid="live-setup-force" />
						{t("live.setup.force")}
					</label>
					<div className="flex flex-wrap items-center gap-3">
						<Button
							disabled={liveSetupStartsJob(split) && (!liveSetupCanSave(params, existing, force) || state.isLoading)}
							onClick={() => void submit()}
							data-testid="live-setup-submit"
						>
							{t("live.setup.submit")}
						</Button>
						{!params && <span className="text-muted-foreground text-xs">{t("live.setup.incomplete")}</span>}
						{params && needsTick && (
							<span className="text-muted-foreground text-xs" data-testid="live-setup-tick-hint">
								{g("live.tickToSave")}
							</span>
						)}
						{state.isError && (
							<span className="text-chart-error-ink text-xs" role="alert" data-testid="live-setup-error">
								{errorText}
							</span>
						)}
					</div>
					{jobId && <JobLogPanel jobId={jobId} onClose={() => setJobId(null)} />}
				</Step>
			</div>
		</SetupCard>
	);
}