import "@/lib/i18n";

import type { JobLogState } from "@/components/platform/useJobLog";
import { RUNNING_JOB, hostedSession } from "@/lib/testing/platformFixtures";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { JobDetail } from "./jobView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useCancelJobMutation: () => [() => undefined, { isLoading: false }],
}));

const log: JobLogState = { lines: [], dropped: 0, ended: true, transport: "stream", error: undefined };

describe("job page names who started the job (D4)", () => {
	it("shows the display name with the raw id as a tooltip", () => {
		const html = render(
			<JobDetail job={{ ...RUNNING_JOB, created_by: "usr_someone", created_by_name: "Ada" }} log={log} />,
			hostedSession("member"),
		);
		const cell = html.slice(html.indexOf('data-testid="job-created-by"') - 200, html.indexOf('data-testid="job-created-by"') + 200);
		expect(cell).toContain(">Ada<");
		expect(cell).toContain('title="usr_someone"');
	});

	it("shows the name alone when the server left the email out", () => {
		const html = render(<JobDetail job={{ ...RUNNING_JOB, created_by: "", created_by_name: "bo" }} log={log} />, hostedSession("member"));
		const at = html.indexOf('data-testid="job-created-by"');
		const cell = html.slice(html.lastIndexOf("<span", at), html.indexOf("</span>", at));
		expect(cell).toContain(">bo");
		expect(cell).not.toContain("title=");
	});
});