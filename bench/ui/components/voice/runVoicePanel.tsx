import { ToneBadge } from "@/components/suites/suiteBadges";
import { useGetRunVoiceQuery } from "@/lib/store/apis/voiceApi";
import type { VoiceAnalysis, VoiceCall } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

// X8 voice agents (docs/voice-agents.md): the call panel of the run page.

/**
 * One run's voice call, fetched by run id. Renders nothing for a run without a voice caller, while
 * loading, or on an error: the run detail beside it reports those states.
 */
export function RunVoicePanel({ runId }: { runId: string }) {
	const q = useGetRunVoiceQuery(runId);
	if (!q.data) return null;
	return <VoicePanel data={q.data} />;
}

/** One bar of the call timeline: who spoke, from when to when, on the call clock. */
export interface VoiceBar {
	who: "caller" | "agent";
	start: number;
	end: number;
	label: string;
	tone: "neutral" | "warn" | "bad";
}

/** The call as bars, caller lines and agent utterances in time order. */
export function voiceBars(a: VoiceAnalysis): VoiceBar[] {
	const bars: VoiceBar[] = [];
	for (const l of a.lines) {
		bars.push({ who: "caller", start: l.start_ms, end: l.end_ms, label: l.text, tone: l.kind === "interrupt" ? "warn" : "neutral" });
	}
	for (const u of a.utterances) {
		bars.push({
			who: "agent",
			start: u.start_ms,
			end: Math.max(u.end_ms, u.start_ms),
			label: u.text ?? "",
			tone: u.interrupted ? "warn" : u.text == null ? "bad" : "neutral",
		});
	}
	return bars.sort((x, y) => x.start - y.start);
}

/** A percentage of the call's length, for absolute bar placement. */
export function pct(ms: number, total: number): string {
	if (!(total > 0)) return "0%";
	return `${Math.min(100, Math.max(0, (ms / total) * 100)).toFixed(2)}%`;
}

