import { JobRunButton } from "@/components/jobs/jobRunButton";
import { Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { livescoreApi } from "@/lib/store/apis/livescoreApi";
import type { LiveInitParams, LiveSplit } from "@/lib/types/liveinbox";
import { JobLogPanel } from "@/components/jobs/jobLogPanel";
import { useId, useState } from "react";
import { useAppDispatch } from "@/lib/store/hooks";
import { useTranslation } from "react-i18next";

/** Refreshes the Live status once a job has been queued (the job keeps running; the user can refresh again). */
function useRefreshLive() {
	const dispatch = useAppDispatch();
	return () => dispatch(livescoreApi.util.invalidateTags([{ type: "BenchLive", id: "STATUS" }]));
}

export interface LiveActionsProps {
	/** True on the hosted server: pulls are refused there, so the pull button is replaced by a note. */
	hosted?: boolean;
	/** True when live scoring is configured; the pull and compare buttons are offered only then. */
	configured: boolean;
}

/** Pull and compare as background jobs (livescore.pull, livescore.compare). */
export function LiveActions({ hosted, configured }: LiveActionsProps) {
	const { t } = useTranslation("liveinbox");
	const refresh = useRefreshLive();
	const [judge, setJudge] = useState(false);
	const [restart, setRestart] = useState(false);
	if (!configured) return null;
	return (
		<Section title={t("live.actions.title")} description={t("live.actions.description")} testId="live-actions">
			<div className="flex flex-wrap items-center gap-4">
				{hosted ? (
					<p className="text-muted-foreground max-w-prose text-xs" data-testid="live-hosted-pull">
						{t("live.actions.hostedPull")}
					</p>
				) : (
					<span className="flex items-center gap-2">
						<JobRunButton kind="livescore.pull" params={{ judge }} label={t("live.actions.pull")} onDone={refresh} />
						<label className="text-muted-foreground flex items-center gap-1 text-xs">
							<input type="checkbox" checked={judge} onChange={(e) => setJudge(e.target.checked)} data-testid="live-judge" />
							{t("live.actions.judge")}
						</label>
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

const EMPTY = { url: "", token_env: "", baseline_key: "", candidate_key: "", tag_header: "x-bf-lh-agent-version", baseline_tag: "", candidate_tag: "" };

/** The setup form: queues livescore.init, which writes livescore/config.yaml. */
export function LiveSetup({ existing }: { existing?: boolean }) {
	const { t } = useTranslation("liveinbox");
	const refresh = useRefreshLive();
	const [split, setSplit] = useState<LiveSplit>("keys");
	const [v, setV] = useState(EMPTY);
	const [force, setForce] = useState(Boolean(existing));
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
		if (!params) return;
		const res = await createJob({ kind: "livescore.init", params });
		if ("data" in res && res.data) {
			setJobId(res.data.id);
			refresh();
		}
	};
	const errorText = state.error ? getBenchErrorMessage(state.error) : "";
	return (
		<Section title={t("live.setup.title")} description={t("live.setup.description")} testId="live-setup">
			<div className="flex flex-col gap-4 rounded-lg border p-4">
				<fieldset className="flex flex-wrap items-center gap-4 text-xs">
					<legend className="text-foreground mb-2 font-medium">{t("live.setup.split")}</legend>
					{(["keys", "tag", "later"] as const).map((s) => (
						<label key={s} className="flex items-center gap-1">
							<input type="radio" name={`${ids}-split`} checked={split === s} onChange={() => setSplit(s)} data-testid={`live-setup-split-${s}`} />
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
				<div className="flex flex-wrap gap-3">
					{field("url", t("live.setup.url"), t("live.setup.urlHint"), "http://localhost:8080")}
					{field("token_env", t("live.setup.tokenEnv"), t("live.setup.tokenEnvHint"), "BIFROST_ADMIN_TOKEN")}
				</div>
				<label className="text-muted-foreground flex items-center gap-1 text-xs">
					<input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} data-testid="live-setup-force" />
					{t("live.setup.force")}
				</label>
				<div className="flex flex-wrap items-center gap-3">
					<Button disabled={!params || state.isLoading} onClick={() => void submit()} data-testid="live-setup-submit">
						{t("live.setup.submit")}
					</Button>
					{!params && <span className="text-muted-foreground text-xs">{t("live.setup.incomplete")}</span>}
					{state.isError && (
						<span className="text-chart-error-ink text-xs" role="alert" data-testid="live-setup-error">
							{errorText}
						</span>
					)}
				</div>
				{jobId && <JobLogPanel jobId={jobId} onClose={() => setJobId(null)} />}
			</div>
		</Section>
	);
}
