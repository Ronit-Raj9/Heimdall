import { CATEGORIES, type Category, type SourceFilter } from "./catalogModel";

// X-85: the catalog's category and source filters live in the address (?category=, ?source=),
// like the search term, so a filtered catalog can be shared and survives a reload.

const CATEGORY_SET = new Set<string>([...CATEGORIES, "general"]);
const SOURCES: readonly SourceFilter[] = ["all", "ready", "needs"];

/** The category of ?category=; "all" when it is missing or not a known category. */
export function categoryFromUrl(raw: string | null | undefined): Category | "all" {
	return raw && CATEGORY_SET.has(raw) ? (raw as Category) : "all";
}

/** The source filter of ?source=; "all" when it is missing or unknown. */
export function sourceFromUrl(raw: string | null | undefined): SourceFilter {
	return SOURCES.includes(raw as SourceFilter) ? (raw as SourceFilter) : "all";
}

/** The value to write back: null (drop the parameter) for the default "all". */
export function filterToUrl(value: string): string | null {
	return value === "all" ? null : value;
}
