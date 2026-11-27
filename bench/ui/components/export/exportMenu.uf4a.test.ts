import { describe, expect, it } from "vitest";
import { experimentExportFiles } from "./exportMenu";

// M4-25: the header's downloads, spreadsheet first, each with its file name.
describe("experimentExportFiles", () => {
	it("names the three files", () => {
		expect(experimentExportFiles("e1")).toEqual([
			{ format: "csv", file: "e1.runs.csv" },
			{ format: "jsonl", file: "e1.runs.jsonl" },
			{ format: "eee", file: "e1.eee.jsonl" },
		]);
	});
});