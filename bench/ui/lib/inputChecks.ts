import i18n from "@/lib/i18n";
import { SUITE_ID_RE } from "@/components/onboard/onboard.utils";

/** Why raw is not a whole number in [min, max], or null when it is. raw is the input's string value. */
export function wholeNumberProblem(raw: string | number, min: number, max: number): string | null {
	const s = String(raw).trim();
	const n = Number(s);
	if (s === "" || !Number.isInteger(n) || n < min || n > max) return i18n.t("inputs.whole", { ns: "w4m0", min, max });
	return null;
}

/** Why id is not a valid id (benchmark, rule set, server name), or null. Same rule as the server: SUITE_ID_RE. */
export function idProblem(id: string, what: string): string | null {
	if (id.trim() === "") return i18n.t("inputs.idEmpty", { ns: "w4m0", what });
	if (!SUITE_ID_RE.test(id)) return i18n.t("inputs.idFormat", { ns: "w4m0", what });
	return null;
}

/** A whole number of at least 0 from a number box: 3.7 becomes 3, -5 becomes 0, "" becomes 0. */
export function wholeAtLeastZero(raw: string | number): number {
	const n = Math.trunc(Number(raw));
	return Number.isFinite(n) && n > 0 ? n : 0;
}
