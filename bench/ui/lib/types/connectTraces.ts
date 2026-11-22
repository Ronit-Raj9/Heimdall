// Connect from traces (handlers/connecttraces.go, bench/agentinfer): the agent an org's
// OpenTelemetry traces show, proposed as a draft. Hints only; the connect test confirms it.

export interface TraceSeen {
	value: string;
	count: number;
}

export interface TraceToolSeen {
	name: string;
	calls: number;
	errors?: number;
}

export interface TraceRoute {
	method: string;
	scheme?: string;
	host?: string;
	path: string;
	url?: string;
	/** Loopback, private or a cluster name: the shared worker never calls it. */
	private?: boolean;
	count: number;
}

export interface TraceEvidence {
	field: string;
	value: string;
	from: string;
}

export interface TraceModelRef {
	provider: string;
	model: string;
}

export interface TraceDraft {
	id: string;
	name?: string;
	service?: string;
	framework?: string;
	language?: string;
	models?: TraceSeen[];
	provider?: string;
	tools?: TraceToolSeen[];
	tools_declared?: string[];
	retrievals?: number;
	memory_ops?: number;
	subagents?: string[];
	conversations?: number;
	multi_turn?: boolean;
	system_instructions_seen?: boolean;
	route?: TraceRoute;
	traces: number;
	spans: number;
	llm_calls?: number;
	first_seen?: string;
	last_seen?: string;
	capabilities?: string[];
	packs?: string[];
	template?: string;
	params?: Record<string, string>;
	model?: TraceModelRef;
	preset?: string;
	confidence: "high" | "medium" | "low";
	evidence?: TraceEvidence[];
	questions?: string[];
}

export interface TraceFromTemplate {
	template: string;
	id: string;
	model?: TraceModelRef;
	notes?: string;
	params?: Record<string, string>;
}

export interface TraceSetup {
	id: string;
	label: string;
	install?: string;
	env: string;
	code?: string;
}

export interface TraceConnectInfo {
	endpoint: string;
	base: string;
	hosted: boolean;
	token_hint: string;
	setup: TraceSetup[];
	exports: number;
	spans_kept: number;
	spans_dropped?: number;
	first_received?: string;
	last_received?: string;
	draft?: TraceDraft;
	summary?: string;
	agent_yaml?: string;
	request?: TraceFromTemplate;
}

export interface TraceInferResult {
	draft: TraceDraft;
	summary: string;
	agent_yaml: string;
	request?: TraceFromTemplate;
}
