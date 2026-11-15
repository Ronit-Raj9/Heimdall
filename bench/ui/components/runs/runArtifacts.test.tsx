import "@/lib/i18n";

import type { ArtifactFile, RunDetail } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ArtifactFiles, artifactDownloadUrl, isExported } from "./runArtifacts";
import { RunScenarioEvidence } from "./runScenario";

const FIXTURES = path.resolve(__dirname, "../../public/fixtures");
/** A plain diff run, as the fixtures hold it. */
const plain = JSON.parse(fs.readFileSync(path.join(FIXTURES, "runs.r_fmw1z7ouim.json"), "utf8")) as RunDetail;

const files: ArtifactFile[] = [
	{
		id: "report",
		name: "report.csv",
		path: "artifacts/report/report.csv",
		source: "out/report.csv",
		sha256: "7777777777777777777777777777777777777777777777777777777777777777",
		bytes: 5_400_000,
	},
	{
		id: "chart",
		name: "<img src=x onerror=alert(1)>.svg",
		path: "artifacts/chart/x.svg",
		source: "out/chart.svg",
		sha256: "8888",
		bytes: 512,
	},
	{ id: "mesh", source: "out/*.obj", bytes: 0, error: "missing" },
	{ id: "big", name: "big.bin", source: "big.bin", bytes: 99_000_000, error: "over the size cap of 32 MiB" },
];

const render = (props: Partial<React.ComponentProps<typeof ArtifactFiles>> = {}) =>
	renderToStaticMarkup(<ArtifactFiles files={files} runId="r_abc" arm="A" sealed {...props} />);

describe("artifactDownloadUrl", () => {
	it("builds the artifact route with every segment encoded", () => {
		expect(artifactDownloadUrl("r_abc", { id: "report", name: "report.csv" })).toBe("/api/bench/runs/r_abc/artifacts/report/report.csv");
		expect(artifactDownloadUrl("r/../x", { id: "a?b", name: "../seal.json#x" })).toBe(
			"/api/bench/runs/r%2F..%2Fx/artifacts/a%3Fb/..%2Fseal.json%23x",
		);
	});
});

describe("isExported", () => {
	it("is true only for a file copied without error", () => {
		expect(isExported(files[0])).toBe(true);
		expect(isExported(files[2])).toBe(false);
		expect(isExported(files[3])).toBe(false);
		expect(isExported({ id: "x", name: "x", source: "x", bytes: 1 })).toBe(false);
	});
});

describe("ArtifactFiles", () => {
	it("lists id, name, size and short hash, with a download link for each exported file", () => {
		const html = render();
		expect(html).toContain('data-testid="run-artifacts-A"');
		expect(html).toContain('aria-label="Artifacts of arm A"');
		expect(html).toMatch(/<th[^>]*scope="col"/);
		const row = html.match(/data-testid="run-artifact-report-report.csv"[\s\S]*?<\/tr>/)?.[0] ?? "";
		expect(row).toContain('data-exported="true"');
		expect(row).toContain(">report<");
		expect(row).toContain("report.csv");
		expect(row).toContain("from out/report.csv");
		expect(row).toContain("5.1 MB");
		expect(row).toContain('title="7777777777777777777777777777777777777777777777777777777777777777"');
		expect(row).toContain("77777777…7777");
		expect(row).toMatch(
			/<a href="\/api\/bench\/runs\/r_abc\/artifacts\/report\/report.csv" download="report.csv" rel="noopener noreferrer" aria-label="Download report.csv \(artifact report\)"/,
		);
	});

	it("never renders an agent-chosen name as markup and encodes it in the link", () => {
		const html = render();
		expect(html).not.toContain("<img");
		expect(html).toContain("&lt;img src=x onerror=alert(1)&gt;.svg");
		expect(html).toContain('href="/api/bench/runs/r_abc/artifacts/chart/%3Cimg%20src%3Dx%20onerror%3Dalert(1)%3E.svg"');
		expect(html).not.toContain("target=");
	});

	it("shows the export error in place of the link", () => {
		const html = render();
		const missing = html.match(/data-testid="run-artifact-mesh"[\s\S]*?<\/tr>/)?.[0] ?? "";
		expect(missing).toContain('data-exported="false"');
		expect(missing).toContain("not exported: missing");
		expect(missing).toContain("from out/*.obj");
		expect(missing).not.toContain("<a ");
		const big = html.match(/data-testid="run-artifact-big-big.bin"[\s\S]*?<\/tr>/)?.[0] ?? "";
		expect(big).toContain("not exported: over the size cap of 32 MiB");
		expect(big).not.toContain("<a ");
		expect(big).not.toContain("MB<");
	});

	it("offers no link before the run is sealed, or when its folder no longer matches the seal", () => {
		const unsealed = render({ sealed: false });
		expect(unsealed).not.toContain("<a ");
		expect(unsealed).toContain("downloadable once the run is sealed");
		const mismatch = render({ sealError: "file artifacts/report/report.csv changed" });
		expect(mismatch).not.toContain("<a ");
		expect(mismatch).toContain("not served: the run folder does not match its seal");
	});
});

describe("RunScenarioEvidence with artifacts", () => {
	it("shows the artifacts section only when the run recorded artifacts", () => {
		expect(renderToStaticMarkup(<RunScenarioEvidence detail={plain} arm="A" />)).not.toContain("run-artifacts-A");
		const run: RunDetail = { ...plain, sealed: true, artifacts: files };
		const html = renderToStaticMarkup(<RunScenarioEvidence detail={run} arm="B" />);
		expect(html).toContain('data-testid="run-artifacts-B"');
		expect(html).toContain(`href="/api/bench/runs/${encodeURIComponent(plain.run.run_id)}/artifacts/report/report.csv"`);
		expect(html).not.toContain('data-testid="run-tools-B"');
	});
});