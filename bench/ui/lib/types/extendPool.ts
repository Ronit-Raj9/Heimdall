/** Round 6 N6: GET /experiments/{id}/extensions (handlers/extendpool_n6.go). */
export interface ExtendPoolMember {
	exp_id: string;
	name: string;
	kind: string;
	runs: number;
	pooled: boolean;
	reason?: string;
}

export interface ExtendPooledResult {
	condition: string;
	metric: string;
	tasks: number;
	runs: number;
	added_runs: number;
	mean_a: number;
	mean_b: number;
	d_bar: number;
	interval?: [number, number];
}

export interface ExtendPoolView {
	exp_id: string;
	parent?: string;
	parent_pooled: boolean;
	parent_reason?: string;
	extensions: ExtendPoolMember[];
	pooled: ExtendPooledResult | null;
}
