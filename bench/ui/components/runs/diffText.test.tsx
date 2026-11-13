import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { DiffText } from "./diffText";

describe("DiffText", () => {
	it("renders the patch as inert monospace text", () => {
		const html = renderToStaticMarkup(
			<DiffText
				arm="A"
				runDiff={{
					run_id: "r",
					diff: {
						path: "workspace.diff",
						sha256: "x",
						files: [{ path: "src/a.py", status: "modified" }],
						rejected: [{ path: "conftest.py", reason: "cache" }],
					},
					patch: "--- a/src/a.py\n+++ b/src/a.py\n@@ -1 +1 @@\n-x = 1\n+x = '<script>alert(1)</script>'\n",
				}}
			/>,
		);
		expect(html).toContain('data-testid="diff-A-patch"');
		expect(html).not.toContain("<script>");
		expect(html).toContain("&lt;script&gt;");
		expect(html).toContain("src/a.py");
		expect(html).toContain('data-testid="diff-A-rejected"');
	});

	it("says so when the run changed nothing", () => {
		const html = renderToStaticMarkup(<DiffText arm="B" runDiff={{ run_id: "r", diff: { path: "", sha256: "", files: [] }, patch: "" }} />);
		expect(html).toContain('data-testid="diff-B-empty"');
	});
});