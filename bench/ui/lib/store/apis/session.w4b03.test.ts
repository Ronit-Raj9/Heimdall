import { afterEach, describe, expect, it, vi } from "vitest";

// Wave 4, organization per tab: the tab keeps its own organization across a reload, and URLs the
// browser opens directly name it in a query parameter.

function memoryStorage(seed: Record<string, string> = {}): Storage {
	const items = new Map<string, string>(Object.entries(seed));
	return {
		get length() {
			return items.size;
		},
		clear: () => items.clear(),
		getItem: (key) => items.get(key) ?? null,
		key: (i) => [...items.keys()][i] ?? null,
		removeItem: (key) => void items.delete(key),
		setItem: (key, value) => void items.set(key, value),
	};
}

afterEach(async () => {
	const { setAuthState } = await import("./session");
	setAuthState({ mode: "local" });
	vi.unstubAllGlobals();
	vi.resetModules();
});

describe("organization per tab", () => {
	it("a reload keeps the tab's org", async () => {
		vi.stubGlobal("window", { sessionStorage: memoryStorage({ "heimdall.tab-org": "org_b" }), localStorage: memoryStorage() });
		vi.resetModules();
		const { getAuthState } = await import("./session");
		expect(getAuthState().org).toBe("org_b");
	});

	it("remembers the org the tab shows", async () => {
		const tab = memoryStorage();
		vi.stubGlobal("window", { sessionStorage: tab, localStorage: memoryStorage() });
		vi.resetModules();
		const { setAuthState } = await import("./session");
		setAuthState({ mode: "hosted", csrf: "c", org: "org_c" });
		expect(tab.getItem("heimdall.tab-org")).toBe("org_c");
	});

	it("withOrgParam adds the org to a download URL in hosted mode", async () => {
		const { setAuthState } = await import("./session");
		const { withOrgParam, ORG_QUERY_PARAM } = await import("./orgParam");
		setAuthState({ mode: "hosted", csrf: "c", org: "org b" });
		expect(ORG_QUERY_PARAM).toBe("heimdall_org");
		expect(withOrgParam("/api/bench/runs/r1/otlp")).toBe("/api/bench/runs/r1/otlp?heimdall_org=org%20b");
		expect(withOrgParam("/api/bench/x?format=csv")).toBe("/api/bench/x?format=csv&heimdall_org=org%20b");
	});

	it("leaves it alone in local mode", async () => {
		const { setAuthState } = await import("./session");
		const { withOrgParam } = await import("./orgParam");
		setAuthState({ mode: "local", org: "local" });
		expect(withOrgParam("/api/bench/runs/r1/otlp")).toBe("/api/bench/runs/r1/otlp");
	});

	it("the job log stream and the download builders carry the org in hosted mode", { timeout: 60000 }, async () => {
		const { setAuthState } = await import("./session");
		setAuthState({ mode: "hosted", csrf: "c", org: "org_a" });
		const { jobLogStreamUrl } = await import("./platformApi");
		const { experimentEventsUrl, releaseEvidenceUrl } = await import("./benchApi");
		const { compliancePackUrl } = await import("./governApi");
		const integrity = await import("./integrityApi");
		const { evaluationReportUrl } = await import("./evaluationsApi");
		const { exportDownloadUrl } = await import("./integrationsApi");
		const { experimentExportUrl, suiteHarborUrl } = await import("./exportApi");
		const { frameImageUrl } = await import("./framesApi");
		const { policyReportUrl } = await import("./policyDocsApi");
		const { projectRawUrl } = await import("./projectFilesApi");
		const { operateBackupUrl } = await import("./operateApi");
		const { recordingUrl, liveFrameUrl } = await import("./recordingsApi");
		const { visualImageUrl } = await import("./visualApi");
		const { reportExportUrl, reproduceUrl } = await import("./shareApi");
		const { kubernetesManifestUrl } = await import("./workerConnectionsApi");
		const { artifactDownloadUrl } = await import("@/components/runs/runArtifacts");
		const { otlpDownloadUrl } = await import("@/components/runs/runTraceLink");
		const urls = [
			jobLogStreamUrl("j1"),
			experimentEventsUrl("e1"),
			releaseEvidenceUrl("rel1"),
			compliancePackUrl("p1"),
			integrity.compliancePackUrl("e1", "zip" as never),
			integrity.monitorReportUrl("c1"),
			evaluationReportUrl("ev1"),
			exportDownloadUrl("x.zip"),
			experimentExportUrl("e1", "csv"),
			suiteHarborUrl("s1"),
			frameImageUrl("r1", 1),
			policyReportUrl("s1", "d1", "pdf"),
			projectRawUrl("agents" as never, "a.yaml"),
			operateBackupUrl("b1"),
			recordingUrl("r1", 1),
			liveFrameUrl("r1", "t"),
			liveFrameUrl("r1"),
			visualImageUrl("r1", "diff" as never),
			reportExportUrl("ev1", "md"),
			reproduceUrl("ev1"),
			kubernetesManifestUrl("ns"),
			artifactDownloadUrl("r1", { id: "a1", name: "out.txt" }),
			otlpDownloadUrl("r1"),
		];
		for (const url of urls) expect(url).toMatch(/[?&]heimdall_org=org_a$/);
	});
});