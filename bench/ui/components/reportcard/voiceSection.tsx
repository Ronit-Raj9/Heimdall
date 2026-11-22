import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Report, ReportRate } from "@/lib/store/apis/evaluationsApi";
import { isAB, pct } from "./reportCard.utils";

/** The voice section shows when any run was a voice call. */
export function hasVoice(report: Report): boolean {
	return (report.voice?.length ?? 0) > 0;
}

/** "640 ms", or "-" when no run measured it. */
export function msText(rate: ReportRate | undefined): string {
	if (!rate || !Number.isFinite(rate.value)) return "-";
	return rate.value >= 10000 ? `${(rate.value / 1000).toFixed(1)} s` : `${Math.round(rate.value)} ms`;
}

function secondsText(rate: ReportRate | undefined): string {
	if (!rate || !Number.isFinite(rate.value)) return "-";
	return `${rate.value.toFixed(1)} s`;
}

function countText(rate: ReportRate | undefined): string {
	if (!rate || !Number.isFinite(rate.value)) return "-";
	return rate.value.toFixed(rate.value >= 10 ? 0 : 1);
}

function shareText(rate: ReportRate | undefined): string {
	return rate && Number.isFinite(rate.value) ? pct(rate.value) : "-";
}

function usdText(rate: ReportRate | undefined): string {
	return rate && Number.isFinite(rate.value) ? `$${rate.value.toFixed(3)}` : "-";
}

const columns: { key: keyof Omit<NonNullable<Report["voice"]>[number], "arm">; label: string; text: (r?: ReportRate) => string; hint: string }[] = [
	{ key: "time_to_first_audio_ms", label: "First audio", text: msText, hint: "From the call connecting to the agent's first words." },
	{ key: "turn_latency_p50_ms", label: "Reply time (median)", text: msText, hint: "From the caller finishing to the agent starting to answer." },
	{ key: "turn_latency_p95_ms", label: "Reply time (slowest 5%)", text: msText, hint: "95th percentile of the reply time." },
	{ key: "barge_in_handled", label: "Stopped when interrupted", text: shareText, hint: "Share of interruptions after which the agent went quiet within the task's bound." },
	{ key: "talk_overs", label: "Talk-overs per call", text: countText, hint: "Times the agent started speaking while the caller was still talking." },
	{ key: "dead_air_s", label: "Dead air", text: secondsText, hint: "Long silences while the caller waited for the agent." },
	{ key: "stt_wer", label: "Speech recognition errors", text: shareText, hint: "Word error rate of the agent's transcript of what the caller said." },
	{ key: "voice_cost_per_minute", label: "Cost per minute", text: usdText, hint: "Measured cost divided by the call length." },
];

/** "Voice": call quality on the voice benchmarks, measured by Heimdall's caller on what it heard. */
export function VoiceTable({ report }: { report: Report }) {
	const ab = isAB(report);
	const rows = report.voice ?? [];
	const shown = columns.filter((c) => rows.some((r) => r[c.key] !== undefined));
	return (
		<Table data-testid="report-voice" aria-label="Voice">
			<TableHeader>
				<TableRow>
					<TableHead scope="col">Agent</TableHead>
					{shown.map((c) => (
						<TableHead key={c.key} scope="col" className="text-right" title={c.hint}>
							{c.label}
						</TableHead>
					))}
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((row) => (
					<TableRow key={row.arm} data-testid={`voice-${row.arm}`}>
						<TableHead scope="row" className="font-medium">
							{ab ? row.arm : "Your agent"}
						</TableHead>
						{shown.map((c) => (
							<TableCell key={c.key} className="text-right tabular-nums" title={row[c.key] ? `${row[c.key]!.runs} calls` : undefined}>
								{c.text(row[c.key])}
							</TableCell>
						))}
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}
