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
	/** Fold the field sits under: mapping, auth, tools, advanced; none means always shown. */
	group?: "mapping" | "auth" | "tools" | "advanced";
	/** Shown text per select option; an option without one shows its value. */
	option_labels?: Record<string, string>;
	/** Show the field only when each named param has one of the listed values. A hidden field's value is still sent. */
	show_when?: Record<string, string[]>;
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
	/** A save's answer only: the agent version current after the save. */
	version?: number;
	/** A save's answer only: true when the save made a new version. */
	version_created?: boolean;
}

export interface AgentConfigPut {
	id: string;
	yaml?: string;
	fields?: AgentConfigFields;
	if_match?: string;
	/** Refuse the save (409) when the agent already exists: a new agent never overwrites one. */
	create_only?: boolean;
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
	/** The requests sent, secret values masked (remotemap.Sent). */
	requests?: SentRequest[];
	/** The last answer's body as received. */
	raw_response?: string;
	/** How the answer was read, in words. */
	reads?: string;
}

/** One request as shown to the user (remotemap.Sent); secret values are masked. */
export interface SentRequest {
	step: "start" | "request" | "poll" | "end" | string;
	method: string;
	url: string;
	/** [name, value] in send order. */
	headers: [string, string][];
	body?: string;
}

/** POST /agents/endpoint-preview: the requests a sample task would send, nothing sent. */
export interface EndpointPreview {
	ok: boolean;
	error?: string;
	requests: SentRequest[];
	reads: string;
}