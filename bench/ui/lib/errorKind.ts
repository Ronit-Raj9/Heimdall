import { errorStatus } from "@/lib/store/apis/session";

/**
 * The kinds of the shared error pattern (DC_feedback.md section 7). Each has a "what" and a "why"
 * sentence in locales/en/ufs2.json under errors.<kind>, so no screen prints a raw status code.
 */
export type ErrorKind =
	| "network"
	| "timeout"
	| "signedOut"
	| "forbidden"
	| "refused"
	| "notFound"
	| "conflict"
	| "invalid"
	| "tooLarge"
	| "planLimit"
	| "rateLimited"
	| "server"
	| "workerRefused"
	| "noModelKey"
	| "budgetReached"
	| "imagePreparing"
	| "unknown";

// Server error types (data.error.type) that name a kind of their own, before the status decides.
const BY_TYPE: Record<string, ErrorKind> = {
	plan_limit: "planLimit",
	worker_refused: "workerRefused",
	no_model_key: "noModelKey",
	missing_model_key: "noModelKey",
	budget_reached: "budgetReached",
	budget_exceeded: "budgetReached",
	image_preparing: "imagePreparing",
};

/** The kind of a failed RTK Query request (or any thrown value). */
export function errorKind(error: unknown): ErrorKind {
	if (!error || typeof error !== "object") return "unknown";
	const type = (error as { data?: { error?: { type?: unknown } } }).data?.error?.type;
	if (typeof type === "string" && BY_TYPE[type]) return BY_TYPE[type];
	const raw = (error as { status?: unknown }).status;
	if (raw === "FETCH_ERROR") return "network";
	if (raw === "TIMEOUT_ERROR") return "timeout";
	const code = errorStatus(error);
	if (code === undefined) return "unknown";
	if (code === 401) return "signedOut";
	if (code === 402) return "planLimit";
	if (code === 403) return "forbidden";
	if (code === 404 || code === 410) return "notFound";
	if (code === 409) return "conflict";
	if (code === 400 || code === 422) return "invalid";
	if (code === 413) return "tooLarge";
	if (code === 429) return "rateLimited";
	if (code >= 500) return "server";
	return "unknown";
}

/** True when pressing Retry can change the answer. */
export function kindRetryable(kind: ErrorKind): boolean {
	return kind === "network" || kind === "timeout" || kind === "rateLimited" || kind === "server" || kind === "unknown" || kind === "imagePreparing";
}
