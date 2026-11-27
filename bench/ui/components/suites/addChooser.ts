/**
 * The one list of ways to add a benchmark (M6-41). The header menu on /suites and the chooser on
 * /suites/add both read this list, so every entrance offers the same six (the Import tab of /suites
 * is gone, decision 5; its importers are in the catalog).
 */
export type AddEntryId = "file" | "catalog" | "api" | "create" | "code" | "failures";

export interface AddEntry {
	id: AddEntryId;
	to: "/suites/add" | "/suites" | "/suites/from-api" | "/suites/new" | "/inbox";
	search?: Record<string, string>;
}

export const ADD_ENTRIES: readonly AddEntry[] = [
	{ id: "file", to: "/suites/add" },
	{ id: "catalog", to: "/suites", search: { tab: "catalog" } },
	{ id: "api", to: "/suites/from-api" },
	{ id: "create", to: "/suites/new" },
	{ id: "code", to: "/suites/new", search: { start: "code" } },
	{ id: "failures", to: "/inbox" },
];
