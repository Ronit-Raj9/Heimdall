// Types of round 5 lane L5: the MCP server report and the rows of a question-and-answer benchmark.
import type { DatasetVersion } from "./annotation";

/** One agent's use of one tool (mcptest.AgentToolStats). */
export interface McpAgentToolStats {
	agent?: string;
	runs: number;
	picked: number;
	calls: number;
	valid: number;
	succeeded: number;
	passed: number;
}

/** One tool across every agent (mcptest.ToolStats). */
export interface McpToolStats extends McpAgentToolStats {
	tool: string;
	pick_rate: number;
	valid_rate: number;
	success_rate: number;
	p50_ms: number;
	p95_ms: number;
	by_agent: McpAgentToolStats[] | null;
	advice: string[] | null;
}

/** GET /mcp-servers/benchmarks/{id}/report (handlers.MCPServerReport). */
export interface McpServerReport {
	benchmark: string;
	experiment?: string;
	server: string;
	runs: number;
	agents: string[] | null;
	calls: number;
	errors: number;
	tools: McpToolStats[] | null;
	unlisted_tools: string[] | null;
}

/** One task of GET /suites/{id}/dataset/tasks (handlers.DatasetTask). */
export interface DatasetTaskRow {
	id: string;
	title?: string;
	held_out?: boolean;
	from_rows: boolean;
	row_sha256?: string;
	chat?: boolean;
	metadata?: Record<string, unknown>;
	attachments?: string[];
}

/** GET /suites/{id}/dataset/tasks (handlers.DatasetTaskList). */
export interface DatasetTaskList {
	benchmark: string;
	version: number;
	total: number;
	offset: number;
	tasks: DatasetTaskRow[] | null;
}

/** One row sent to POST /suites/{id}/dataset/tasks (dataset.Row, the fields this form sets). */
export interface DatasetRowInput {
	id?: string;
	title?: string;
	input: string;
	expected?: string[];
}

/** Body of POST /suites/{id}/dataset/tasks (handlers.DatasetTasksRequest). */
export interface DatasetTasksRequest {
	tasks: DatasetRowInput[];
	mode?: "upsert" | "add";
	source?: string;
}

/** Answer of POST /suites/{id}/dataset/tasks (handlers.DatasetTasksResult, the fields shown). */
export interface DatasetTasksResult {
	benchmark: string;
	version: DatasetVersion | null;
	added: string[] | null;
	updated: string[] | null;
	unchanged: string[] | null;
	problems: { index: number; id?: string; message: string }[] | null;
}

/** GET /suites/{id}/dataset/versions/{n} (handlers.DatasetVersionDetail). */
export interface DatasetVersionDetail {
	benchmark: string;
	version: DatasetVersion;
	tasks: Record<string, string> | null;
	actor_name?: string;
}