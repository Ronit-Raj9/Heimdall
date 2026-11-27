import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { STACK_CLASSES, Table, TableBody, TableCell, TableRow } from "./table";

describe("Table stack mode (C2-8)", () => {
	it("stacks rows below sm only when asked, and cells carry their column label", () => {
		const row = (
			<TableBody>
				<TableRow>
					<TableCell label="Score">80%</TableCell>
				</TableRow>
			</TableBody>
		);
		const stacked = renderToStaticMarkup(<Table stack>{row}</Table>);
		expect(stacked).toContain("data-stack");
		expect(stacked).toContain("max-sm:[&amp;_thead]:sr-only");
		expect(stacked).toContain('data-label="Score"');
		const plain = renderToStaticMarkup(<Table>{row}</Table>);
		expect(plain).not.toContain("data-stack");
		expect(plain).not.toContain(STACK_CLASSES.split(" ")[0].replace("&", "&amp;"));
	});
});
