// Import an agent by its platform id (lane Z16): mirrors handlers/agentimport.go and
// platformimport (Platform, Result).

export interface AgentPlatformInput {
	name: string;
	label: string;
	placeholder?: string;
	help?: string;
	required?: boolean;
}

export interface AgentPlatformSecret {
	role: string;
	label: string;
	help?: string;
	required?: boolean;
	/** Name the form offers when the org has no secret picked yet. */
	suggested: string;
}

export interface AgentPlatform {
	id: string;
	label: string;
	/** voice: a voice platform agent; endpoint: an HTTP endpoint agent. */
	kind: "voice" | "endpoint";
	help: string;
	id_label: string;
	id_placeholder: string;
	inputs?: AgentPlatformInput[];
	secrets: AgentPlatformSecret[];
	imports: string[];
	docs: string;
}

export interface AgentPlatformList {
	platforms: AgentPlatform[];
}

export interface ImportedTool {
	name: string;
	type?: string;
	description?: string;
}

export interface ImportedDefinition {
	name: string;
	description?: string;
	prompt?: string;
	first_message?: string;
	language?: string;
	model?: string;
	tools?: ImportedTool[];
	voice?: { provider?: string; voice_id?: string; model?: string; settings?: Record<string, unknown> };
	protocol?: string;
	status?: string;
	version?: string;
	fields?: Record<string, string>;
	source: string;
}

export interface ImportResult {
	platform: string;
	agent_id: string;
	definition: ImportedDefinition;
	adapter: string;
	preset?: string;
	block: Record<string, unknown>;
	capability_hints: string[];
	warnings: string[];
	fetched_at: string;
}

export interface AgentImportRequest {
	id: string;
	platform: string;
	agent_id: string;
	inputs?: Record<string, string>;
	/** Secret role to org secret name. */
	secrets?: Record<string, string>;
	dry_run?: boolean;
}

export interface AgentImportResponse {
	id: string;
	path: string;
	files: string[];
	yaml: string;
	dry_run: boolean;
	import: ImportResult;
	validation_error?: string;
}

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		agentImport: typeof import("@/locales/en/agentImport.json");
	}
}
