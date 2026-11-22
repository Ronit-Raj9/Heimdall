import { errorQuery, mutation, okQuery, render } from "@/lib/testing/render";
import { routerState } from "@/lib/testing/routerMock";
import { describe, expect, it, vi } from "vitest";
import { encodeProjectPath, projectFilePath, saveProjectFileRequest } from "@/lib/store/apis/projectFilesApi";
import { syncCommands } from "@/components/projectfs/syncCard";
import FilesView, { validNewName } from "./filesView";

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

describe("FilesView", () => {
	it("shows the empty state with a create box", () => {
		routerState.location.search = { area: "agents" };
		listState.current = okQuery({ area: "agents", dir: "", entries: [] });
		const html = render(<FilesView />);
		expect(html).toContain('data-testid="files-empty"');
		expect(html).toContain('data-testid="files-new-name"');
		expect(html).toContain('data-testid="files-export"');
		expect(html).toContain("Download project (zip)");
		expect(html).toContain('data-testid="files-sync-card"');
		expect(html).toContain("heimdall pull -server");
		expect(html).toContain("heimdall push -server");
	});

	it("builds the sync commands for this server and org", () => {
		const c = syncCommands("https://heimdall.example.com", "acme");
		expect(c.pull).toBe("heimdall pull -server https://heimdall.example.com -org acme -token $HEIMDALL_TOKEN -dir .");
		expect(c.push).toBe("heimdall push -server https://heimdall.example.com -org acme -token $HEIMDALL_TOKEN -dir .");
		expect(syncCommands("http://127.0.0.1:8090").pull).not.toContain("-org");
	});

	it("lists entries and opens the editor for ?path", () => {
		routerState.location.search = { area: "agents", path: "bot.yaml" };
		listState.current = okQuery({
			area: "agents",
			dir: "",
			entries: [
				{ path: "bot.yaml", size: 8, dir: false, mod_time: "2026-11-21T00:00:00Z" },
				{ path: "skills", size: 0, dir: true, mod_time: "2026-11-21T00:00:00Z" },
			],
		});
		const html = render(<FilesView />);
		expect(html).toContain('data-testid="files-entry-bot.yaml"');
		expect(html).toContain('data-testid="files-entry-skills"');
		expect(html).toContain('data-testid="file-editor"');
		expect(html).toContain("id: bot");
	});

	it("shows the server error", () => {
		routerState.location.search = { area: "agents" };
		listState.current = errorQuery(503, "project files are not available");
		expect(render(<FilesView />)).toContain("project files are not available");
	});
});

describe("project file requests", () => {
	it("escapes segments, sends raw content and If-Match", () => {
		expect(encodeProjectPath("a b/c?.yaml")).toBe("a%20b/c%3F.yaml");
		expect(projectFilePath("agents", "x/y.yaml")).toBe("/project/files/agents/x/y.yaml");
		const req = saveProjectFileRequest({ area: "agents", path: "y.yaml", content: "id: y", ifMatch: "abc", note: "n 1" });
		expect(req.method).toBe("PUT");
		expect(req.body).toBe("id: y");
		expect(req.url).toBe("/project/files/agents/y.yaml?note=n%201");
		expect((req.headers as Record<string, string>)["If-Match"]).toBe('"abc"');
	});

	it("checks new file names", () => {
		expect(validNewName("x.yaml")).toBe(true);
		expect(validNewName("dir/x.yaml")).toBe(true);
		expect(validNewName("../x")).toBe(false);
		expect(validNewName("/x")).toBe(false);
		expect(validNewName(".git/config")).toBe(false);
	});
});