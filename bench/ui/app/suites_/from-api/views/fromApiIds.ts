// Pure helpers of the From API form (M6-57): ids drafted from the spec's own title, and which
// field is wrong.

export const SUITE_ID_RE = /^[a-z0-9][a-z0-9._-]{0,62}$/;
export const APP_ID_RE = /^[a-z][a-z0-9_-]{0,31}$/;

/** The `info.title` of an OpenAPI file, JSON or YAML; "" when there is none. */
export function specTitle(spec: string): string {
	const text = spec.trim();
	if (!text) return "";
	if (text.startsWith("{")) {
		try {
			const v = JSON.parse(text) as { info?: { title?: unknown } };
			return typeof v.info?.title === "string" ? v.info.title.trim() : "";
		} catch {
			return "";
		}
	}
	// YAML: the first indented title: under a top-level info: block.
	const m = /^info:\s*\n((?:[ \t]+.*\n?)*)/m.exec(text);
	if (!m) return "";
	const t = /^[ \t]+title:\s*(.+?)\s*$/m.exec(m[1]);
	return t ? t[1].replace(/^["']|["']$/g, "").trim() : "";
}

/** "Shop API" becomes "shop". Words such as api, service and server are dropped; empty when nothing is left. */
export function appIdFrom(title: string): string {
	const words = title
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, " ")
		.trim()
		.split(/\s+/)
		.filter((w) => w && !["api", "apis", "service", "server", "openapi", "the", "v1", "v2", "v3"].includes(w));
	const id = words.join("_").replace(/^[^a-z]+/, "").slice(0, 32).replace(/_+$/, "");
	return APP_ID_RE.test(id) && id !== "customer" ? id : "";
}

/** The benchmark id for an app: "shop" becomes "shop-support". */
export function suiteIdFrom(app: string): string {
	if (!app) return "";
	const id = `${app.replace(/_/g, "-")}-support`.slice(0, 63);
	return SUITE_ID_RE.test(id) ? id : "";
}

export type IdField = "benchmark" | "app" | "appReserved";

/** The fields that are wrong, in form order; empty fields count only once the other was typed. */
export function idProblems(suite: string, app: string): IdField[] {
	const out: IdField[] = [];
	if (!SUITE_ID_RE.test(suite)) out.push("benchmark");
	if (app === "customer") out.push("appReserved");
	else if (!APP_ID_RE.test(app)) out.push("app");
	return out;
}

/** A cost cap as typed: empty is the default, else a number above 0. */
export function maxUsdProblem(text: string): boolean {
	if (text.trim() === "") return false;
	const n = Number(text);
	return !Number.isFinite(n) || n <= 0;
}
