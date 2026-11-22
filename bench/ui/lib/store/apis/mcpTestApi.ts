import type { McpDraftRequest, McpDraftResponse, McpInspectRequest, McpReport } from "@/lib/types/mcptest";
import { benchApi } from "./benchApi";

/** MCP server mode (handlers/mcptest.go): inspect a server, draft a benchmark of it. */
export const mcpTestApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		inspectMcpServer: builder.mutation<McpReport, McpInspectRequest>({
			query: (body) => ({ url: "/mcp-servers/inspect", method: "POST", body }),
		}),
		draftMcpBenchmark: builder.mutation<McpDraftResponse, McpDraftRequest>({
			query: (body) => ({ url: "/mcp-servers/benchmarks", method: "POST", body }),
		}),
	}),
});

export const { useInspectMcpServerMutation, useDraftMcpBenchmarkMutation } = mcpTestApi;