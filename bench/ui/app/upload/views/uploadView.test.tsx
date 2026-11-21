import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { checkZipFile, UploadResultView } from "./uploadView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("upload view", () => {
	it("shows imported and refused experiments with the refusal reason", () => {
		const html = renderToStaticMarkup(
			<UploadResultView
				result={{
					kind: "runs",
					report: {
						imported: 1,
						skipped: 0,
						refused: 1,
						runs: 4,
						experiments: [
							{ exp_id: "e_ok", runs: 4, status: "imported" },
							{ exp_id: "e_bad", runs: 0, status: "refused", reason: "run r1: seal mismatch" },
						],
					},
				}}
			/>,
		);
		expect(html).toContain('data-testid="upload-exp-e_ok"');
		expect(html).toContain("seal mismatch");
		expect(html).toContain("1 added, 0 skipped, 1 refused (4 saved runs)");
	});

	it("lists refused project files", () => {
		const html = renderToStaticMarkup(
			<UploadResultView
				result={{
					kind: "project",
					report: { written: [{ area: "agents", path: "a.yaml", size: 3 }], refused: [{ area: "policies", path: "p.yaml", error: "invalid" }], skipped: [] },
				}}
			/>,
		);
		expect(html).toContain('data-testid="upload-files-refused"');
		expect(html).toContain("policies/p.yaml: invalid");
		expect(html).toContain("agents/a.yaml");
	});

	it("refuses non-zip and oversized files before upload", () => {
		expect(checkZipFile({ name: "a.tar", size: 1 })).toBe("notZip");
		expect(checkZipFile({ name: "a.zip", size: 65 << 20 })).toBe("tooLarge");
		expect(checkZipFile({ name: "A.ZIP", size: 10 })).toBeNull();
	});
});
