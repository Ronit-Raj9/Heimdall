import type { ParseDiagnostics } from "@/lib/types/bench";

export function TraceQualityDetails({ trace, arm }: { trace?: ParseDiagnostics; arm: string }) {
	return (
		<div data-testid={`runs-trace-quality-${arm}`}>
			<div>
				Agent trace: {trace?.quality || "unknown"} ({trace?.source || "unknown"})
			</div>
			{trace ? (
				<>
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