// The retention dry run (G1, M8-55): GET /data/retention?preview_days=N answers the policy plus
// how many comparisons and runs a shorter retention would delete. Org admins only. Servers
// without the field leave `preview` out; callers keep the plain confirm then.
// The query and types now live in the shared files; this file keeps only previewOf and passes the
// shared names through until dataControls.tsx imports them directly.
import type { RetentionPreview, RetentionWithPreview } from "@/lib/types/serverFields";

export type { RetentionPreview, RetentionWithPreview };
export { useLazyGetRetentionPreviewQuery } from "@/lib/store/apis/platformApi";

/** The preview of the answer when it is well formed and for the asked days, else null. */
export function previewOf(answer: RetentionWithPreview | undefined, days: number): RetentionPreview | null {
	const p = answer?.preview;
	if (!p || p.days !== days || typeof p.runs !== "number" || typeof p.experiments !== "number") return null;
	return p;
}
