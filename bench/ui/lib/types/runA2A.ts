/** GET /runs/{id}/a2a (handlers/runa2a.go RunA2A, schemas/a2a.go A2AEvidence): the agent gateway's witness of an a2a run. */
export interface A2AHop {
	from: string;
	to: string;
	operation_id: string;
	task_id?: string;
	source: string;
}

export interface A2ALinkedRow {
	kind: string;
	id: string;
	task_id: string;
	virtual_key_id?: string;
	member?: string;
	on_run_key: boolean;
}

export interface A2AAgreement {
	task_id?: string;
	launcher_state?: string;
	gateway_state?: string;
	answer_matches?: boolean;
	note?: string;
}

export interface A2AOperation {
	id: string;
	operation: string;
	status: string;
	agent: string;
	member?: string;
	caller?: string;
	task_id?: string;
	task_state?: string;
	error?: string;
}

export interface A2AEvidence {
	entry: string;
	operations?: A2AOperation[];
	hops?: A2AHop[];
	linked?: A2ALinkedRow[];
	agreement?: A2AAgreement;
	complete: boolean;
	reason?: string;
	problems?: string[];
	gateway?: string;
	strength: string;
	foreign?: number;
}

export interface RunA2AResponse {
	run_id: string;
	available: boolean;
	a2a: A2AEvidence | null;
}