import { describe, expect, it } from "vitest";

// Every English locale value (wave 4, N78): a command name or a flag may appear only in text shown inside a
// "From a terminal" block. Keys listed here are known hits; each names the lane that fixes it, or says that the
// text is a terminal hint that stays. The final integrator removes the lines of lanes whose fix is merged.
const ALLOWED = new Set<string>([
	"admin:notAdmin", // terminal hint, stays
	"analysis:audit.tooLarge", // terminal hint, stays
	"bench:integrity.monitors.none", // terminal hint, stays
	"bench:integrity.monitors.noneAll", // terminal hint, stays
	"bench:releases.timeline.chainBrokenHint", // terminal hint, stays
	"bench:policy.none", // terminal hint, stays
	"bench:canaries.empty", // terminal hint, stays
	"bench:suites.broken.hint", // terminal hint, stays
	"bench:suites.add.task.taskInit", // terminal hint, stays
	"bench:agents.empty", // terminal hint, stays
	"bench:quickstart.minedHint", // terminal hint, stays
	"bench:quickstart.noMined", // terminal hint, stays
	"bench:migration.noExperiments", // terminal hint, stays
	"byoworker:kubernetes.imageHint", // runner image text for a terminal, stays
	"cliConnect:device.subtitle", // the command line sign-in page, stays
	"cliConnect:device.notFoundBody", // the command line sign-in page, stays
	"cliConnect:device.localBody", // the command line sign-in page, stays
	"cliConnect:ide.subtitle", // the editor set-up page, stays
	"cliConnect:ide.step1", // the editor set-up page, stays
	"cliConnect:ide.noSecret", // the editor set-up page, stays
	"connectTraces:draft.yamlHint", // terminal hint, stays
	"files:sync.rules", // terminal hint, stays
	"gw16:live.terminal", // terminal hint, stays
	"identity:admin.modeHelp.closed", // server admin text, stays
	"identity:admin.deliveryLog", // server admin text, stays
	"improve:empty.cli", // terminal copy block, stays
	"scenarioBuild:mock.help", // terminal hint, stays
	"ux13:canaries.cli", // terminal hint, stays
]);

const COMMAND = /\b(bifrost-bench|heimdall) [a-z]+/;
const FLAG = / -[a-z][a-z-]+/;

const files = import.meta.glob("../locales/en/*.json", { eager: true, import: "default" }) as Record<string, unknown>;

function hits(): string[] {
	const out: string[] = [];
	const walk = (ns: string, value: unknown, path: string) => {
		if (typeof value === "string") {
			if (COMMAND.test(value) || FLAG.test(value)) out.push(`${ns}:${path}`);
		} else if (value && typeof value === "object") {
			for (const [k, v] of Object.entries(value)) walk(ns, v, path ? `${path}.${k}` : k);
		}
	};
	for (const [file, json] of Object.entries(files)) walk(file.replace(/^.*\/([^/]+)\.json$/, "$1"), json, "");
	return out.sort();
}

describe("locale text names no command line tool", () => {
	it("finds every locale file", () => {
		expect(Object.keys(files).length).toBeGreaterThan(10);
	});
	it("has no command name or flag outside the known terminal hints", () => {
		expect(hits().filter((k) => !ALLOWED.has(k))).toEqual([]);
	});
});