import type { StepRecord } from "@/lib/types/bench";
import type { RunCall } from "@/lib/types/runDebug";

/** Parses "1, 4,7" into [1, 4, 7]; null when any part is not a whole number above 0. */
export function parseCallList(text: string): number[] | null {
	const parts = text
		.split(/[\s,]+/)
		.map((p) => p.trim())
		.filter(Boolean);
	const out: number[] = [];
	for (const p of parts) {
		if (!/^\d+$/.test(p)) return null;
		const n = Number(p);
		if (n < 1) return null;
		if (!out.includes(n)) out.push(n);
	}
	return out.sort((a, b) => a - b);
}

/** Parses "bash, edit" into ["bash", "edit"]. */
export function parseNames(text: string): string[] {
	return Array.from(new Set(text.split(/[\s,]+/).map((p) => p.trim()).filter(Boolean)));
}

/** The model calls of a run keyed by gateway request id. */
export function callsByRequest(calls: readonly RunCall[] | undefined): Map<string, RunCall> {
	const out = new Map<string, RunCall>();
	for (const c of calls ?? []) if (c.request_id) out.set(c.request_id, c);
	return out;
}

/** The next selection for j (+1) or k (-1) over a list of length n; -1 means nothing selected. */
export function stepIndex(current: number, delta: 1 | -1, n: number): number {
	if (n === 0) return -1;
	if (current < 0) return delta > 0 ? 0 : n - 1;
	return Math.min(n - 1, Math.max(0, current + delta));
}

/** "m:ss" until deadline, never below 0:00. */
export function countdown(deadline: string, now: number): string {
	const left = Math.max(0, Math.round((Date.parse(deadline) - now) / 1000));
	return `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`;
}

/** The steps a side-by-side diff compares: model calls and tool requests. */
const DIFF_KINDS = new Set(["model_turn", "tool_request"]);

/** What a step did, for comparison across runs: the kind, and the tool name for a tool request. */
export function stepSignature(s: StepRecord): string {
	return s.kind === "tool_request" ? `tool:${s.tool_name ?? ""}` : s.kind;
}

export interface DiffRow {
	turn: number;
	left: StepRecord[];
	right: StepRecord[];
	same: boolean;
}

/**
 * Aligns two runs' traces by model call (the step's turn): one row per turn either run reached,
 * with that turn's model and tool steps on each side. A row is the same when both sides did the
 * same things in the same order. firstDiff is the turn of the first row that differs, or null.
 */
export function alignTraces(left: readonly StepRecord[], right: readonly StepRecord[]): { rows: DiffRow[]; firstDiff: number | null } {
	const group = (steps: readonly StepRecord[]) => {
		const m = new Map<number, StepRecord[]>();
		for (const s of steps) {
			if (!DIFF_KINDS.has(s.kind) || s.witness_of) continue;
			const list = m.get(s.turn) ?? [];
			list.push(s);
			m.set(s.turn, list);
		}
		return m;
	};
	const l = group(left);
	const r = group(right);
	const turns = Array.from(new Set([...l.keys(), ...r.keys()])).sort((a, b) => a - b);
	const rows = turns.map((turn) => {
		const a = l.get(turn) ?? [];
		const b = r.get(turn) ?? [];
		const same = a.length === b.length && a.every((s, i) => stepSignature(s) === stepSignature(b[i]));
		return { turn, left: a, right: b, same };
	});
	const first = rows.find((row) => !row.same);
	return { rows, firstDiff: first ? first.turn : null };
}
