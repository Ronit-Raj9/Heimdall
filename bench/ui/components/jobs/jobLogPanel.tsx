import { useJobLog } from "@/components/platform/useJobLog";
import { Button } from "@/components/ui/button";

export interface JobLogPanelProps {
	jobId: string;
	onClose?: () => void;
}

/** A job's live log (web parity Phase 0 stub; lane L02 completes it). */
export function JobLogPanel({ jobId, onClose }: JobLogPanelProps) {
	const log = useJobLog(jobId);
	return (
		<div className="border-border rounded-lg border p-3" data-testid="job-log-panel">
			<div className="mb-2 flex items-center justify-between gap-2 text-sm">
				<span className="font-mono">{jobId}</span>
				{onClose && (
					<Button size="sm" variant="ghost" onClick={onClose} data-testid="job-log-panel-close">
						Close
					</Button>
				)}
			</div>
			<pre className="bg-muted max-h-80 overflow-auto rounded p-2 text-xs">{log.lines.map((l) => l.line).join("\n")}</pre>
		</div>
	);
}

export default JobLogPanel;
