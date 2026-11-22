// MCP server mode (bench/mcptest, handlers/mcptest.go): the org's own MCP server is under test.

export interface McpServerTarget {
	url: string;
	secret?: string;
	auth_header?: string;
	auth_scheme?: string;
}

export interface McpInspectRequest extends McpServerTarget {
	repeats?: number;
	call_read_only?: boolean;
	call_tools?: string[];
	args?: Record<string, Record<string, unknown>>;
}

export interface McpDraftRequest extends McpInspectRequest {
	id: string;
	server_id: string;
	tools?: string[];
	max_tasks?: number;
}

export interface McpScores {
	overall: number;
	reliability: number;
	error_recovery: number;
	latency: number;
	schema: number;
	description: number;
}

export interface McpIssue {
	code: string;
	severity: "warn" | "info";
	field?: string;
	message: string;
	fix: string;
}

export interface McpToolQuality {
	tool: string;
	description_score: number;
	schema_score: number;
	params: number;
	required: number;
	read_only: boolean;
	destructive: boolean;
	issues: McpIssue[];
}

export interface McpTool {
	name: string;
	title?: string;
	description: string;
	read_only: boolean;
	destructive: boolean;
}

export interface McpRecoveryCheck {
	id: string;
	title: string;
	passed: boolean;
	skipped?: boolean;
	detail: string;
}

export interface McpToolCall {
	tool: string;
	args: Record<string, unknown>;
	placeholder: boolean;
	ok: boolean;
	is_error: boolean;
	latency_ms: number;
	detail: string;
}

export interface McpPercentiles {
	samples: number;
	p50_ms: number;
	p95_ms: number;
	max_ms: number;
}

export interface McpScanFinding {
	rule: string;
	severity: string;
	file: string;
	detail: string;
}

export interface McpReport {
	url: string;
	checked_at: string;
	reached: boolean;
	error?: string;
	server: { protocol_version: string; name: string; version: string; has_tools: boolean; session: boolean };
	tools: McpTool[];
	scores: McpScores;
	reliability: { attempts: number; succeeded: number; rate: number; stable_listing: boolean; ping_supported: boolean; failures: string[] };
	latency: McpPercentiles & { by_method: Record<string, McpPercentiles> };
	error_recovery: McpRecoveryCheck[];
	calls: McpToolCall[];
	quality: { schema_score: number; description_score: number; tools: McpToolQuality[]; server_issues: McpIssue[] };
	scan: McpScanFinding[];
	requests: number;
}

export interface McpDraftResponse {
	suite: string;
	tasks: { id: string; tool: string; title: string; reference: boolean }[];
	no_reference: string[];
	files: string[];
	scores: McpScores;
	reference_agents: string[];
	/** Reference agents whose default provider has a usable model key of the org, in order (N113). */
	reference_agents_keyed?: string[];
	next: string;
}

/** A server name agents see: lowercase letters, digits and dashes (handlers: serverIDRe). */
export function serverIdFrom(raw: string): string {
	return raw
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, "-")
		.replace(/^-+|-+$/g, "")
		.slice(0, 41);
}

/** Plain-word band for a 0 to 100 score. */
export function scoreBand(score: number): "good" | "fair" | "poor" {
	if (score >= 80) return "good";
	if (score >= 50) return "fair";
	return "poor";
}