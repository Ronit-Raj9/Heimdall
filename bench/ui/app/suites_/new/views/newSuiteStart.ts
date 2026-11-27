// Which starting point /suites/new opens on. Two URL fields lead here: `?start=` (the add
// chooser's rows, for example start=code) and `?template=` (lane 6a's links, for example
// template=support or template=qa). Both are read; `start` wins when both are given.

export type StartingPoint = "questions" | "code" | "other";
export const STARTS = ["questions", "code", "other"] as const satisfies readonly StartingPoint[];

/** `?template=` values that name a starting point rather than a template id. */
const TEMPLATE_STARTS: Record<string, StartingPoint> = {
	support: "questions",
	qa: "questions",
	questions: "questions",
	commits: "code",
	code: "code",
};

/** The starting point from the URL, "questions" when neither field names one. Pure. */
export function startFromUrl(start: string | null | undefined, template: string | null | undefined): StartingPoint {
	if (start && (STARTS as readonly string[]).includes(start)) return start as StartingPoint;
	const fromTemplate = template ? TEMPLATE_STARTS[template.trim().toLowerCase()] : undefined;
	return fromTemplate ?? "questions";
}
