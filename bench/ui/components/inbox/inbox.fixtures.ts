import type { InboxAcceptResponse, InboxIngestResponse, InboxItemDetailResponse, InboxListResponse } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";

// Tests only. The synthetic payloads fixture mode serves for GET /inbox and GET /inbox/{id}
// (public/fixtures), read once so tests and fixture mode share one copy. The write results have no
// fixture file (a synthetic server takes no writes), so they are written out here.

const FIXTURE_DIR = path.resolve(__dirname, "../../public/fixtures");
const read = <T>(file: string): T => JSON.parse(fs.readFileSync(path.join(FIXTURE_DIR, file), "utf8")) as T;

/** Six items, one per state and two accepted (one passing with a recurrence, one not run yet). */
export const INBOX_LIST_FIXTURE = read<InboxListResponse>("inbox.json");

/** The new refund timeout item, with its redacted preview. */
export const INBOX_NEW_DETAIL_FIXTURE = read<InboxItemDetailResponse>("inbox.inc-3f9a1c2b7d10.json");

/** The accepted warranty item, whose task passes in e_123. */
export const INBOX_ACCEPTED_DETAIL_FIXTURE = read<InboxItemDetailResponse>("inbox.inc-7b20c4d9e811.json");

/** What accepting the new item answers, as the API contract shows it. */
export const INBOX_ACCEPT_FIXTURE: InboxAcceptResponse = {
	item: {
		...INBOX_NEW_DETAIL_FIXTURE.item,
		state: "accepted",
		task: {
			suite: "support",
			id: "refund-timeout",
			dir: "benchmarks/support/tasks/refund-timeout",
			accepted_at: "2026-11-18T06:00:00Z",
			admitted: false,
		},
		updated: "2026-11-18T06:00:00Z",
	},
	draft: {
		task: "support/refund-timeout",
		dir: "benchmarks/support/tasks/refund-timeout",
		template: "mcp",
		edit_me: ["instruction.md", "verifier/tests/checks.py"],
		next: [
			"edit instruction.md and verifier/tests/checks.py where they say EDIT ME",
			"bifrost-bench task validate support/refund-timeout",
			"bifrost-bench task admit support/refund-timeout",
		],
		warnings: ["the trace has no tool schema for lookup_order; the draft stubs it"],
	},
	mode: "live",
};

/** What POST /inbox/ingest answers for a new signature. */
export const INBOX_INGEST_FIXTURE: InboxIngestResponse = { item_id: "inc-3f9a1c2b7d10", created: true, occurrences: 1, mode: "live" };