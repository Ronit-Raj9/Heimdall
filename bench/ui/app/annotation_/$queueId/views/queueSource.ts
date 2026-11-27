import type { AnnotationQueue } from "@/lib/types/annotation";

// M6-68: the queue page's subtitle names where its items come from: what people label and, when
// set, the benchmark. The queue's own description follows it.

export type QueueSourceKey = "source.runsOf" | "source.runs" | "source.judgeOf" | "source.judge";

/** The uf6b key and values of the source line. Pure. */
export function queueSourceLine(queue: Pick<AnnotationQueue, "kind" | "benchmark">): { key: QueueSourceKey; benchmark?: string } {
	const benchmark = queue.benchmark?.trim() || undefined;
	if (queue.kind === "judge") return benchmark ? { key: "source.judgeOf", benchmark } : { key: "source.judge" };
	return benchmark ? { key: "source.runsOf", benchmark } : { key: "source.runs" };
}
