import "@/lib/i18n";

import type { RecordingView, RunRecordings } from "@/lib/store/apis/recordingsApi";
import type { VoiceCall } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setAuthState } from "./session";

// Wave 4, organization per tab: the URLs the browser opens directly that the first list missed (the
// project zip, the run trace download, the call audio and the run recordings) name the tab's
// organization in hosted mode and stay unchanged in local mode.

const state: { data?: RunRecordings; isLoading: boolean } = { isLoading: false };
vi.mock("@/lib/store/apis/recordingsApi", () => ({ useGetRunRecordingsQuery: () => state }));

const { projectExportUrl } = await import("./projectFilesApi");
const { runTraceUrl } = await import("@/components/runs/traceView.utils");
const { VoicePanel } = await import("@/components/voice/runVoicePanel");
const { RecordingPlayer } = await import("@/components/runs/recordingPlayer");

const video: RecordingView = {
	index: 0,
	service: "browser",
	id: "video",
	kind: "video",
	file: "video-page-1.webm",
	mime: "video/webm",
	bytes: 2048,
	started_at: "2026-11-21T10:00:00Z",
	url: "/api/bench/runs/r1/recordings/0",
};
const har: RecordingView = {
	index: 1,
	service: "browser",
	id: "har",
	kind: "har",
	file: "har-context-1.har",
	bytes: 2048,
	url: "/api/bench/runs/r1/recordings/1",
};

const call: VoiceCall = {
	run_id: "r_v1",
	caller: "customer",
	called: true,
	audio: [{ track: "agent", sha256: "c3d7", bytes: 10, url: "/api/bench/runs/r_v1/voice/audio/agent" }],
	witness: "w",
	bounds_source: "default",
} as VoiceCall;

afterEach(() => setAuthState({ mode: "local" }));

describe("direct URLs carry the tab's organization (w4b03)", () => {
	it("project zip and run trace download", () => {
		setAuthState({ mode: "hosted", csrf: "c", org: "org_a" });
		expect(projectExportUrl()).toBe("/api/bench/project/export.zip?heimdall_org=org_a");
		expect(runTraceUrl("/api/bench", "r 1", "otlp")).toBe("/api/bench/runs/r%201/trace?format=otlp&heimdall_org=org_a");
		setAuthState({ mode: "local" });
		expect(projectExportUrl()).toBe("/api/bench/project/export.zip");
		expect(runTraceUrl("/api/bench", "r 1", "otlp")).toBe("/api/bench/runs/r%201/trace?format=otlp");
	});
	it("call audio", () => {
		setAuthState({ mode: "hosted", csrf: "c", org: "org_a" });
		const html = renderToStaticMarkup(<VoicePanel data={call} />);
		expect(html).toContain('src="/api/bench/runs/r_v1/voice/audio/agent?heimdall_org=org_a"');
	});
	it("run recordings: the video, its download and the other files", () => {
		setAuthState({ mode: "hosted", csrf: "c", org: "org_a" });
		state.data = { run_id: "r1", sealed: true, recordings: [video, har], steps: [] };
		const html = renderToStaticMarkup(<RecordingPlayer runId="r1" />);
		expect(html).toContain('src="/api/bench/runs/r1/recordings/0?heimdall_org=org_a"');
		expect(html).toContain('href="/api/bench/runs/r1/recordings/0?heimdall_org=org_a"');
		expect(html).toContain('href="/api/bench/runs/r1/recordings/1?heimdall_org=org_a"');
	});
});