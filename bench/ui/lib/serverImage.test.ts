import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

// The hosted server image builds the UI in a Docker stage of its own
// (deploy/hosted/Dockerfile.server) that starts from ui/ alone, and its
// `npm run build` typechecks every file, tests included. A UI file that
// imports from outside ui/ therefore builds there only when that stage copies
// the file in at the same relative path and the build context filter
// (Dockerfile.server.dockerignore) lets it through. (S1-fix: the live scoring
// fixtures broke the hosted image build this way.)
const uiRoot = path.resolve(__dirname, "..");
const benchRoot = path.resolve(uiRoot, "..");
const importRe = /^\s*(?:import|export)\s[^"';]*?from\s+["']([^"']+)["']/gm;

function sources(dir: string, out: string[] = []): string[] {
	for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
		if (entry.name === "node_modules" || entry.name === "dist" || entry.name.startsWith(".")) continue;
		const p = path.join(dir, entry.name);
		if (entry.isDirectory()) sources(p, out);
		else if (/\.(ts|tsx|mts)$/.test(entry.name)) out.push(p);
	}
	return out;
}

function outsideImports(): Map<string, string> {
	const found = new Map<string, string>(); // bench-relative target -> importing file
	for (const file of sources(uiRoot)) {
		for (const m of fs.readFileSync(file, "utf8").matchAll(importRe)) {
			if (!m[1].startsWith(".")) continue;
			const target = path.resolve(path.dirname(file), m[1]);
			if (!target.startsWith(uiRoot + path.sep)) found.set(path.relative(benchRoot, target), path.relative(benchRoot, file));
		}
	}
	return found;
}

const under = (rel: string, dir: string) => rel === dir || rel.startsWith(dir.replace(/\/$/, "") + "/");

describe("hosted server image UI stage", () => {
	it("copies in every file the UI imports from outside ui/", () => {
		const dockerfile = fs.readFileSync(path.join(benchRoot, "deploy/hosted/Dockerfile.server"), "utf8");
		const ignore = fs.readFileSync(path.join(benchRoot, "deploy/hosted/Dockerfile.server.dockerignore"), "utf8");
		const uiStage = dockerfile.split(/^FROM /m).find((stage) => /\sAS ui\s*$/m.test(stage.split("\n")[0])) ?? "";
		expect(uiStage).not.toBe("");
		// COPY <bench path> /src/<same path>: the stage's /src is the bench root.
		const copies = [...uiStage.matchAll(/^COPY\s+(\S+)\s+\/src\/(\S+)\s*$/gm)].filter((m) => m[1] === m[2]).map((m) => m[1]);
		const allowed = ignore
			.split("\n")
			.filter((line) => line.startsWith("!"))
			.map((line) => line.slice(1).trim());
		for (const [rel, file] of outsideImports()) {
			expect(
				copies.some((c) => under(rel, c)),
				`${file} imports ${rel}: the ui stage of Dockerfile.server does not copy it in`,
			).toBe(true);
			if (/(^|\/)testdata\//.test(rel)) {
				expect(
					allowed.some((a) => under(rel, a)),
					`${file} imports ${rel}: **/testdata is filtered out of the build context without a ! exception for it`,
				).toBe(true);
			}
		}
	});
});