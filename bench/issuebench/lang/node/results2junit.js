// Converts a jest or vitest JSON report to JUnit XML on stdout.
// Usage: node results2junit.js <report.json>
// Each test case is classname = the test file relative to the working
// directory, name = its describe titles and its title joined by " > ".
// Standard library only.
"use strict";

const fs = require("fs");
const path = require("path");

function esc(s) {
	return String(s)
		.replace(/&/g, "&amp;")
		.replace(/</g, "&lt;")
		.replace(/>/g, "&gt;")
		.replace(/"/g, "&quot;")
		.replace(/'/g, "&apos;");
}

function relFile(name) {
	if (!name) {
		return "";
	}
	let rel = path.isAbsolute(name) ? path.relative(process.cwd(), name) : name;
	return rel.split(path.sep).join("/");
}

function main() {
	const file = process.argv[2];
	let report = { testResults: [] };
	try {
		report = JSON.parse(fs.readFileSync(file, "utf8"));
	} catch (e) {
		process.stderr.write("results2junit: could not read " + file + ": " + e.message + "\n");
	}
	const out = ['<?xml version="1.0" encoding="UTF-8"?>', "<testsuites>"];
	for (const suite of report.testResults || []) {
		const cls = relFile(suite.name || suite.testFilePath || "");
		out.push('<testsuite name="' + esc(cls) + '">');
		for (const a of suite.assertionResults || suite.testResults || []) {
			const titles = (a.ancestorTitles || []).filter((t) => t !== "").concat([a.title || ""]);
			const name = titles.join(" > ");
			const head = '<testcase classname="' + esc(cls) + '" name="' + esc(name) + '"';
			if (a.status === "failed") {
				out.push(head + "><failure/></testcase>");
			} else if (a.status === "pending" || a.status === "skipped" || a.status === "todo") {
				out.push(head + "><skipped/></testcase>");
			} else {
				out.push(head + "/>");
			}
		}
		out.push("</testsuite>");
	}
	out.push("</testsuites>");
	process.stdout.write(out.join("\n") + "\n");
}

main();
