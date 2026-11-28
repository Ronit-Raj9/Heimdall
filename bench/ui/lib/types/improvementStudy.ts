// Improvement studies (bench/improve/study.go StudyView, StudyVerdict): the whole loop run 2 to 5
// times from version 0, judged together on one shared held-out split.

export interface StudyVerdict {
	state: string;
	headline: string;
	n: number;
	n_nonzero: number;
	gain: number;
	ci95: [number, number];
	p_upper: number;
	loop_gains: number[];
	/** Loops that ended better than version 0. */
	better: number;
	loops: number;
	loop_ci80?: [number, number];
}

export interface StudyView {
	id: string;
	name: string;
	agent: string;
	suite: string;
	status: string;
	reason: string;
	/** Improvement run ids, one per loop started so far. */
	loops: string[];
	loops_max: number;
	held_out: number;
	created_at: string;
	updated_at: string;
	created_by: string;
	final?: StudyVerdict;
	cost_usd: number;
	running: boolean;
	/** The total limit and each loop's share, in words. */
	limit?: string;
}

export interface StudyList {
	studies: StudyView[];
}
