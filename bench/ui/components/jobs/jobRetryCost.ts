import type { Job } from "@/lib/types/bench";

/**
 * True for a job whose retry calls no model, so its Retry is free (R3-4). Only what the server
 * proves: a debug replay of a run (handlers/rundebug.go) is an experiment.run job whose comparison
 * is named "replay <run id>", so its id starts "e_replay_r_"; it answers from the recording.
 * Fork and step debug runs call the model after the fork point and stay paid. Pure.
 */
export function canRetryFree(job: Pick<Job, "kind" | "params" | "target">): boolean {
	if (job.kind !== "experiment.run") return false;
	const exp = typeof job.params?.exp_id === "string" ? job.params.exp_id : (job.target ?? "");
	return /^(experiment:)?e_replay_r_/.test(exp);
}
