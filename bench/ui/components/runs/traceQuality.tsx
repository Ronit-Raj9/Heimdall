import type { ParseDiagnostics } from "@/lib/types/bench";

/** One plain line on why the trace is not complete, or null when it is complete or unknown. */
export function traceMissingLine(trace?: ParseDiagnostics): string | null {
	if (!trace?.quality || trace.quality === "complete") return null;
	if (!trace.parser) return "Steps come from the gateway only: this agent has no step parser.";
	return `Parser ${trace.parser} read only part of the agent's output.`;
}

export function TraceQualityDetails({ trace, arm }: { trace?: ParseDiagnostics; arm: string }) {
	return (
		<div data-testid={`runs-trace-quality-${arm}`}>
			<div>
				Agent trace: {trace?.quality || "unknown"} ({trace?.source || "unknown"})
			</div>
			{trace ? (
				<>
					{traceMissingLine(trace) && <div data-testid={`runs-trace-missing-${arm}`}>{traceMissingLine(trace)}</div>}
					<div>
						Parser: {trace.parser || "none"} {trace.parser_version || ""}
					</div>
					<div>
						Records: {trace.lines ?? 0}; events: {trace.emitted ?? 0}; malformed: {trace.malformed ?? 0}; oversized: {trace.oversized ?? 0};
						unrecognized: {trace.unrecognized ?? 0}
					</div>
					{trace.truncated && <div>Captured output was truncated.</div>}
					{trace.reason && <div>Trace note: {trace.reason}</div>}
				</>
			) : (
				<div>Trace diagnostics were not recorded for this run.</div>
			)}
		</div>
	);
}