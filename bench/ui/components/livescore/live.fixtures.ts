import compareJSON from "../../../handlers/testdata/livescore/compare.json";
import pullJSON from "../../../handlers/testdata/livescore/pull.json";
import sessionsJSON from "../../../handlers/testdata/livescore/sessions.json";
import statusJSON from "../../../handlers/testdata/livescore/status.json";
import type {
	LiveComparison,
	LiveComparisonResponse,
	LivePullResponse,
	LivePullResult,
	LiveSessionList,
	LiveSessionListResponse,
	LiveStatus,
	LiveStatusResponse,
} from "./livescore.types";

// The fixtures are the Go side's own output: bench/livescore's TestWriteFixtures runs the end-to-end
// scenario (two versions on a fake gateway, the candidate failing every second session) and writes
// these files, which the handler contract test also serves. The server adds "mode".
export const LIVE_STATUS_FIXTURE: LiveStatusResponse = {
	...(statusJSON as unknown as LiveStatus),
	mode: "live",
};
export const LIVE_COMPARE_FIXTURE: LiveComparisonResponse = {
	...(compareJSON as unknown as LiveComparison),
	mode: "live",
};
export const LIVE_SESSIONS_FIXTURE: LiveSessionListResponse = {
	...(sessionsJSON as unknown as LiveSessionList),
	mode: "live",
};
export const LIVE_PULL_FIXTURE: LivePullResponse = {
	...(pullJSON as unknown as LivePullResult),
	mode: "live",
};