/** Milliseconds as "1.2 s" or "340 ms". */
export function formatMs(ms: number | undefined | null): string {
	if (ms == null || !Number.isFinite(ms)) return "-";
	return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms)} ms`;
}

/**
 * The call: a turn timeline with each side's speech as bars, every turn's latency as a bar from the
 * end of the caller's line to the agent's first audio (red past the dead air bound), interruptions,
 * both transcripts, and the call audio when the run kept it.
 */
export function VoicePanel({ data }: { data: VoiceCall }) {
	const { t } = useTranslation("bench");
	const a = data.analysis;
	return (
		<div className="flex min-w-0 flex-col gap-3 rounded-md border p-3 text-xs" data-testid={`voice-panel-${data.run_id}`}>
			<div className="flex flex-wrap items-center gap-2">
				<p className="text-sm font-medium">{t("voice.title")}</p>
				<ToneBadge tone="neutral" hint={data.witness}>
					{t("voice.witness")}
				</ToneBadge>
				{a ? (
					<ToneBadge tone={a.complete ? "good" : "bad"} testId="voice-ended-badge">
						{t("voice.ended", { by: a.ended_by, length: formatMs(a.call_ms) })}
					</ToneBadge>
				) : null}
			</div>
			{!data.called || !a ? (
				<p className="text-muted-foreground" data-testid="voice-no-call">
					{t("voice.noCall", { reason: data.reason ?? "" })}
				</p>
			) : (
				<>
					<Summary a={a} />
					{data.bounds_source === "default" && (
						<p className="text-chart-warning-ink" data-testid="voice-default-bounds">
							{t("voice.defaultBounds")}
						</p>
					)}
					<Timeline a={a} />
					<Turns a={a} />
					{a.barge_ins.length > 0 ? <BargeIns a={a} /> : null}
					<Transcript a={a} />
				</>
			)}
			{data.audio.length > 0 ? (
				<div className="flex flex-wrap gap-4" data-testid="voice-audio">
					{data.audio.map((tr) => (
						<figure key={tr.track} className="flex flex-col gap-1">
							<figcaption className="text-muted-foreground">{t(`voice.track.${tr.track}`)}</figcaption>
							{/* biome-ignore lint/a11y/useMediaCaption: call audio has its transcript beside it */}
							<audio controls preload="none" src={tr.url} data-testid={`voice-audio-${tr.track}`} />
						</figure>
					))}
				</div>
			) : data.called ? (
				<p className="text-muted-foreground" data-testid="voice-no-audio">
					{t("voice.noAudio")}
				</p>
			) : null}
		</div>
	);
}

function Summary({ a }: { a: VoiceAnalysis }) {
	const { t } = useTranslation("bench");
	const lat = a.turns.flatMap((x) => (x.latency_ms != null ? [x.latency_ms] : []));
	return (
		<p className="text-muted-foreground" data-testid="voice-summary">
			{t("voice.summary", {
				first: formatMs(a.first_audio_ms),
				turns: lat.length,
				deadAir: formatMs(a.dead_air_ms),
				bound: formatMs(a.dead_air_bound_ms),
				clears: a.clears,
			})}
		</p>
	);
}

function Timeline({ a }: { a: VoiceAnalysis }) {
	const { t } = useTranslation("bench");
	const bars = voiceBars(a);
	const total = Math.max(a.call_ms, ...bars.map((b) => b.end), 1);
	const color = (b: VoiceBar) =>
		b.tone === "warn" ? "bg-amber-500/70" : b.tone === "bad" ? "bg-red-500/70" : b.who === "caller" ? "bg-sky-500/70" : "bg-emerald-500/70";
	return (
		<div className="flex flex-col gap-1" data-testid="voice-timeline">
			{(["caller", "agent"] as const).map((who) => (
				<div key={who} className="flex items-center gap-2">
					<span className="w-14 shrink-0 text-muted-foreground">{t(`voice.who.${who}`)}</span>
					<div className="relative h-4 flex-1 rounded bg-muted">
						{bars
							.filter((b) => b.who === who)
							.map((b) => (
								<div
									key={`${who}-${b.start}`}
									title={`${formatMs(b.start)}: ${b.label}`}
									className={`absolute top-0 h-4 rounded ${color(b)}`}
									style={{ left: pct(b.start, total), width: pct(Math.max(b.end - b.start, total / 400), total) }}
									data-testid={`voice-bar-${who}`}
								/>
							))}
					</div>
				</div>
			))}
		</div>
	);
}

function Turns({ a }: { a: VoiceAnalysis }) {
	const { t } = useTranslation("bench");
	const longest = Math.max(a.dead_air_bound_ms, ...a.turns.map((x) => x.gap_ms), 1);
	return (
		<table className="w-full table-fixed" data-testid="voice-turns">
			<thead>
				<tr className="text-left text-muted-foreground">
					<th className="w-28 font-normal">{t("voice.turn")}</th>
					<th className="font-normal">{t("voice.latency")}</th>
					<th className="w-20 font-normal">{t("voice.gap")}</th>
				</tr>
			</thead>
			<tbody>
				{a.turns.map((x) => (
					<tr key={`${x.kind}-${x.floor_ms}`} data-testid="voice-turn-row">
						<td>{t(`voice.kind.${x.kind}`, { defaultValue: x.kind, turn: x.caller_turn ?? 0 })}</td>
						<td>
							<div className="h-2 rounded bg-muted">
								<div
									className={`h-2 rounded ${x.dead_air ? "bg-red-500" : "bg-emerald-500"}`}
									style={{ width: pct(x.gap_ms, longest) }}
									data-testid={x.dead_air ? "voice-dead-air" : "voice-latency"}
								/>
							</div>
						</td>
						<td>{x.agent_start_ms == null ? t("voice.noAnswer") : formatMs(x.gap_ms)}</td>
					</tr>
				))}
			</tbody>
		</table>
	);
}

function BargeIns({ a }: { a: VoiceAnalysis }) {
	const { t } = useTranslation("bench");
	return (
		<ul className="flex flex-col gap-1" data-testid="voice-barge-ins">
			{a.barge_ins.map((b) => (
				<li key={b.caller_start_ms}>
					<ToneBadge tone={b.handled ? "good" : "bad"}>{b.handled ? t("voice.bargeHandled") : t("voice.bargeMissed")}</ToneBadge>{" "}
					{t("voice.barge", { after: formatMs(b.stopped_after_ms), by: b.by, bound: formatMs(a.barge_in_bound_ms) })}
				</li>
			))}
		</ul>
	);
}

function Transcript({ a }: { a: VoiceAnalysis }) {
	const { t } = useTranslation("bench");
	const rows = voiceBars(a);
	return (
		<ol className="flex flex-col gap-1" data-testid="voice-transcript">
			{rows.map((r) => (
				<li key={`${r.who}-${r.start}`} className="flex gap-2">
					<span className="w-16 shrink-0 text-muted-foreground">{formatMs(r.start)}</span>
					<span className="w-14 shrink-0 font-medium">{t(`voice.who.${r.who}`)}</span>
					<span className={r.tone === "bad" ? "text-muted-foreground italic" : ""}>{r.label || t("voice.inaudible")}</span>
				</li>
			))}
		</ol>
	);
}
