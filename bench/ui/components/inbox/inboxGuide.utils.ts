// Pure helpers of the "what should have happened" accept form: its starting values from the
// recorded session, its validation, and the guide the accept sends. No I/O, so each is tested alone.
import type { InboxGuide, InboxGuideCall, InboxSession, InboxTaskKind } from "@/lib/types/bench";

/** How the accept writes the task: finished from the form, or as a draft to finish by hand. */
export type GuideMode = "guided" | "draft";

/** One recorded call in the form: whether a correct agent makes it, and its arguments as JSON text. */
export interface GuideCallRow {
	tool: string;
	args: string;
	/** "right": a correct agent makes this call; "wrong": it must never be made; "skip": not checked. */
	verdict: "right" | "wrong" | "skip";
	/** From the recording, or added by the person. */
	added?: boolean;
}

export interface GuideFormValues {
	mode: GuideMode;
	instruction: string;
	keepTurns: boolean;
	/** The recorded session was right (a golden). */
	useRecorded: boolean;
	/** qa and memory: one fact of the right answer per line. */
	answers: string;
	recordedWrong: boolean;
	/** tool-use and mcp. */
	calls: GuideCallRow[];
	ordered: boolean;
	replyRubric: string;
	switchGrader: boolean;
}

export type GuideFormError = "answers" | "calls" | "args" | "tool";

/** Tool-use and mcp tasks are graded on tool calls; qa and memory on the answer. */
export function isToolKind(kind: InboxTaskKind): boolean {
	return kind === "tool-use" || kind === "mcp";
}

/**
 * Starting values: guided, the first request as the instruction, later turns kept, the recorded
 * calls listed (a failed call marked wrong), the recorded answer shown as wrong for a failure and as
 * right for a golden.
 */
export function guideDefaults(session: InboxSession | undefined, golden: boolean): GuideFormValues {
	const calls: GuideCallRow[] = (session?.calls ?? []).map((c) => ({
		tool: c.tool,
		args: prettyArgs(c.args),
		verdict: golden ? "right" : c.is_error ? "wrong" : "right",
	}));
	return {
		mode: session ? "guided" : "draft",
		instruction: "",
		keepTurns: (session?.requests.length ?? 0) > 1,
		useRecorded: golden,
		answers: golden ? (session?.final_answer ?? "") : "",
		recordedWrong: !golden && Boolean(session?.final_answer),
		calls,
		ordered: calls.length > 1,
		replyRubric: "",
		switchGrader: false,
	};
}

/** Arguments as indented JSON when they parse, else as recorded. */
export function prettyArgs(args: string): string {
	try {
		const v: unknown = JSON.parse(args);
		if (v && typeof v === "object" && !Array.isArray(v) && Object.keys(v).length === 0) return "";
		return JSON.stringify(v);
	} catch {
		return args;
	}
}

/** Arguments typed into the form: empty is none, else a JSON object. */
export function parseArgs(text: string): { ok: true; args?: Record<string, unknown> } | { ok: false } {
	const trimmed = text.trim();
	if (trimmed === "") return { ok: true };
	try {
		const v: unknown = JSON.parse(trimmed);
		if (!v || typeof v !== "object" || Array.isArray(v)) return { ok: false };
		const obj = v as Record<string, unknown>;
		return Object.keys(obj).length === 0 ? { ok: true } : { ok: true, args: obj };
	} catch {
		return { ok: false };
	}
}

function answerLines(text: string): string[] {
	return text
		.split("\n")
		.map((s) => s.trim())
		.filter((s) => s !== "");
}

/** What blocks a guided accept; a draft accept is never blocked here. */
export function guideFormErrors(values: GuideFormValues, kind: InboxTaskKind): GuideFormError[] {
	if (values.mode === "draft") return [];
	const errors: GuideFormError[] = [];
	if (isToolKind(kind)) {
		if (values.calls.some((c) => c.verdict !== "skip" && c.tool.trim() === "")) errors.push("tool");
		if (values.calls.some((c) => c.verdict !== "skip" && !parseArgs(c.args).ok)) errors.push("args");
		const checked = values.calls.some((c) => c.verdict !== "skip");
		if (!checked && values.replyRubric.trim() === "") errors.push("calls");
	} else if (answerLines(values.answers).length === 0) {
		errors.push("answers");
	}
	return errors;
}

function toCall(row: GuideCallRow): InboxGuideCall {
	const parsed = parseArgs(row.args);
	const args = parsed.ok ? parsed.args : undefined;
	return { tool: row.tool.trim(), ...(args ? { args } : {}) };
}

/** The guide the accept sends, or undefined for a draft accept. Only the fields of the kind go out. */
export function guideFrom(values: GuideFormValues, kind: InboxTaskKind): InboxGuide | undefined {
	if (values.mode === "draft") return undefined;
	const guide: InboxGuide = {};
	const instruction = values.instruction.trim();
	if (instruction) guide.instruction = instruction;
	if (values.keepTurns) guide.keep_turns = true;
	if (isToolKind(kind)) {
		const right = values.calls.filter((c) => c.verdict === "right").map(toCall);
		const wrong = values.calls.filter((c) => c.verdict === "wrong").map(toCall);
		if (right.length > 0) guide.calls = right;
		if (wrong.length > 0) guide.forbidden = wrong;
		if (values.ordered && right.length > 1) guide.ordered = true;
		const rubric = values.replyRubric.trim();
		if (rubric) guide.reply_rubric = rubric;
	} else {
		guide.answers = answerLines(values.answers);
		if (values.recordedWrong) guide.recorded_wrong = true;
	}
	return guide;
}
