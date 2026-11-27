/**
 * The one list of ways to add a benchmark (M6-41). The header menu on /suites, the chooser on
 * /suites/add and the Import tab all read this list, so every entrance offers the same six.
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
