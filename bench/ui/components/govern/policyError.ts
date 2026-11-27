/**
 * The error a rule set file shows (Q-265). policies/schedules.yaml sits beside the rule sets but
 * holds schedules, so its schema error is replaced with a plain line; any other file keeps the
 * server's error. Pure.
 */
export function policyErrorText(file: string, error: string | undefined, t: (key: string) => string): string | undefined {
	const base = file.split("/").pop() ?? file;
	if (/^schedules\.ya?ml$/.test(base)) return t("policies.notRuleSet");
	return error;
}
