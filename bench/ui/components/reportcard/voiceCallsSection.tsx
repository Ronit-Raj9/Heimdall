import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Report, ReportRate } from "@/lib/store/apis/evaluationsApi";
import { armName, isAB, pct } from "./reportCard.utils";

/** Rows of the voice calls section, or none when every voice call used the plain caller. */
export function hasVoiceCalls(report: Report): boolean {
	return (report.voice_calls?.length ?? 0) > 0;
}

/** "Plain caller" for a call with no persona or audio test; a persona or audio test id as words otherwise. */
export function callerName(id: string): string {
	if (id === "clean") return "Plain caller";
	return id.charAt(0).toUpperCase() + id.slice(1).replace(/[_-]/g, " ");
}

function ms(r?: ReportRate): string {
	return r && Number.isFinite(r.value) ? `${Math.round(r.value)} ms` : "-";
}

/** "Voice calls by caller": task success and call quality under each simulated caller. */
export function VoiceCallsTable({ report }: { report: Report }) {
	const ab = isAB(report);
	const rows = report.voice_calls ?? [];
	return (
		<Table data-testid="report-voice-calls" aria-label="Voice calls by caller">
			<TableHeader>
				<TableRow>
					{ab && <TableHead scope="col">Agent</TableHead>}
					<TableHead scope="col">Caller</TableHead>
					<TableHead scope="col" className="text-right">
						Calls
					</TableHead>
					<TableHead scope="col" className="text-right">
						Task success
					</TableHead>
					<TableHead scope="col" className="text-right">
						First audio
					</TableHead>
					<TableHead scope="col" className="text-right">
						Turn latency p95
					</TableHead>
					<TableHead scope="col" className="text-right">
						Stopped when interrupted
					</TableHead>
					<TableHead scope="col" className="text-right">
						Audio jitter p95
					</TableHead>
					<TableHead scope="col" className="text-right">
						Word error rate
					</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((row) => (
					<TableRow key={`${row.arm}-${row.condition}`} data-testid={`voice-call-${row.arm}-${row.condition}`}>
						{ab && <TableCell className="font-semibold">{armName(report, row.arm)}</TableCell>}
						<TableHead scope="row" className="font-medium">
							{callerName(row.condition)}
						</TableHead>
						<TableCell className="text-right tabular-nums">{row.runs}</TableCell>
						<TableCell className="text-right tabular-nums">
							{pct(row.success_rate)} ({row.successes} of {row.runs})
						</TableCell>
						<TableCell className="text-right tabular-nums">{ms(row.time_to_first_audio_ms)}</TableCell>
						<TableCell className="text-right tabular-nums">{ms(row.turn_latency_p95_ms)}</TableCell>
						<TableCell className="text-right tabular-nums">{row.barge_in_handled ? pct(row.barge_in_handled.value) : "-"}</TableCell>
						<TableCell className="text-right tabular-nums">{ms(row.audio_jitter_p95_ms)}</TableCell>
						<TableCell className="text-right tabular-nums">{row.stt_wer ? pct(row.stt_wer.value) : "-"}</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}
