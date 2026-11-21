import { Button, type buttonVariants } from "@/components/ui/button";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import type { Job } from "@/lib/types/bench";
import type { VariantProps } from "class-variance-authority";
import { useState } from "react";
import { JobLogPanel } from "./jobLogPanel";

export interface JobRunButtonProps {
	kind: string;
	params?: object;
	label: string;
	/** When set, asked with window.confirm before the job is queued. */
	confirm?: string;
	onDone?(job: Job): void;
	variant?: VariantProps<typeof buttonVariants>["variant"];
}

/** Queues a job through POST /jobs and opens its log (web parity Phase 0 stub; lane L02 completes it). */
export function JobRunButton({ kind, params, label, confirm, onDone, variant }: JobRunButtonProps) {
	const [createJob, { isLoading }] = useCreateJobMutation();
	const [jobId, setJobId] = useState<string | null>(null);
	const run = async () => {
		if (confirm && !window.confirm(confirm)) return;
		const res = await createJob({ kind, params });
		if ("data" in res && res.data) {
			setJobId(res.data.id);
			onDone?.(res.data);
		}
	};
	return (
		<>
			<Button variant={variant} disabled={isLoading} onClick={() => void run()} data-testid={`job-run-${kind}`}>
				{label}
			</Button>
			{jobId && <JobLogPanel jobId={jobId} onClose={() => setJobId(null)} />}
		</>
	);
}

export default JobRunButton;
