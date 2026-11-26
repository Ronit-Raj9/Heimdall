import { isPracticeSuite } from "@/components/suites/suiteDisplay";
import type { AgentPlatform } from "@/lib/types/agentImport";
import type {
	ConnectCandidate,
	ConnectDetectResponse,
	ConnectQuestion,
	ConnectSaveResponse,
	ConnectVerifyStatus,
	VerifyRow,
	VerifyRowStatus,
} from "@/lib/types/connect";

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

/** N105: a candidate is proven when a test request brought the check code back. */
export function isProven(c: ConnectCandidate | undefined): boolean {
	return Boolean(c && (c.proven ?? c.nonce_found));
}

/** The key and values of the result title (N105, N106): not recognised, could not connect, found but not proven, found. */
export function resultTitle(
	det: ConnectDetectResponse,
	cand: ConnectCandidate | undefined,
): {
	key: "result.notRecognised" | "result.couldNotConnect" | "result.notSupported" | "result.found" | "result.foundUnproven";
	vars?: Record<string, string>;
} {
	if (det.class === "unknown") return { key: "result.notRecognised" };
	if (!cand) {
		const f = (det.failures ?? [])[0];
		return f ? { key: "result.couldNotConnect", vars: { reason: f.title } } : { key: "result.notSupported" };
	}
	return isProven(cand)
		? { key: "result.found", vars: { label: cand.label } }
		: { key: "result.foundUnproven", vars: { label: cand.label } };
}

/** N105: Save needs a proven answer, or an answer field the user picked. */
export function canSave(cand: ConnectCandidate | undefined, userMapped: boolean): boolean {
	return Boolean(cand && (isProven(cand) || userMapped));
}

/** N107: no Detect button while the import catalog of a picked platform loads, nor when the platform has its own form. */
export function showDetectButton(
	platform: string,
	importable: unknown,
	catalog: { isLoading: boolean; isError: boolean },
	none = "none",
): boolean {
	if (importable) return false;
	return !(platform !== none && catalog.isLoading);
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
	// The pick question stays, so the choice can be changed (p02-3); the view marks the picked field.
	return { ...c, mapping };
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

/** What the Connect page holds after a platform is picked: the platform and nothing from before. */
export interface PlatformChoice {
	platform: string;
	det: ConnectDetectResponse | undefined;
	cand: ConnectCandidate | undefined;
	saved: ConnectSaveResponse | undefined;
	inline: ConnectVerifyStatus | undefined;
	jobId: string | undefined;
	polling: boolean;
	probeSecrets: Record<string, string>;
}

/** Picking a platform clears the earlier detect result, candidate, saved agent and verify job. */
export function platformChoice(platform: string): PlatformChoice {
	return {
		platform,
		det: undefined,
		cand: undefined,
		saved: undefined,
		inline: undefined,
		jobId: undefined,
		polling: false,
		probeSecrets: {},
	};
}

/** Connect platform ids that the import catalog spells differently. */
const IMPORT_PLATFORM_IDS: Record<string, string> = { bedrock_agentcore: "agentcore" };

/** The import platform (with its own fields) behind a Connect platform id, if there is one. */
export function importPlatformFor(id: string, platforms: AgentPlatform[]): AgentPlatform | undefined {
	const want = IMPORT_PLATFORM_IDS[id] ?? id;
	return platforms.find((p) => p.id === want);
}

/** The line above the verify rows: the real task's benchmark, flagged when it is only a practice one. */
export function verifyBenchmarkKey(suite: string | undefined): "verify.benchmark" | "verify.benchmarkPractice" | "verify.noBenchmark" {
	if (!suite) return "verify.noBenchmark";
	return isPracticeSuite(suite.split("@")[0]) ? "verify.benchmarkPractice" : "verify.benchmark";
}