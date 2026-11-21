// Agent builder (web parity L08): mirrors handlers/agentbuilder.go.

export type TemplateCategory = "cli" | "framework" | "starter" | "bring-your-own";

export interface TemplateParam {
	name: string;
	label: string;
	help?: string;
	placeholder?: string;
	required: boolean;
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
