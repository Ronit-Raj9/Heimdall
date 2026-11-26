import i18n from "@/lib/i18n";
import { errorStatus } from "@/lib/store/apis/session";

/**
 * The sentence for a request that got no usable answer: no network, a time-out, or an answer
 * that is not JSON (a proxy's plain-text 413 or 502). Null when the server answered in JSON;
 * the caller then shows the server's own message.
 */
export function transportErrorText(error: unknown): string | null {
	if (!error || typeof error !== "object" || !("status" in error)) return null;
	const status = (error as { status: unknown }).status;
	if (status === "FETCH_ERROR") return i18n.t("errors.offline", { ns: "w4m0" });
	if (status === "TIMEOUT_ERROR") return i18n.t("errors.timeout", { ns: "w4m0" });
	if (status === "PARSING_ERROR") {
		const code = errorStatus(error);
		if (code === 413) return i18n.t("errors.tooLarge", { ns: "w4m0" });
		if (code === 429) return i18n.t("errors.rateLimitedNoWait", { ns: "bench" });
		return i18n.t("errors.unreadable", { ns: "w4m0", status: code ?? "?" });
	}
	return null;
}

/** False for answers a retry cannot change (400, 401, 403, 404, 409, 413, 422); true otherwise. */
export function canRetry(error: unknown): boolean {
	const code = errorStatus(error);
	return code === undefined || ![400, 401, 403, 404, 409, 413, 422].includes(code);
}
