// The retention dry run (G1, M8-55): GET /data/retention?preview_days=N answers the policy plus
// how many comparisons and runs a shorter retention would delete. Org admins only. Servers
// without the field leave `preview` out; callers keep the plain confirm then.
import { benchApi } from "@/lib/store/apis/benchApi";
import type { RetentionPolicy } from "@/lib/types/dataops";

/** What a retention of `days` would delete at the next sweep (an upper bound). */
export interface RetentionPreview {
	days: number;
	experiments: number;
	runs: number;
}

export type RetentionWithPreview = RetentionPolicy & { preview?: RetentionPreview };

/** The preview of the answer when it is well formed and for the asked days, else null. */
export function previewOf(answer: RetentionWithPreview | undefined, days: number): RetentionPreview | null {
	const p = answer?.preview;
	if (!p || p.days !== days || typeof p.runs !== "number" || typeof p.experiments !== "number") return null;
	return p;
}

export const retentionPreviewApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRetentionPreview: builder.query<RetentionWithPreview, number>({
			query: (days) => ({ url: "/data/retention", params: { preview_days: days } }),
			keepUnusedDataFor: 0,
		}),
	}),
});

export const { useLazyGetRetentionPreviewQuery } = retentionPreviewApi;
