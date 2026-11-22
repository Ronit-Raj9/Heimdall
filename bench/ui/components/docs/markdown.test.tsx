import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import "@/lib/i18n";

import {
	hostedDocSource,
	isInternalDoc,
	Markdown,
	parseBlocks,
	parseCallout,
	relativeDocTarget,
	repoFileUrl,
	safeHref,
	slugify,
} from "./markdown";

describe("Markdown", () => {
	it("parses the common blocks", () => {
		const blocks = parseBlocks(
			"# Title\n\nSome *text*.\n\n```sh\nmake\n```\n\n- a\n- b\n\n| x | y |\n|---|---|\n| 1 | 2 |\n\n> note\n\n---\n",
		);
		expect(blocks.map((b) => b.kind)).toEqual(["heading", "para", "code", "list", "table", "quote", "rule"]);
	});

	it("never renders raw HTML or script links", () => {
		const html = renderToStaticMarkup(
			<Markdown source={"<script>alert(1)</script>\n\n[x](javascript:alert(1)) [y](https://e.com) `<b>`"} />,
		);
		expect(html).not.toContain("<script>");
		expect(html).not.toContain("javascript:");
		expect(html).toContain('href="https://e.com"');
		expect(html).toContain("&lt;b&gt;");
	});

	it("turns guide links into in-place links and slugs headings", () => {
		const html = renderToStaticMarkup(
			<Markdown source={"## Run it now\n\nSee [runners](runners.md#setup)."} onDocLink={() => undefined} />,
		);
		expect(html).toContain('id="run-it-now"');
		expect(html).toContain('href="?doc=runners"');
		expect(safeHref("data:text/html,x")).toBeNull();
		expect(slugify("Hello `World`!")).toBe("hello-world");
	});

	it("renders links to internal design notes as plain text", () => {
		const html = renderToStaticMarkup(
			<Markdown source={"See [design](DESIGN.md), [plan](platform-design.md#x) and [runners](runners.md)."} onDocLink={() => undefined} />,
		);
		expect(html).not.toContain("?doc=DESIGN");
		expect(html).not.toContain("?doc=platform-design");
		expect(html).toContain("design");
		expect(html).toContain('href="?doc=runners"');
		expect(isInternalDoc("CLI_WEB_PARITY")).toBe(true);
		expect(isInternalDoc("spike")).toBe(true);
		expect(isInternalDoc("hosting")).toBe(false);
	});

	// G3: links out of the docs folder point at files the server does not serve. N137: the other
	// ones open the file on GitHub.
	it("turns the schema and example links into in-app guide links, and other ../ links into GitHub links", () => {
		const source =
			"See [`spec/agent-event.schema.json`](../spec/agent-event.schema.json), [schema](../spec/agent-event.schema.json#x), " +
			"[`examples/acme-node`](../examples/acme-node/README.md) and [other](../examples/acme-web/README.md).";
		const opened: string[] = [];
		const html = renderToStaticMarkup(<Markdown source={source} onDocLink={(name) => opened.push(name)} />);
		expect(html).toContain('href="/docs?doc=agent-event-schema"');
		expect(html).toContain('href="/docs?doc=example-acme-node"');
		expect(html).not.toContain("../spec");
		expect(html).not.toContain("../examples");
		expect(html).toContain("other");
		expect(html.match(/<a /g)?.length).toBe(4);
		expect(html).toContain('href="https://github.com/Ronit-Raj9/Heimdall/blob/main/bench/examples/acme-web/README.md"');
		// Without an in-place handler (the public site) the links still go to the app's docs page.
		const plain = renderToStaticMarkup(<Markdown source={source} />);
		expect(plain).toContain('href="/docs?doc=agent-event-schema"');
		expect(plain).toContain('href="/docs?doc=example-acme-node"');
		expect(plain).not.toContain("../examples/acme-web");
		expect(plain).toContain("bench/examples/acme-web/README.md");
		expect(relativeDocTarget("../spec/agent-event.schema.json")).toBe("agent-event-schema");
		expect(relativeDocTarget("../examples/acme-node/README.md#setup")).toBe("example-acme-node");
		expect(relativeDocTarget("../examples/acme-web/README.md")).toBeNull();
	});

	// N137: repository files the guides link to open on GitHub, at main.
	it("sends relative links to repository files to GitHub", () => {
		const base = "https://github.com/Ronit-Raj9/Heimdall/blob/main/";
		expect(repoFileUrl("../examples/acme-web/README.md")).toBe(`${base}bench/examples/acme-web/README.md`);
		expect(repoFileUrl("probes/tool-poison.yaml")).toBe(`${base}bench/docs/probes/tool-poison.yaml`);
		expect(repoFileUrl("./results/a/report.md#top")).toBe(`${base}bench/docs/results/a/report.md#top`);
		expect(repoFileUrl("../spec/agent-event.schema.json")).toBeNull();
		expect(repoFileUrl("hosting.md")).toBeNull();
		expect(repoFileUrl("hosting.md#setup")).toBeNull();
		expect(repoFileUrl("../../../etc/passwd")).toBeNull();
		expect(repoFileUrl("https://example.com/x")).toBeNull();
		expect(repoFileUrl("#anchor")).toBeNull();
		expect(repoFileUrl("/docs")).toBeNull();
		const html = renderToStaticMarkup(<Markdown source="See [poison](probes/tool-poison.yaml) and [x](../../../etc/passwd)." />);
		expect(html).toContain(`href="${base}bench/docs/probes/tool-poison.yaml"`);
		expect(html).toContain('target="_blank"');
		expect(html).toContain("on GitHub");
		expect(html).not.toContain("etc/passwd");
		// The in-app guide links stay in the app.
		expect(renderToStaticMarkup(<Markdown source="[s](../spec/agent-event.schema.json)" />)).toContain(
			'href="/docs?doc=agent-event-schema"',
		);
	});

	// N136: a hosted reader's samples point at this server, not a developer's local one.
	it("rewrites local server addresses for a hosted reader", () => {
		const src =
			"curl http://127.0.0.1:8090/api and http://localhost:8090/verdict?x=1 but not http://127.0.0.1:8080/ or http://localhost:80901";
		const out = hostedDocSource(src, "https://heimdall.example");
		expect(out.replaced).toBe(2);
		expect(out.text).toBe(
			"curl https://heimdall.example/api and https://heimdall.example/verdict?x=1 but not http://127.0.0.1:8080/ or http://localhost:80901",
		);
		expect(hostedDocSource("nothing", "https://h.example/").replaced).toBe(0);
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