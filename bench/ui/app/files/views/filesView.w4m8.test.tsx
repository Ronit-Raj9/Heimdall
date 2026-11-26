import "@/lib/i18n";

import { errorQuery, mutation, okQuery, render } from "@/lib/testing/render";
import { routerState } from "@/lib/testing/routerMock";
import { describe, expect, it, vi } from "vitest";
import FilesView from "./filesView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const listState = { current: okQuery({ area: "agents", dir: "", entries: [] as unknown[] }) as unknown };

vi.mock("@/lib/store/apis/projectFilesApi", async (orig) => {
	const real = await orig<typeof import("@/lib/store/apis/projectFilesApi")>();
	return {
		...real,
		useListProjectFilesQuery: () => listState.current,
		useGetProjectFileQuery: () =>
			okQuery({
				area: "agents",
				path: "bot.yaml",
				encoding: "utf-8",
				content: "id: bot\n",
				entry: { path: "bot.yaml", size: 8, dir: false, mod_time: "", sha256: "abc" },
			}),
		useGetProjectFileVersionsQuery: () => okQuery({ area: "agents", path: "bot.yaml", versions: [] }),
		useLazyGetProjectFileVersionQuery: () => [() => Promise.resolve({}), {}],
		useSaveProjectFileMutation: () => mutation(),
		useDeleteProjectFileMutation: () => mutation(),
	};
});

describe("files load error", () => {
	it("names the problem and offers Retry", () => {
		routerState.location.search = { area: "agents" };
		listState.current = errorQuery(500, "boom");
		const html = render(<FilesView />);
		expect(html).toContain("Could not load the files");
		expect(html).toContain('data-testid="files-error-retry"');
	});
});