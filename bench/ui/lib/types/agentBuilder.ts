// Agent builder (web parity L08): mirrors handlers/agentbuilder.go.

export type TemplateCategory = "cli" | "framework" | "starter" | "bring-your-own";

export interface TemplateParam {
	name: string;
	label: string;
	help?: string;
	placeholder?: string;
	required: boolean;
	/** How the form shows it: text (default), secret (a Settings > Secrets name), bool, number, select, multiline. */
	kind?: "text" | "secret" | "bool" | "number" | "select" | "multiline";
	options?: string[];
	default?: string;
	/** Fold the field sits under: auth, tools, advanced; none means always shown. */
	group?: "auth" | "tools" | "advanced";
}

export interface AgentTemplate {
	id: string;
	name: string;
	category: TemplateCategory;
	description: string;
	adapter: string;
	/** Gateway providers the template runs on; the first is the default. */
	providers: string[];
	default_model: string;
	builds: boolean;
	params: TemplateParam[];
	docs?: string;
}

export interface AgentTemplateList {
	templates: AgentTemplate[];
}

export interface AgentModelRef {
	provider: string;
	model: string;
	key_id?: string;
}

export interface AgentFromTemplateRequest {
	template: string;
	id: string;
	model?: AgentModelRef;
	notes?: string;
	params?: Record<string, string>;
	dry_run?: boolean;
}

export interface AgentFromTemplateResponse {
	id: string;
	path: string;
	files: string[];
	yaml: string;
	dry_run: boolean;
}

export interface AgentConfigFields {
	id: string;
	adapter: string;
	image?: string;
	model: string;
	provider: string;
	wall_s: number;
	cpus: number;
	mem_gb: number;
	turns: number;
	argv?: string[];
	env?: Record<string, string>;
	remote_url?: string;
	a2a_card_url?: string;
	description?: string;
	/** Instructions, memory, retries and limits, capabilities. Absent from an older server. */
	behavior?: AgentBehaviorFields;
}

/** The Setup form's behavior fields; each maps to one key of the agent YAML (empty removes it). */
export interface AgentBehaviorFields {
	notes_project_file: string;
	notes_user_file: string;
	notes_user_path: string;
	memory_paths: string[];
	max_retries: number;
	max_tool_calls: number;
	provider_retries: number;
	fallbacks: string[];
	/** Empty uses the defaults of the agent's kind. */
	capabilities: string[];
}

export interface AgentConfigDoc {
	id: string;
	path: string;
	yaml: string;
	sha256: string;
	fields: AgentConfigFields;
}

export interface AgentConfigPut {
	id: string;
	yaml?: string;
	fields?: AgentConfigFields;
	if_match?: string;
}

export interface AgentConfigValidateRequest {
	yaml?: string;
	fields?: AgentConfigFields;
}

export interface AgentConfigValidateResponse {
	ok: boolean;
	error?: string;
	yaml: string;
	fields?: AgentConfigFields;
}

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		agentBuilder: typeof import("@/locales/en/agentBuilder.json");
	}
}

/** POST /agents/test-endpoint: a saved agent or the new-agent form. */
export interface EndpointTestRequest {
	agent?: string;
	template?: AgentFromTemplateRequest;
}

export interface EndpointTestStep {
	at_ms: number;
	kind: "request" | "response" | "poll" | "answer" | "event" | "note" | "error" | string;
	detail: string;
}

/** One sample task sent to the endpoint, with its answer and trace (handlers/agenttest.go). */
export interface EndpointTestResult {
	ok: boolean;
	matched: boolean;
	adapter: string;
	url: string;
	status?: number;
	latency_ms: number;
	answer?: string;
	error?: string;
	reported?: Record<string, unknown>;
	trace: EndpointTestStep[];
	notes: string[];
}
