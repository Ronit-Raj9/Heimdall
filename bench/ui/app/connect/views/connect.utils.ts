import type { ConnectCandidate, ConnectDetectResponse, ConnectQuestion, VerifyRow, VerifyRowStatus } from "@/lib/types/connect";

/** How often the verification job is polled while it runs. */
export const VERIFY_POLL_MS = 1500;

/** Job statuses after which the rows no longer change. */
export function isTerminalJob(status: string | undefined): boolean {
	return status === "succeeded" || status === "failed" || status === "cancelled";
}

/** The best candidate: the first one Heimdall can connect. */
export function bestCandidate(det: ConnectDetectResponse | undefined): ConnectCandidate | undefined {
	return det?.candidates.find((c) => c.supported);
}

/** The other candidates, shown folded. */
export function otherCandidates(det: ConnectDetectResponse | undefined): ConnectCandidate[] {
	const best = bestCandidate(det);
	return (det?.candidates ?? []).filter((c) => c !== best);
}

/** Questions that ask for a secret value. */
export function secretQuestions(c: ConnectCandidate | undefined): ConnectQuestion[] {
	const seen = new Set<string>();
	return (c?.questions ?? []).filter((q) => {
		if (q.kind !== "secret" || !q.secret || seen.has(q.secret)) return false;
		seen.add(q.secret);
		return true;
	});
}

/** The answer path picked from the fields the endpoint returned, applied to a copy of the candidate. */
export function withAnswerPath(c: ConnectCandidate, path: string): ConnectCandidate {
	const mapping = { ...(c.mapping ?? {}) };
	if (!mapping.body)
		mapping.body =
			'{"instruction": "{{instruction}}", "session_id": "{{session_id}}", "session_index": "{{session_index}}", "output": "{{output}}"}';
	mapping.answer_path = path;
	return { ...c, mapping, questions: (c.questions ?? []).filter((q) => q.kind !== "pick_field") };
}

/** Badge variant of a row status. */
export function rowTone(status: VerifyRowStatus): "success" | "warning" | "destructive" | "neutral" | "info" {
	switch (status) {
		case "pass":
			return "success";
		case "warn":
			return "warning";
		case "fail":
			return "destructive";
		case "running":
			return "info";
		default:
			return "neutral";
	}
}

/** Whether the connection rows passed (reach to answer); later rows only warn. */
export function connectionOK(rows: VerifyRow[]): boolean {
	const key = ["reach", "auth", "shape", "answer"];
	return key.every((id) => {
		const r = rows.find((x) => x.id === id);
		return !r || r.status === "pass" || (id === "reach" && r.status === "skip");
	});
}

/** A suggested agent name for a candidate, from the detect preview. */
export function suggestedName(det: ConnectDetectResponse | undefined): string {
	return det?.preview?.id ?? "my-agent";
}

/** The agent name rule of the server (agentIDRe). */
export function validAgentName(id: string): boolean {
	return /^[a-z0-9][a-z0-9._-]{0,62}$/.test(id) && !["new", "diff", "templates", "config"].includes(id);
}

/** Minutes, rounded up, of an estimate in seconds; 0 when unknown. */
export function estimateMinutes(seconds: number): number {
	return seconds > 0 ? Math.max(1, Math.ceil(seconds / 60)) : 0;
}