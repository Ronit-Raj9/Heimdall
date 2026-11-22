import "@/lib/i18n";

import type { SuiteMockResult, ScenarioRowsResult } from "@/lib/types/scenarioBuild";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MOCK_ID, MockResult, ScenarioPreview, mockIdFrom, scenarioBody, scenarioFormatOf } from "./scenarioBuild";

describe("scenario rows", () => {
	const v = { format: "jsonl" as const, data: "{}", shape: "", switchGrader: false, provider: "", model: "", heldOut: false, source: "" };
	it("sends only what is set", () => {
		expect(scenarioBody(v)).toEqual({ format: "jsonl", data: "{}" });
		expect(scenarioBody({ ...v, shape: " refund ", switchGrader: true, provider: "openai", model: "gpt-x", heldOut: true, source: "t.jsonl" })).toEqual({
			format: "jsonl",
			data: "{}",
			shape_task: "refund",
			switch_grader: true,
			judge: { provider: "openai", model: "gpt-x" },
			held_out: true,
			source: "t.jsonl",
		});
		expect(scenarioBody({ ...v, provider: "openai", model: "gpt-x" })).not.toHaveProperty("judge");
	});
	it("reads the format from the file name", () => {
		expect(scenarioFormatOf("Tickets.CSV")).toBe("csv");
		expect(scenarioFormatOf("tickets.jsonl")).toBe("jsonl");
	});
	it("previews tasks, their checks and the rows with problems", () => {
		const result: ScenarioRowsResult = {
			suite: "desk",
			shape: "refund-in-policy",
			grader: "checks",
			grader_changed: false,
			dry_run: true,
			tasks: [{ line: 1, id: "dup-charge", title: "Dup", opening: "I was charged twice", check: "", checks: 2 }],
			problems: [{ line: 2, message: "opening_message is empty" }],
			next: "",
		};
		const html = renderToStaticMarkup(<ScenarioPreview result={result} />);
		expect(html).toContain("1 rows become tasks, 1 rows have problems.");
		expect(html).toContain("dup-charge");
		expect(html).toContain("2 checks");
		expect(html).toContain("Row 2: opening_message is empty");
	});
});

describe("OpenAPI mock", () => {
	it("makes a valid service name from a file name", () => {
		expect(mockIdFrom("Acme CRM.v2.yaml")).toBe("acme-crm-v2");
		expect(mockIdFrom("2024-orders.json")).toBe("orders");
		expect(MOCK_ID.test(mockIdFrom("Acme CRM.v2.yaml"))).toBe(true);
		expect(MOCK_ID.test("Orders")).toBe(false);
	});
	it("shows the operations and what a task adds", () => {
		const result: SuiteMockResult = {
			suite: "desk",
			id: "orders",
			folder: "services/orders",
			dry_run: false,
			replaced: false,
			operations: [{ name: "get_order", method: "GET", path: "/orders/{order_id}", kind: "get", collection: "orders" }],
			collections: ["orders"],
			warnings: [],
			service_yaml: "  - id: orders\n",
			seed: '{"orders": []}\n',
			next: "Add the services entry",
		};
		const html = renderToStaticMarkup(<MockResult result={result} />);
		expect(html).toContain("Wrote services/orders.");
		expect(html).toContain("1 operation over orders");
		expect(html).toContain("read one");
		expect(html).toContain("- id: orders");
	});
});
