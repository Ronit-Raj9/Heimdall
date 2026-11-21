import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { Markdown, parseBlocks, parseCallout, safeHref, slugify } from "./markdown";

describe("Markdown", () => {
	it("parses the common blocks", () => {
		const blocks = parseBlocks("# Title\n\nSome *text*.\n\n```sh\nmake\n```\n\n- a\n- b\n\n| x | y |\n|---|---|\n| 1 | 2 |\n\n> note\n\n---\n");
		expect(blocks.map((b) => b.kind)).toEqual(["heading", "para", "code", "list", "table", "quote", "rule"]);
	});

	it("never renders raw HTML or script links", () => {
		const html = renderToStaticMarkup(<Markdown source={'<script>alert(1)</script>\n\n[x](javascript:alert(1)) [y](https://e.com) `<b>`'} />);
		expect(html).not.toContain("<script>");
		expect(html).not.toContain("javascript:");
		expect(html).toContain('href="https://e.com"');
		expect(html).toContain("&lt;b&gt;");
	});

	it("turns guide links into in-place links and slugs headings", () => {
		const html = renderToStaticMarkup(<Markdown source={"## Run it now\n\nSee [runners](runners.md#setup)."} onDocLink={() => undefined} />);
		expect(html).toContain('id="run-it-now"');
		expect(html).toContain('href="?doc=runners"');
		expect(safeHref("data:text/html,x")).toBeNull();
		expect(slugify("Hello `World`!")).toBe("hello-world");
	});

	it("renders callouts and code blocks with a copy button", () => {
		expect(parseCallout("[!WARNING] careful")).toEqual({ kind: "warning", label: "Warning", body: "careful" });
		expect(parseCallout("**Note:** read this")?.kind).toBe("note");
		expect(parseCallout("just a quote")).toBeNull();
		const html = renderToStaticMarkup(<Markdown source={"> [!TIP] use it\n\n```sh\nmake\n```"} />);
		expect(html).toContain('data-callout="tip"');
		expect(html).toContain('data-testid="code-copy"');
	});
});
