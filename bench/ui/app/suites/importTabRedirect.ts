/**
 * Decision 5 (M6-49): the Import tab of /suites is gone; its importers are in the Catalog tab, each
 * with its command line text. Old links to ?tab=import land on the catalog with the rest of their query.
 */
export function importTabRedirect(search: Record<string, unknown>): { to: "/suites"; search: Record<string, unknown> } | null {
	if (search.tab !== "import") return null;
	return { to: "/suites", search: { ...search, tab: "catalog" } };
}
