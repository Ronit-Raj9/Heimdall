// Agent ops area (web parity lane L09): handlers/agentops.go.

/** One skill folder of an agent. */
export interface AgentSkill {
	name: string;
	/** Relative to the project, for example agents/codex/skills/review. */
	path: string;
	files: number;
	bytes: number;
	/** Holds SKILL.md at its top. */
	valid: boolean;
	/**
	 * installed: passed its scan and runs install it. blocked: the scan blocked it; kept aside, never installed.
	 * not_scanned: no scan of its current files; runs refuse it until it passes one.
	 */
	status?: AgentSkillStatus;
	/** The skill's last scan, when there is one. */
	scan?: SkillScanSummary;
}

export type AgentSkillStatus = "installed" | "blocked" | "not_scanned";

/** One finding of an admission scan (schemas.AdmitScanFinding). */
export interface AdmitScanFinding {
	rule: string;
	severity: string;
	file: string;
	line?: number;
	column?: number;
	excerpt?: string;
	detail: string;
}

/** The last scan of one skill: decision, counts and the findings that block or warn (at most 20). */
export interface SkillScanSummary {
	decision: string;
	block: number;
	warn: number;
	scanned_at: string;
	findings: AdmitScanFinding[];
}

/** The parts of an admission scan record (schemas.AdmitScanRecord) the page reads. */
export interface AdmitScanRecord {
	decision: string;
	counts: { block: number; warn: number; info: number };
	findings: AdmitScanFinding[];
}

/** POST /agents/{id}/skills (upload) and POST /agents/{id}/skills/{name}/scan. */
export interface SkillScanResult {
	skill: AgentSkill;
	installed: boolean;
	record: AdmitScanRecord;
}

/** GET /agents/{id}/skills. */
export interface AgentSkillsResponse {
	agent: string;
	config_path: string;
	dir: string;
	/** The config's skills.extra lists dir, so runs install these skills. */
	installed: boolean;
	skills: AgentSkill[];
}

/** One MCP server spec (the shape of a task's services: entry). */
export interface AgentMCPServer {
	id: string;
	role?: string;
	image?: string;
	argv?: string[];
	port?: number;
}

/** GET and PUT /agents/{id}/mcp. */
export interface AgentMCPResponse {
	agent: string;
	config_path: string;
	format: string;
	mount_path: string;
	servers_file: string;
	servers_yaml: string;
	servers: AgentMCPServer[];
}

/** PUT /agents/{id}/mcp body. */
export interface AgentMCPRequest {
	format: string;
	mount_path: string;
	servers_yaml: string;
}

/** The MCP config formats an agent adapter renders ("" lets the adapter choose). */
export const MCP_FORMATS = ["", "codex_toml", "claude_json", "opencode_json", "generic_json", "env"] as const;

/**
 * Job params of the scan kinds (lane L02's cliJob arg builders): the path is relative to the
 * project folder. Kept in one place so a contract change is a one-line fix.
 */
export const scanSkillJob = (dir: string) => ({ kind: "scan.skill", params: { path: dir } });
export const scanMCPJob = (config: string) => ({ kind: "scan.mcp", params: { path: config } });

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		agentops: typeof import("@/locales/en/agentops.json");
	}
}