import { parseAsString, useQueryState } from "nuqs";

/**
 * Test objects (Spec 10, M1-18): the agents, evaluations, comparisons and benchmarks that the
 * project's own checks create ("zz-test-..." ids, "Zz test" names, stub agents). Lists hide them by
 * default and offer a "Show test items (n)" chip that sets ?tests=1.
 */

/** Words in a description or notes that mark a stand-in agent or object. */
export const TEST_DOUBLE_TEXT = /\b(test[ -]?double|stub agent|fake agent|mock agent|demo agent|for (the )?example benchmarks?)\b/i;

const TEST_PREFIX = /^(zz[-_ ]test\b|zz-test-)/i;

/** Whether a name or id belongs to a test object; extra text (description, notes) is checked for stand-in words. Pure. */
export function isTestObject(name: string | null | undefined, extra?: { description?: string | null; notes?: string | null }): boolean {
	if (name && TEST_PREFIX.test(name.trim())) return true;
	if (!extra) return false;
	return TEST_DOUBLE_TEXT.test(`${extra.description ?? ""} ${extra.notes ?? ""}`);
}

/** Splits a list into the items to show and the count of hidden test items. Pure. */
export function withoutTests<T>(items: readonly T[], isTest: (item: T) => boolean, showTests: boolean): { shown: T[]; hidden: number } {
	if (showTests) return { shown: [...items], hidden: items.filter(isTest).length };
	const shown = items.filter((item) => !isTest(item));
	return { shown, hidden: items.length - shown.length };
}

/** The "show test items" switch, kept in the URL as ?tests=1 so a shared link keeps it. */
export function useShowTests(): [boolean, (on: boolean) => void] {
	const [raw, setRaw] = useQueryState("tests", parseAsString);
	return [raw === "1", (on: boolean) => void setRaw(on ? "1" : null)];
}
