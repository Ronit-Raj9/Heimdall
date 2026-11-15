import { ToneBadge } from "@/components/suites/suiteBadges";
import { useGetRunRemoteQuery } from "@/lib/store/apis/runRemoteApi";
import type { RemoteChannelState, RemoteEvidence } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";
import { clipText } from "./stepTimeline.utils";

/** Tone of a channel state: on is measured, off is a choice, missing was asked for and did not arrive. */
const CHANNEL_TONE = { on: "good", off: "neutral", missing: "bad" } as const;

/** The three channels, in the order the docs list them. */
const CHANNELS = ["model_traffic", "tools", "self_report"] as const;

/**
 * One run's remote coverage, fetched by run id. Renders nothing for a run of an agent that is not
 * remote, while loading, or on an error: the step timeline beside it already reports those states.
 */
export function RunRemoteCoverage({ runId, arm }: { runId: string; arm: string }) {
	const remote = useGetRunRemoteQuery(runId);
	if (!remote.data?.available || !remote.data.remote) return null;
	return <RemoteCoverage remote={remote.data.remote} arm={arm} />;
}

/**
 * Which of a remote agent's three channels were active and what is therefore trusted, agent-reported
 * or unknown. Agent-reported values carry their own badge and are shown as plain, clipped text.
 */
export function RemoteCoverage({ remote, arm }: { remote: RemoteEvidence; arm: string }) {
	const { t } = useTranslation("bench");
	const rep = remote.reported;
	const tokens = rep?.total_tokens ?? (rep?.input_tokens !== undefined || rep?.output_tokens !== undefined ? (rep?.input_tokens ?? 0) + (rep?.output_tokens ?? 0) : undefined);
	return (
		<div className="flex min-w-0 flex-col gap-2 rounded-md border p-3 text-xs" data-testid={`remote-coverage-${arm}`}>
			<p className="font-medium text-sm">{t("runs.remote.title")}</p>
			<ul className="flex flex-wrap gap-2" aria-label={t("runs.remote.channels")}>
				{CHANNELS.map((ch) => {
					const state: RemoteChannelState = remote.channels[ch] in CHANNEL_TONE ? remote.channels[ch] : "missing";
					return (
						<li key={ch} data-testid={`remote-channel-${arm}-${ch}`}>
							<ToneBadge tone={CHANNEL_TONE[state]}>
								{t(`runs.remote.channel.${ch}`)}: {t(`runs.remote.state.${state}`)}
							</ToneBadge>
						</li>
					);
				})}
			</ul>
			<RemoteList label={t("runs.remote.trusted")} items={remote.trusted} testId={`remote-trusted-${arm}`} />
			<RemoteList label={t("runs.remote.agentReported")} items={remote.agent_reported} testId={`remote-reported-${arm}`} />
			<RemoteList label={t("runs.remote.unknown")} items={remote.unknown} testId={`remote-unknown-${arm}`} />
			{rep ? (
				<div className="flex flex-wrap items-center gap-2" data-testid={`remote-self-report-${arm}`}>
					<ToneBadge tone="warn" hint={t("runs.remote.reportedHint")}>
						{t("runs.remote.reportedBadge")}
					</ToneBadge>
					{tokens !== undefined ? <span>{t("runs.remote.reportedTokens", { value: tokens })}</span> : null}
					{rep.cost_usd !== undefined ? <span>{t("runs.remote.reportedCost", { value: rep.cost_usd.toFixed(4) })}</span> : null}
					{rep.version || rep.build ? (
						<span>{t("runs.remote.version", { version: clipText(rep.version, 60) || "-", build: clipText(rep.build, 60) || "-" })}</span>
					) : null}
					{rep.events ? <span>{t("runs.remote.events", { count: rep.events })}</span> : null}
				</div>
			) : null}
		</div>
	);
}

function RemoteList({ label, items, testId }: { label: string; items?: string[]; testId: string }) {
	if (!items || items.length === 0) return null;
	return (
		<p className="text-muted-foreground" data-testid={testId}>
			<span className="text-foreground">{label}</span> {items.map((i) => clipText(i, 120)).join(", ")}
		</p>
	);
}
