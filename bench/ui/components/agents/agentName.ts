import i18n from "@/lib/i18n";
import "./gw17I18n";

/** The agent id rule, the same as the server's (handlers/agentbuilder.go agentIDRe). */
export const AGENT_ID_RE = /^[a-z0-9][a-z0-9._-]{0,62}$/;
export const AGENT_ID_MAX = 63;

/**
 * Why name cannot be used for a new agent, or "" when it can. The problems are checked in order: empty, too long
 * (with the count), a bad first character, any other bad character, then a name another agent already uses.
 */
export function agentNameProblem(name: string, taken: ReadonlySet<string>): string {
	if (name === "") return i18n.t("name.empty", { ns: "gw17" });
	if (name.length > AGENT_ID_MAX) return i18n.t("name.tooLong", { ns: "gw17", count: name.length });
	if (!/^[a-z0-9]/.test(name)) return i18n.t("name.start", { ns: "gw17" });
	if (!AGENT_ID_RE.test(name)) return i18n.t("new.nameInvalid", { ns: "agentBuilder" });
	if (taken.has(name)) return i18n.t("new.nameExists", { ns: "agentBuilder", name });
	return "";
}