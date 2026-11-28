import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { RAW_EVIDENCE, RUN_FILES, RunFileDownloads, runFileUrl } from "./runFileDownloads";

describe("run file downloads", () => {
	it("requests the run file and raw evidence routes with escaped segments", () => {
		expect(runFileUrl("r/1", "files", "grade.json")).toContain("/runs/r%2F1/files/grade.json");
		expect(runFileUrl("r1", "evidence", "gateway.raw.jsonl")).toContain("/runs/r1/evidence/gateway.raw.jsonl");
	});
	it("offers raw evidence only on a sealed run", () => {
		const sealed = renderToStaticMarkup(<RunFileDownloads runId="r1" sealed />);
		for (const n of [...RUN_FILES, ...RAW_EVIDENCE]) expect(sealed).toContain(n);
		const open = renderToStaticMarkup(<RunFileDownloads runId="r1" sealed={false} />);
		expect(open).not.toContain("gateway.raw.jsonl");
		expect(open).toContain("once the run is sealed");
	});
});